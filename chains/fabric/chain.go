// implementa core.Chain contra o chaincode cc_ibc
// via fabric-gateway. Cada sdk.Msg vira uma transação Fabric separada
package fabric

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	transfertypes "github.com/cosmos/ibc-go/v8/modules/apps/transfer/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	conntypes "github.com/cosmos/ibc-go/v8/modules/core/03-connection/types"
	chantypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	host "github.com/cosmos/ibc-go/v8/modules/core/24-host"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"

	"github.com/hyperledger-labs/yui-relayer/core"

	fabricmsp "github.com/hyperledger-labs/yui-relayer/lightclients/fabric-msp"
)

type Chain struct {
	config ChainConfig

	gw       *gatewayConn
	PathEnd  *core.PathEnd
	codec    codec.ProtoCodecMarshaler
	listener core.MsgEventListener

	mu      sync.Mutex
	results map[string]*msgResult
}

var _ core.Chain = (*Chain)(nil)

func (c *Chain) ChainID() string { return c.config.ChainId }

func (c *Chain) Codec() codec.ProtoCodecMarshaler { return c.codec }

func (c *Chain) Path() *core.PathEnd { return c.PathEnd }

func (c *Chain) GetAddress() (sdk.AccAddress, error) {
	// Fabric não tem uma conta sdk.AccAddress nativa - deriva um
	// endereço estável a partir do msp_id
	return sdk.AccAddress([]byte(c.config.MspId)), nil
}

func (c *Chain) GetAddressString() (string, error) {
	return c.config.MspId, nil
}

func (c *Chain) Init(homePath string, timeout time.Duration, cdc codec.ProtoCodecMarshaler, debug bool) error {
	gw, err := connectGateway(c.config)
	if err != nil {
		return err
	}
	c.gw = gw
	c.codec = cdc
	c.results = make(map[string]*msgResult)
	return nil
}

func (c *Chain) SetRelayInfo(p *core.PathEnd, _ *core.ProvableChain, _ *core.PathEnd) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("path on chain %s failed to set: %w", c.ChainID(), err)
	}
	c.PathEnd = p
	return nil
}

func (c *Chain) SetupForRelay(ctx context.Context) error {
	return nil
}

func (c *Chain) RegisterMsgEventListener(l core.MsgEventListener) {
	c.listener = l
}

func (c *Chain) AverageBlockTime() time.Duration {
	return time.Duration(c.config.AverageBlockTimeMsec) * time.Millisecond
}

// LatestHeight/Timestamp: a "altura" do chain Fabric é o Sequence atual
// do commitment log não um block height Tendermint-like.
func (c *Chain) LatestHeight(ctx context.Context) (ibcexported.Height, error) {
	value, _, err := c.querySequence()
	if err != nil {
		return nil, err
	}
	return clienttypes.NewHeight(0, value), nil
}

func (c *Chain) Timestamp(ctx context.Context, height ibcexported.Height) (time.Time, error) {
	value, timestamp, err := c.querySequence()
	if err != nil {
		return time.Time{}, err
	}
	if height.GetRevisionHeight() != value {
		return time.Time{}, fmt.Errorf("fabric: historical timestamps not supported (MVP) - requested height %d, latest is %d", height.GetRevisionHeight(), value)
	}
	return time.Unix(timestamp, 0), nil
}

func (c *Chain) querySequence() (value uint64, timestamp int64, err error) {
	bz, err := c.gw.evaluate("QuerySequence")
	if err != nil {
		return 0, 0, err
	}
	parts := strings.SplitN(string(bz), ",", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("fabric: unexpected QuerySequence response %q", bz)
	}
	value, err = strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	timestamp, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	return value, timestamp, nil
}

// --- SendMsgs / GetMsgResult ---

type msgResult struct {
	height  clienttypes.Height
	success bool
	errMsg  string
	events  []core.MsgEventLog
}

func (r *msgResult) BlockHeight() clienttypes.Height { return r.height }
func (r *msgResult) Status() (bool, string)          { return r.success, r.errMsg }
func (r *msgResult) Events() []core.MsgEventLog      { return r.events }

