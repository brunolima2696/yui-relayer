// prover.go implementa core.Prover ClientState/ConsensusState/Header/
// CommitmentProof - a partir de dados reais do cc_ibc (T30) via o
// gateway (gateway.go). MSPInfos/ChaincodeInfo vêm de config estática descoberta automática
// via channel config block/lifecycle querycommitted fica pra depois).
package fabric

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	gogoproto "github.com/cosmos/gogoproto/proto"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	"github.com/cosmos/ibc-go/v8/modules/core/exported"

	"github.com/hyperledger-labs/yui-relayer/core"

	fabricmsp "github.com/hyperledger-labs/yui-relayer/lightclients/fabric-msp"
)

type Prover struct {
	chain  *Chain
	config ProverConfig
}

var _ core.Prover = (*Prover)(nil)

func NewProver(chain *Chain, config ProverConfig) *Prover {
	return &Prover{chain: chain, config: config}
}

func (pr *Prover) Init(homePath string, timeout time.Duration, cdc codec.ProtoCodecMarshaler, debug bool) error {
	return nil
}

func (pr *Prover) SetRelayInfo(path *core.PathEnd, counterparty *core.ProvableChain, counterpartyPath *core.PathEnd) error {
	return nil
}

func (pr *Prover) SetupForRelay(ctx context.Context) error {
	return nil
}

// GetLatestFinalizedHeader: Fabric não tem risco de reorg
func (pr *Prover) GetLatestFinalizedHeader(ctx context.Context) (core.Header, error) {
	ch, err := pr.currentChaincodeHeader()
	if err != nil {
		return nil, err
	}
	return fabricmsp.NewHeader(ch, nil, nil), nil
}

// CreateInitialLightClientState ignora o parâmetro height explícito
func (pr *Prover) CreateInitialLightClientState(ctx context.Context, height exported.Height) (exported.ClientState, exported.ConsensusState, error) {
	ch, err := pr.currentChaincodeHeader()
	if err != nil {
		return nil, nil, err
	}
	// exported.ClientState exige LatestHeight > 0
	// (validação padrão do ibc-go, ValidateBasic de qualquer client) -
	// fabric-msp.sequence começa em 0 (nunca avançada) numa rede
	// recém-deployada, então o client inicial precisaria de altura 0,
	// sempre inválido. Avança a sequência uma vez antes de montar o
	// ClientState inicial se ainda estiver zerada.
	if ch.Sequence.Value == 0 {
		if _, err := pr.chain.gw.submit("AdvanceSequence"); err != nil {
			return nil, nil, fmt.Errorf("fabric: failed to advance initial sequence: %w", err)
		}
		ch, err = pr.currentChaincodeHeader()
		if err != nil {
			return nil, nil, err
		}
	}
	mspInfos, err := pr.loadMSPInfos()
	if err != nil {
		return nil, nil, err
	}
	ccInfo, err := pr.loadChaincodeInfo()
	if err != nil {
		return nil, nil, err
	}
	cs := fabricmsp.NewClientState(pr.chain.ChainID(), *ch, ccInfo, mspInfos)
	consState := fabricmsp.NewConsensusState(ch.Sequence.Timestamp)
	return cs, consState, nil
}

// SetupHeadersForUpdate chama a transação AdvanceSequence
// e captura o endosso via o gateway (Propose->Endorse->Submit) pra
// montar um ChaincodeHeader real com CommitmentProof - mesmo mecanismo
// de ProveCommitment (chain.go/proveAndQuery), só que pra sequência.
func (pr *Prover) SetupHeadersForUpdate(ctx context.Context, counterparty core.FinalityAwareChain, latestFinalizedHeader core.Header) (<-chan *core.HeaderOrError, error) {
	out := make(chan *core.HeaderOrError, 1)
	go func() {
		defer close(out)
		result, err := pr.chain.gw.submit("AdvanceSequence")
		if err != nil {
			out <- &core.HeaderOrError{Error: err}
			return
		}
		seqBz, err := base64.StdEncoding.DecodeString(string(result.Result))
		if err != nil {
			out <- &core.HeaderOrError{Error: err}
			return
		}
		var seq fabricmsp.Sequence
		if err := gogoproto.Unmarshal(seqBz, &seq); err != nil {
			out <- &core.HeaderOrError{Error: err}
			return
		}
		proofBz, err := marshalCommitmentProof(result)
		if err != nil {
			out <- &core.HeaderOrError{Error: err}
			return
		}
		var proof fabricmsp.CommitmentProof
		if err := gogoproto.Unmarshal(proofBz, &proof); err != nil {
			out <- &core.HeaderOrError{Error: err}
			return
		}
		chHeader := fabricmsp.ChaincodeHeader{Sequence: seq, Proof: proof}
		out <- &core.HeaderOrError{Header: fabricmsp.NewHeader(&chHeader, nil, nil)}
	}()
	return out, nil
}

