// gateway.go conecta ao serviço Gateway do peer Fabric não fabric-sdk-go e
// implementa o fluxo de baixo nível Propose -> Endorse -> Submit ->
// Commit necessário porque o endosso capturado é usado pra gerar o CommitmentProof que o lado Cosmos espera
package fabric

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/hyperledger/fabric-gateway/pkg/client"
	"github.com/hyperledger/fabric-gateway/pkg/identity"
	"github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/hyperledger/fabric-protos-go-apiv2/gateway"
	"github.com/hyperledger/fabric-protos-go-apiv2/ledger/rwset"
	"github.com/hyperledger/fabric-protos-go-apiv2/peer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
)

type gatewayConn struct {
	conn     *grpc.ClientConn
	gw       *client.Gateway
	contract *client.Contract
}

func connectGateway(c ChainConfig) (*gatewayConn, error) {
	certPEM, err := os.ReadFile(c.CertPath)
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to read cert_path: %w", err)
	}
	cert, err := identity.CertificateFromPEM(certPEM)
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to parse cert_path: %w", err)
	}
	id, err := identity.NewX509Identity(c.MspId, cert)
	if err != nil {
		return nil, err
	}

	keyPEM, err := os.ReadFile(c.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to read key_path: %w", err)
	}
	pk, err := identity.PrivateKeyFromPEM(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to parse key_path: %w", err)
	}
	sign, err := identity.NewPrivateKeySign(pk)
	if err != nil {
		return nil, err
	}

	tlsConf := &tls.Config{}
	if c.TlsCaCertPath != "" {
		caPEM, err := os.ReadFile(c.TlsCaCertPath)
		if err != nil {
			return nil, fmt.Errorf("fabric: failed to read tls_ca_cert_path: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("fabric: failed to parse tls_ca_cert_path as PEM")
		}
		tlsConf.RootCAs = pool
	}
	if c.GatewayHostOverride != "" {
		tlsConf.ServerName = c.GatewayHostOverride
	}

	conn, err := grpc.NewClient(c.GatewayEndpoint, grpc.WithTransportCredentials(credentials.NewTLS(tlsConf)))
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to dial gateway endpoint %s: %w", c.GatewayEndpoint, err)
	}

	gw, err := client.Connect(id, client.WithSign(sign), client.WithClientConnection(conn))
	if err != nil {
		conn.Close()
		return nil, err
	}

	network := gw.GetNetwork(c.ChannelId)
	contract := network.GetContract(c.ChaincodeName)

	return &gatewayConn{conn: conn, gw: gw, contract: contract}, nil
}

func (g *gatewayConn) Close() error {
	if err := g.gw.Close(); err != nil {
		return err
	}
	return g.conn.Close()
}

// evaluate executa uma query, sem submeter transação/sem endosso
// necessário além de 1 peer, usado pelos métodos Query*/ICS0*Querier.
func (g *gatewayConn) evaluate(name string, args ...string) ([]byte, error) {
	return g.contract.EvaluateTransaction(name, args...)
}

// endorsedResult é o resultado de uma transação submetida com sucesso,
// junto com os dados brutos de endosso Envelope assinado
type endorsedResult struct {
	TxID     string
	Result   []byte
	Proposal []byte   // ProposalResponsePayload bytes
	Idents   [][]byte // identidade por endossante (Endorsement.Endorser)
	Sigs     [][]byte // assinatura por endossante (Endorsement.Signature)
	NsIndex  uint32   // índice do namespace do cc_ibc dentro do TxReadWriteSet
}

// submit executa Propose -> Endorse -> Submit -> Commit  necessário pra capturar o
// endosso bruto. Espera até o commit (síncrono - diferente de cadeias
// baseadas em bloco, Fabric já confirma o resultado na mesma chamada).
func (g *gatewayConn) submit(name string, args ...string) (*endorsedResult, error) {
	proposal, err := g.contract.NewProposal(name, client.WithArguments(args...))
	if err != nil {
		return nil, err
	}

	transaction, err := proposal.Endorse()
	if err != nil {
		return nil, fmt.Errorf("fabric: endorse failed: %w", err)
	}

	proof, err := extractCommitmentProof(transaction, g.contract.ChaincodeName())
	if err != nil {
		return nil, fmt.Errorf("fabric: failed to extract endorsement from transaction: %w", err)
	}

	commit, err := transaction.Submit()
	if err != nil {
		return nil, fmt.Errorf("fabric: submit failed: %w", err)
	}

	status, err := commit.Status()
	if err != nil {
		return nil, fmt.Errorf("fabric: commit status failed: %w", err)
	}
	if !status.Successful {
		return nil, fmt.Errorf("fabric: transaction %s failed to commit, code %v", transaction.TransactionID(), status.Code)
	}

	return &endorsedResult{
		TxID:     transaction.TransactionID(),
		Result:   transaction.Result(),
		Proposal: proof.proposalResponsePayload,
		Idents:   proof.identities,
		Sigs:     proof.signatures,
		NsIndex:  proof.nsIndex,
	}, nil
}