var _ core.MsgResult = (*msgResult)(nil)

func (c *Chain) SendMsgs(ctx context.Context, msgs []sdk.Msg) ([]core.MsgID, error) {
	ids := make([]core.MsgID, 0, len(msgs))
	for _, msg := range msgs {
		id, err := c.sendMsg(msg)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if c.listener != nil {
		if err := c.listener.OnSentMsg(ctx, msgs); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// txNameFor mapeia o tipo concreto do sdk.Msg pro nome da transação do mesmo conjunto exposto em cc_ibc.go.
func (c *Chain) sendMsg(msg sdk.Msg) (core.MsgID, error) {
	bz, err := c.codec.Marshal(msg)
	if err != nil {
		return nil, err
	}
	arg := base64.StdEncoding.EncodeToString(bz)

	var txName string
	switch msg.(type) {
	case *clienttypes.MsgCreateClient:
		txName = "CreateClient"
	case *clienttypes.MsgUpdateClient:
		txName = "UpdateClient"
	case *conntypes.MsgConnectionOpenInit:
		txName = "ConnectionOpenInit"
	case *conntypes.MsgConnectionOpenTry:
		txName = "ConnectionOpenTry"
	case *conntypes.MsgConnectionOpenAck:
		txName = "ConnectionOpenAck"
	case *conntypes.MsgConnectionOpenConfirm:
		txName = "ConnectionOpenConfirm"
	case *chantypes.MsgChannelOpenInit:
		txName = "ChannelOpenInit"
	case *chantypes.MsgChannelOpenTry:
		txName = "ChannelOpenTry"
	case *chantypes.MsgChannelOpenAck:
		txName = "ChannelOpenAck"
	case *chantypes.MsgChannelOpenConfirm:
		txName = "ChannelOpenConfirm"
	case *chantypes.MsgRecvPacket:
		txName = "RecvPacket"
	case *chantypes.MsgAcknowledgement:
		txName = "Acknowledgement"
	case *chantypes.MsgTimeout:
		txName = "Timeout"
	default:
		return nil, fmt.Errorf("fabric: unsupported msg type %T", msg)
	}

	result, err := c.gw.submit(txName, arg)
	if err != nil {
		c.mu.Lock()
		c.results[""] = &msgResult{success: false, errMsg: err.Error()}
		c.mu.Unlock()
		return nil, err
	}

	value, _, seqErr := c.querySequence()
	height := clienttypes.NewHeight(0, value)
	if seqErr != nil {
		height = clienttypes.NewHeight(0, 0)
	}

	events, err := parseGeneratedIdentifierEvent(msg, result.Result)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.results[result.TxID] = &msgResult{height: height, success: true, events: events}
	c.mu.Unlock()

	return NewMsgID(result.TxID), nil
}

// parseGeneratedIdentifierEvent monta o core.MsgEventLog de identificador
// gerado o core do yui-relayer usa isso pra descobrir o ID
// recém-criado e gravar no path
func parseGeneratedIdentifierEvent(msg sdk.Msg, result []byte) ([]core.MsgEventLog, error) {
	switch msg.(type) {
	case *clienttypes.MsgCreateClient:
		return []core.MsgEventLog{&core.EventGenerateClientIdentifier{ID: string(result)}}, nil
	case *conntypes.MsgConnectionOpenInit, *conntypes.MsgConnectionOpenTry:
		return []core.MsgEventLog{&core.EventGenerateConnectionIdentifier{ID: string(result)}}, nil
	case *chantypes.MsgChannelOpenInit:
		bz, err := base64.StdEncoding.DecodeString(string(result))
		if err != nil {
			return nil, err
		}
		var resp chantypes.MsgChannelOpenInitResponse
		if err := gogoproto.Unmarshal(bz, &resp); err != nil {
			return nil, err
		}
		return []core.MsgEventLog{&core.EventGenerateChannelIdentifier{ID: resp.ChannelId}}, nil
	case *chantypes.MsgChannelOpenTry:
		bz, err := base64.StdEncoding.DecodeString(string(result))
		if err != nil {
			return nil, err
		}
		var resp chantypes.MsgChannelOpenTryResponse
		if err := gogoproto.Unmarshal(bz, &resp); err != nil {
			return nil, err
		}
		return []core.MsgEventLog{&core.EventGenerateChannelIdentifier{ID: resp.ChannelId}}, nil
	default:
		return nil, nil
	}
}

// GetMsgResult: Fabric já confirma o commit dentro de SendMsgs
// (síncrono) - só devolve o resultado já capturado.
func (c *Chain) GetMsgResult(ctx context.Context, id core.MsgID) (core.MsgResult, error) {
	fabID, ok := id.(*MsgID)
	if !ok {
		return nil, fmt.Errorf("fabric: unexpected MsgID type %T", id)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.results[fabID.TxId]
	if !ok {
		return nil, fmt.Errorf("fabric: no result found for tx %s", fabID.TxId)
	}
	return r, nil
}

// --- ICS20Querier
func (c *Chain) QueryBalance(ctx core.QueryContext, address sdk.AccAddress) (sdk.Coins, error) {
	return nil, fmt.Errorf("fabric: QueryBalance not supported (use cc_ibc's QueryBalance(account, denom) tx directly)")
}

func (c *Chain) QueryDenomTraces(ctx core.QueryContext, offset, limit uint64) (*transfertypes.QueryDenomTracesResponse, error) {
	return nil, fmt.Errorf("fabric: QueryDenomTraces not supported")
}

// --- channel upgrade: fora de escopo

func (c *Chain) QueryChannelUpgrade(ctx core.QueryContext) (*chantypes.QueryUpgradeResponse, error) {
	return nil, fmt.Errorf("fabric: channel upgrade not supported")
}

func (c *Chain) QueryChannelUpgradeError(ctx core.QueryContext) (*chantypes.QueryUpgradeErrorResponse, error) {
	return nil, fmt.Errorf("fabric: channel upgrade not supported")
}

func (c *Chain) QueryCanTransitionToFlushComplete(ctx core.QueryContext) (bool, error) {
	return false, fmt.Errorf("fabric: channel upgrade not supported")
}

// --- ICS02/03/04 Querier ---

func (c *Chain) QueryClientState(ctx core.QueryContext) (*clienttypes.QueryClientStateResponse, error) {
	icsPath := host.FullClientStatePath(c.PathEnd.ClientID)
	value, proof, height, err := c.proveAndQuery(icsPath)
	if err != nil {
		return nil, err
	}
	var any codectypes.Any
	if err := c.codec.Unmarshal(value, &any); err != nil {
		return nil, err
	}
	return &clienttypes.QueryClientStateResponse{ClientState: &any, Proof: proof, ProofHeight: height}, nil
}

func (c *Chain) QueryClientConsensusState(ctx core.QueryContext, dstClientConsHeight ibcexported.Height) (*clienttypes.QueryConsensusStateResponse, error) {
	h, ok := dstClientConsHeight.(clienttypes.Height)
	if !ok {
		h = clienttypes.NewHeight(dstClientConsHeight.GetRevisionNumber(), dstClientConsHeight.GetRevisionHeight())
	}
	icsPath := host.FullConsensusStatePath(c.PathEnd.ClientID, h)
	value, proof, height, err := c.proveAndQuery(icsPath)
	if err != nil {
		return nil, err
	}
	var any codectypes.Any
	if err := c.codec.Unmarshal(value, &any); err != nil {
		return nil, err
	}
	return &clienttypes.QueryConsensusStateResponse{ConsensusState: &any, Proof: proof, ProofHeight: height}, nil
}

func (c *Chain) QueryConnection(ctx core.QueryContext, connectionID string) (*conntypes.QueryConnectionResponse, error) {
	icsPath := host.ConnectionPath(connectionID)
	value, proof, height, err := c.proveAndQuery(icsPath)
	if err != nil {
		return nil, err
	}
	var conn conntypes.ConnectionEnd
	if err := c.codec.Unmarshal(value, &conn); err != nil {
		return nil, err
	}
	return &conntypes.QueryConnectionResponse{Connection: &conn, Proof: proof, ProofHeight: height}, nil
}

func (c *Chain) QueryChannel(ctx core.QueryContext) (*chantypes.QueryChannelResponse, error) {
	icsPath := host.ChannelPath(c.PathEnd.PortID, c.PathEnd.ChannelID)
	value, proof, height, err := c.proveAndQuery(icsPath)
	if err != nil {
		return nil, err
	}
	var ch chantypes.Channel
	if err := c.codec.Unmarshal(value, &ch); err != nil {
		return nil, err
	}
	return &chantypes.QueryChannelResponse{Channel: &ch, Proof: proof, ProofHeight: height}, nil
}

func (c *Chain) QueryNextSequenceReceive(ctx core.QueryContext) (*chantypes.QueryNextSequenceReceiveResponse, error) {
	icsPath := host.NextSequenceRecvPath(c.PathEnd.PortID, c.PathEnd.ChannelID)
	value, proof, height, err := c.proveAndQuery(icsPath)
	if err != nil {
		return nil, err
	}
	seq := sdk.BigEndianToUint64(value)
	return &chantypes.QueryNextSequenceReceiveResponse{NextSequenceReceive: seq, Proof: proof, ProofHeight: height}, nil
}

func (c *Chain) QueryUnreceivedPackets(ctx core.QueryContext, seqs []uint64) ([]uint64, error) {
	var unreceived []uint64
	for _, seq := range seqs {
		bz, err := c.gw.evaluate("QueryPacketReceipt", c.PathEnd.PortID, c.PathEnd.ChannelID, strconv.FormatUint(seq, 10))
		if err != nil {
			return nil, err
		}
		if string(bz) != "true" {
			unreceived = append(unreceived, seq)
		}
	}
	return unreceived, nil
}

func (c *Chain) QueryUnreceivedAcknowledgements(ctx core.QueryContext, seqs []uint64) ([]uint64, error) {
	var unreceived []uint64
	for _, seq := range seqs {
		icsPath := host.PacketCommitmentPath(c.PathEnd.PortID, c.PathEnd.ChannelID, seq)
		if _, _, _, err := c.proveAndQuery(icsPath); err == nil {
			// commitment ainda existe -> ack ainda não foi processado
			unreceived = append(unreceived, seq)
		}
	}
	return unreceived, nil
}

// queryNextSequenceSend devolve a próxima sequence de envio do canal
// desta chain (QueryNextSequenceSend, cc_ibc.go) - até onde iterar
// procurando pacotes enviados.
func (c *Chain) queryNextSequenceSend() (uint64, error) {
	bz, err := c.gw.evaluate("QueryNextSequenceSend", c.PathEnd.PortID, c.PathEnd.ChannelID)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(string(bz), 10, 64)
}

// querySentPacket reconstrói o channeltypes.Packet enviado nessa
// sequence, gravado no chaincode no momento do envio (QuerySentPacket,
// cc_ibc.go / internal/ibcadapter/transfer_send.go) - necessário porque
// o ChannelKeeper real só grava o hash do commitment no state, nunca o
// pacote em si (o Cosmos reconstrói via busca de eventos ABCI da tx
// original, ver Chain.querySentPacket em
// src_codes/yui-relayer/chains/tendermint/query.go; Fabric não expõe
// esse tipo de índice de eventos via fabric-gateway pro relayer, daí o
// store dedicado do lado chaincode).
func (c *Chain) querySentPacket(seq uint64) (*chantypes.Packet, error) {
	bz, err := c.gw.evaluate("QuerySentPacket", c.PathEnd.PortID, c.PathEnd.ChannelID, strconv.FormatUint(seq, 10))
	if err != nil {
		return nil, err
	}
	var packet chantypes.Packet
	if err := json.Unmarshal(bz, &packet); err != nil {
		return nil, fmt.Errorf("fabric: cannot unmarshal sent packet %d: %w", seq, err)
	}
	return &packet, nil
}

// QueryUnfinalizedRelayPackets descobre os pacotes que esta chain
// enviou (via SendTransfer) e que a contraparte ainda não recebeu.
// Fabric tem finalidade instantânea, sem reorg - não existe
// o conceito de "commitado mas ainda não finalizado" que dá nome a esta
// função na interface core.Chain (compartilhada com chains que têm essa
// noção, como Tendermint/Ethereum). Mas a função continua sendo a única
// fonte real de descoberta de pacotes enviados: QueryUnreceivedPackets
// só FILTRA uma lista de sequences já dada, não descobre nada sozinha -
// devolver sempre nil aqui (como antes) fazia qualquer pacote enviado
// por esta chain nunca ser encontrado por `tx relay`, mesmo depois de
// commitado com sucesso
func (c *Chain) QueryUnfinalizedRelayPackets(ctx core.QueryContext, counterparty core.LightClientICS04Querier) (core.PacketInfoList, error) {
	nextSeq, err := c.queryNextSequenceSend()
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to query next sequence send: %w", err)
	}

	// EventHeight não tem um equivalente real aqui (Fabric não indexa
	// eventos por altura histórica pro relayer, e Chain.Timestamp deste
	// módulo só aceita a altura atual - "historical timestamps not
	// supported (MVP)"). Usa a altura atual (mesma que LatestHeight
	// devolve) pros PacketInfo devolvidos - suficiente pro único
	// consumidor real (updateBacklogMetrics, telemetria), que só usa
	// EventHeight pra Timestamp() de uma chain que aceita esse valor.
	currentSeq, _, err := c.querySequence()
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to query current sequence: %w", err)
	}
	eventHeight := clienttypes.NewHeight(0, currentSeq)

	var packets core.PacketInfoList
	for seq := uint64(1); seq < nextSeq; seq++ {
		packet, err := c.querySentPacket(seq)
		if err != nil {
			// Não deveria acontecer (a sequence só avança em SendPacket
			// bem-sucedido, que sempre grava o QuerySentPacket
			// correspondente antes de devolver) - ignora em vez de
			// derrubar a query inteira por causa de uma sequence
			// isolada.
			continue
		}
		packets = append(packets, &core.PacketInfo{Packet: *packet, EventHeight: eventHeight})
	}
	if len(packets) == 0 {
		return nil, nil
	}

	counterpartyHeader, err := counterparty.GetLatestFinalizedHeader(ctx.Context())
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to get counterparty latest finalized header: %w", err)
	}
	counterpartyCtx := core.NewQueryContext(ctx.Context(), counterpartyHeader.GetHeight())

	seqs, err := counterparty.QueryUnreceivedPackets(counterpartyCtx, packets.ExtractSequenceList())
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to query counterparty for unreceived packets: %w", err)
	}
	return packets.Filter(seqs), nil
}