// CheckRefreshRequired: MVP simplificado Fabric não tem
// um "trusting period" análogo ao Tendermint, nunca exige refresh proativo; a checagem real de
// expiração/frozen acontece em VerifyClientMessage do lado Cosmos
func (pr *Prover) CheckRefreshRequired(ctx context.Context, counterparty core.ChainInfoICS02Querier) (bool, error) {
	return false, nil
}

func (pr *Prover) ProveState(ctx core.QueryContext, path string, value []byte) ([]byte, clienttypes.Height, error) {
	got, proofBz, height, err := pr.chain.proveAndQuery(path)
	if err != nil {
		return nil, clienttypes.Height{}, err
	}
	if string(got) != string(value) {
		return nil, clienttypes.Height{}, fmt.Errorf("fabric: ProveState value mismatch at path %q", path)
	}
	return proofBz, height, nil
}

// ProveHostConsensusState: no-op documentado, mesmo padrão já usado
// nos outros dois light clients deste projeto.
func (pr *Prover) ProveHostConsensusState(ctx core.QueryContext, height exported.Height, consensusState exported.ConsensusState) ([]byte, error) {
	return nil, nil
}

func (pr *Prover) currentChaincodeHeader() (*fabricmsp.ChaincodeHeader, error) {
	value, timestamp, err := pr.chain.querySequence()
	if err != nil {
		return nil, err
	}
	ch := fabricmsp.NewChaincodeHeader(value, timestamp)
	return &ch, nil
}

func (pr *Prover) loadMSPInfos() (fabricmsp.MSPInfos, error) {
	infos := make([]fabricmsp.MSPInfo, 0, len(pr.chain.config.MspInfos))
	for _, m := range pr.chain.config.MspInfos {
		configBz, err := os.ReadFile(m.ConfigPath)
		if err != nil {
			return fabricmsp.MSPInfos{}, fmt.Errorf("fabric: failed to read msp config_path for %s: %w", m.MspId, err)
		}
		var policyBz []byte
		if m.PolicyPath != "" {
			policyBz, err = os.ReadFile(m.PolicyPath)
			if err != nil {
				return fabricmsp.MSPInfos{}, fmt.Errorf("fabric: failed to read msp policy_path for %s: %w", m.MspId, err)
			}
		}
		infos = append(infos, fabricmsp.NewMSPInfo(m.MspId, configBz, policyBz))
	}
	return fabricmsp.NewMSPInfos(infos), nil
}

func (pr *Prover) loadChaincodeInfo() (fabricmsp.ChaincodeInfo, error) {
	cc := pr.chain.config.ChaincodeInfo
	if cc == nil {
		return fabricmsp.ChaincodeInfo{}, fmt.Errorf("fabric: chaincode_info is required in ChainConfig")
	}
	endorsementPolicy, err := os.ReadFile(cc.EndorsementPolicyPath)
	if err != nil {
		return fabricmsp.ChaincodeInfo{}, fmt.Errorf("fabric: failed to read endorsement_policy_path: %w", err)
	}
	ibcPolicy, err := os.ReadFile(cc.IbcPolicyPath)
	if err != nil {
		return fabricmsp.ChaincodeInfo{}, fmt.Errorf("fabric: failed to read ibc_policy_path: %w", err)
	}
	ccID := fabricmsp.NewChaincodeID(cc.Path, cc.Name, cc.Version)
	return fabricmsp.NewChaincodeInfo(pr.chain.config.ChannelId, ccID, endorsementPolicy, ibcPolicy), nil
}