type rawEndorsement struct {
	proposalResponsePayload []byte
	identities              [][]byte
	signatures              [][]byte
	nsIndex                 uint32
}

// extractCommitmentProof desmonta o Envelope assinado que
// transaction.Bytes() devolve (Envelope -> Payload -> peer.Transaction
// -> TransactionAction -> ChaincodeActionPayload -> ChaincodeEndorsedAction
// {ProposalResponsePayload, Endorsements}),mesma estrutura que
// VerifyEndorsedCommitment espera dentro de um
// CommitmentProof. O fabric-gateway não expõe isso publicamente (seu
// parser interno, transactionparser.go, é privado ao pacote client)
// replicado aqui contra os mesmos tipos públicos.
func extractCommitmentProof(transaction *client.Transaction, chaincodeName string) (*rawEndorsement, error) {
	// Transaction.Bytes() serializa um
	// gateway.PreparedTransaction{TransactionId, Envelope}, não um
	// common.Envelope direto - unmarshaling direto como Envelope produz
	// "invalid wire-format data" (campos incompatíveis). Confirmado
	// lendo fabric-protos-go-apiv2/gateway/gateway.pb.go.
	preparedBytes, err := transaction.Bytes()
	if err != nil {
		return nil, err
	}
	prepared := &gateway.PreparedTransaction{}
	if err := proto.Unmarshal(preparedBytes, prepared); err != nil {
		return nil, fmt.Errorf("failed to unmarshal prepared transaction: %w", err)
	}
	envelope := prepared.GetEnvelope()
	payload := &common.Payload{}
	if err := proto.Unmarshal(envelope.GetPayload(), payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}
	tx := &peer.Transaction{}
	if err := proto.Unmarshal(payload.GetData(), tx); err != nil {
		return nil, fmt.Errorf("failed to unmarshal transaction: %w", err)
	}
	if len(tx.GetActions()) == 0 {
		return nil, fmt.Errorf("transaction has no actions")
	}
	actionPayload := &peer.ChaincodeActionPayload{}
	if err := proto.Unmarshal(tx.GetActions()[0].GetPayload(), actionPayload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal chaincode action payload: %w", err)
	}
	endorsedAction := actionPayload.GetAction()
	idents := make([][]byte, 0, len(endorsedAction.GetEndorsements()))
	sigs := make([][]byte, 0, len(endorsedAction.GetEndorsements()))
	for _, e := range endorsedAction.GetEndorsements() {
		idents = append(idents, e.GetEndorser())
		sigs = append(sigs, e.GetSignature())
	}

	nsIndex, err := findChaincodeNamespaceIndex(endorsedAction.GetProposalResponsePayload(), chaincodeName)
	if err != nil {
		return nil, err
	}

	return &rawEndorsement{
		proposalResponsePayload: endorsedAction.GetProposalResponsePayload(),
		identities:              idents,
		signatures:              sigs,
		nsIndex:                 nsIndex,
	}, nil
}

// findChaincodeNamespaceIndex acha o índice do namespace do proprio
func findChaincodeNamespaceIndex(proposalResponsePayloadBz []byte, chaincodeName string) (uint32, error) {
	payload := &peer.ProposalResponsePayload{}
	if err := proto.Unmarshal(proposalResponsePayloadBz, payload); err != nil {
		return 0, fmt.Errorf("failed to unmarshal proposal response payload: %w", err)
	}
	action := &peer.ChaincodeAction{}
	if err := proto.Unmarshal(payload.GetExtension(), action); err != nil {
		return 0, fmt.Errorf("failed to unmarshal chaincode action: %w", err)
	}
	txRwSet := &rwset.TxReadWriteSet{}
	if err := proto.Unmarshal(action.GetResults(), txRwSet); err != nil {
		return 0, fmt.Errorf("failed to unmarshal tx read-write set: %w", err)
	}
	for i, ns := range txRwSet.GetNsRwset() {
		if ns.GetNamespace() == chaincodeName {
			return uint32(i), nil
		}
	}
	return 0, fmt.Errorf("fabric: namespace %q not found in read-write set", chaincodeName)
}