// queryReceivedHighSequence devolve a maior sequence já recebida no
// canal desta chain (QueryReceivedHighSequence, cc_ibc.go) - até onde
// iterar procurando acknowledgements escritas por esta chain.
func (c *Chain) queryReceivedHighSequence() (uint64, error) {
	bz, err := c.gw.evaluate("QueryReceivedHighSequence", c.PathEnd.PortID, c.PathEnd.ChannelID)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(string(bz), 10, 64)
}

// queryReceivedPacket reconstrói o Packet recebido e a Acknowledgement
// escrita pra ele nessa sequence, gravados no chaincode no momento do
// recebimento (QueryReceivedPacket, cc_ibc.go /
// internal/ibcadapter/msg_server.go) - mesmo raciocínio de
// querySentPacket: o ChannelKeeper real só grava o hash
// (CommitAcknowledgement) no state, nunca a Acknowledgement em si.
func (c *Chain) queryReceivedPacket(seq uint64) (*chantypes.Packet, []byte, error) {
	bz, err := c.gw.evaluate("QueryReceivedPacket", c.PathEnd.PortID, c.PathEnd.ChannelID, strconv.FormatUint(seq, 10))
	if err != nil {
		return nil, nil, err
	}
	var resp struct {
		Packet          chantypes.Packet `json:"packet"`
		Acknowledgement []byte           `json:"acknowledgement"`
	}
	if err := json.Unmarshal(bz, &resp); err != nil {
		return nil, nil, fmt.Errorf("fabric: cannot unmarshal received packet %d: %w", seq, err)
	}
	return &resp.Packet, resp.Acknowledgement, nil
}

// QueryUnfinalizedRelayAcknowledgements descobre as acknowledgements que
// esta chain escreveu (ao processar RecvPacket) e que a contraparte
// (remetente original do pacote) ainda não recebeu de volta. Mesma
// lógica/mesmo bug de QueryUnfinalizedRelayPackets acima, só que do lado
// receptor: devolver sempre nil aqui (como antes) fazia
// `tx relay-acknowledgements` nunca encontrar nada pra relayar de volta
// quando esta chain era o destino do pacote (ex.: cosmos->fabric) -
// reproduzido: RecvPacket bem-sucedido na fabric (voucher creditado com
// saldo correto), mas relay-acknowledgements sempre via num_src:0
// num_dst:0, deixando o commitment do remetente original nunca limpo.
func (c *Chain) QueryUnfinalizedRelayAcknowledgements(ctx core.QueryContext, counterparty core.LightClientICS04Querier) (core.PacketInfoList, error) {
	highSeq, err := c.queryReceivedHighSequence()
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to query received high sequence: %w", err)
	}

	currentSeq, _, err := c.querySequence()
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to query current sequence: %w", err)
	}
	eventHeight := clienttypes.NewHeight(0, currentSeq)

	var packets core.PacketInfoList
	for seq := uint64(1); seq <= highSeq; seq++ {
		packet, ack, err := c.queryReceivedPacket(seq)
		if err != nil {
			// Canais UNORDERED não garantem sequences contíguas -
			// ignora sequences não encontradas em vez de derrubar a
			// query inteira.
			continue
		}
		packets = append(packets, &core.PacketInfo{Packet: *packet, Acknowledgement: ack, EventHeight: eventHeight})
	}
	if len(packets) == 0 {
		return nil, nil
	}

	counterpartyHeader, err := counterparty.GetLatestFinalizedHeader(ctx.Context())
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to get counterparty latest finalized header: %w", err)
	}
	counterpartyCtx := core.NewQueryContext(ctx.Context(), counterpartyHeader.GetHeight())

	seqs, err := counterparty.QueryUnreceivedAcknowledgements(counterpartyCtx, packets.ExtractSequenceList())
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to query counterparty for unreceived acknowledgements: %w", err)
	}
	return packets.Filter(seqs), nil
}

// proveAndQuery lê o valor atual em icsPath e obtém uma prova endossada
// fresca
func (c *Chain) proveAndQuery(icsPath string) ([]byte, []byte, clienttypes.Height, error) {
	result, err := c.gw.submit("ProveCommitment", icsPath)
	if err != nil {
		return nil, nil, clienttypes.Height{}, err
	}
	value, err := base64.StdEncoding.DecodeString(string(result.Result))
	if err != nil {
		return nil, nil, clienttypes.Height{}, err
	}
	proofBz, err := marshalCommitmentProof(result)
	if err != nil {
		return nil, nil, clienttypes.Height{}, err
	}
	seqValue, _, err := c.querySequence()
	if err != nil {
		return nil, nil, clienttypes.Height{}, err
	}
	return value, proofBz, clienttypes.NewHeight(0, seqValue), nil
}

// marshalCommitmentProof monta o fabricmsp.CommitmentProof real a
// partir do endosso bruto capturado pelo gateway e o
// serializa pra virar o campo Proof da resposta de query
func marshalCommitmentProof(result *endorsedResult) ([]byte, error) {
	proof := fabricmsp.CommitmentProof{
		Proposal:      result.Proposal,
		NsIndex:       result.NsIndex,
		WriteSetIndex: 0,
		Identities:    result.Idents,
		Signatures:    result.Sigs,
	}
	return gogoproto.Marshal(&proof)
}
