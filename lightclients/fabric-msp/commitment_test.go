package fabric_msp_test

import (
	"testing"
	"time"

	"cosmossdk.io/store/dbadapter"
	dbm "github.com/cosmos/cosmos-db"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/hyperledger/fabric-protos-go-apiv2/ledger/rwset/kvrwset"
	apiv2peer "github.com/hyperledger/fabric-protos-go-apiv2/peer"
	"github.com/hyperledger/fabric/common/policydsl"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	"google.golang.org/protobuf/proto"

	fmsp "github.com/hyperledger-labs/yui-relayer/lightclients/fabric-msp"
)

func buildEndorsedProposal(t *testing.T, ns, key string, value []byte) []byte {
	t.Helper()
	txRwSet := &rwsetutil.TxRwSet{
		NsRwSets: []*rwsetutil.NsRwSet{
			{
				NameSpace: ns,
				KvRwSet: &kvrwset.KVRWSet{
					Writes: []*kvrwset.KVWrite{
						{Key: key, Value: value},
					},
				},
			},
		},
	}
	resultsBytes, err := txRwSet.ToProtoBytes()
	if err != nil {
		t.Fatal(err)
	}

	cact := &apiv2peer.ChaincodeAction{
		ChaincodeId: &apiv2peer.ChaincodeID{Name: "cc_ibc"},
		Results:     resultsBytes,
	}
	extBytes, err := proto.Marshal(cact)
	if err != nil {
		t.Fatal(err)
	}

	payload := &apiv2peer.ProposalResponsePayload{Extension: extBytes}
	payloadBytes, err := proto.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return payloadBytes
}

func buildTestPolicyAndConfigs(t *testing.T) (sid interface {
	Sign([]byte) ([]byte, error)
	Serialize() ([]byte, error)
}, policyBytes []byte, configs []*fmsp.MSPPBConfig, mspConfigBytes []byte) {
	t.Helper()
	rootCertPEM, _, rootCert, rootKey := newCA(t)
	adminCertPEM, adminKeyPEM := newLeafSignedByCA(t, rootCert, rootKey)
	mspConfigBytes = buildMSPConfigBytes(t, "Org1MSP", rootCertPEM, adminCertPEM, adminKeyPEM)
	sid = newSigningIdentity(t, mspConfigBytes)

	envelope, err := policydsl.FromString("OR('Org1MSP.member')")
	if err != nil {
		t.Fatal(err)
	}
	ap := &apiv2peer.ApplicationPolicy{Type: &apiv2peer.ApplicationPolicy_SignaturePolicy{SignaturePolicy: envelope}}
	policyBytes, err = proto.Marshal(ap)
	if err != nil {
		t.Fatal(err)
	}

	mspInfos := fmsp.NewMSPInfos([]fmsp.MSPInfo{fmsp.NewMSPInfo("Org1MSP", mspConfigBytes, nil)})
	configs, err = mspInfos.GetMSPPBConfigs()
	if err != nil {
		t.Fatal(err)
	}
	return sid, policyBytes, configs, mspConfigBytes
}

func signCommitmentProof(t *testing.T, sid interface {
	Sign([]byte) ([]byte, error)
	Serialize() ([]byte, error)
}, proposalBytes []byte) fmsp.CommitmentProof {
	t.Helper()
	idBytes, err := sid.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	msg := make([]byte, len(proposalBytes)+len(idBytes))
	copy(msg[:len(proposalBytes)], proposalBytes)
	copy(msg[len(proposalBytes):], idBytes)
	sig, err := sid.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	return fmsp.CommitmentProof{
		Proposal:      proposalBytes,
		NsIndex:       0,
		WriteSetIndex: 0,
		Identities:    [][]byte{idBytes},
		Signatures:    [][]byte{sig},
	}
}

func TestVerifyEndorsedCommitment_RealCrypto(t *testing.T) {
	sid, policyBytes, configs, _ := buildTestPolicyAndConfigs(t)

	const key = "connections/connection-0"
	value := []byte("connection-end-bytes")
	proposalBytes := buildEndorsedProposal(t, "cc_ibc", key, value)
	proof := signCommitmentProof(t, sid, proposalBytes)

	ccID := &apiv2peer.ChaincodeID{Name: "cc_ibc"}

	ok, err := fmsp.VerifyEndorsedCommitment(ccID, policyBytes, proof, key, value, configs)
	if err != nil {
		t.Fatalf("esperava sucesso, erro: %v", err)
	}
	if !ok {
		t.Fatal("esperava que o commitment fosse aceito")
	}

	// valor errado na mesma chave: sem erro, mas ok=false.
	ok, err = fmsp.VerifyEndorsedCommitment(ccID, policyBytes, proof, key, []byte("valor errado"), configs)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if ok {
		t.Fatal("esperava rejeição por valor errado")
	}

	// chave errada: erro explícito (write-set não bate).
	if _, err := fmsp.VerifyEndorsedCommitment(ccID, policyBytes, proof, "chave/errada", value, configs); err == nil {
		t.Fatal("esperava erro por chave errada")
	}

	// assinatura adulterada: erro na avaliação da política.
	tamperedProof := proof
	tamperedProof.Signatures = [][]byte{append([]byte{}, proof.Signatures[0]...)}
	tamperedProof.Signatures[0][0] ^= 0xFF
	if _, err := fmsp.VerifyEndorsedCommitment(ccID, policyBytes, tamperedProof, key, value, configs); err == nil {
		t.Fatal("esperava erro por assinatura adulterada")
	}
}

func TestChaincodeHeaderUpdateState_RealCrypto(t *testing.T) {
	sid, policyBytes, configs, mspConfigBytes := buildTestPolicyAndConfigs(t)
	_ = configs

	ccID := "cc_ibc"
	newSeq := fmsp.Sequence{Value: 2, Timestamp: time.Now().Unix()}
	seqBytes, err := gogoproto.Marshal(&newSeq)
	if err != nil {
		t.Fatal(err)
	}
	proposalBytes := buildEndorsedProposal(t, ccID, fmsp.SequenceCommitmentKey, seqBytes)
	proof := signCommitmentProof(t, sid, proposalBytes)

	initialHeader := fmsp.NewChaincodeHeader(1, time.Now().Unix())
	info := fmsp.NewChaincodeInfo("channel-all", fmsp.NewChaincodeID("", ccID, ""), policyBytes, policyBytes)
	msps := fmsp.NewMSPInfos([]fmsp.MSPInfo{fmsp.NewMSPInfo("Org1MSP", mspConfigBytes, nil)})
	cs := fmsp.NewClientState("fabric-msp-0", initialHeader, info, msps)

	header := fmsp.NewHeader(&fmsp.ChaincodeHeader{Sequence: newSeq, Proof: proof}, nil, nil)

	clientStore := dbadapter.Store{DB: dbm.NewMemDB()}
	registry := codectypes.NewInterfaceRegistry()
	fmsp.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	ctx := sdk.NewContext(nil, cmtproto.Header{Time: time.Now()}, false, nil)

	if err := cs.VerifyClientMessage(ctx, cdc, clientStore, header); err != nil {
		t.Fatalf("esperava VerifyClientMessage aceitar, erro: %v", err)
	}

	heights := cs.UpdateState(ctx, cdc, clientStore, header)
	if len(heights) != 1 || heights[0].GetRevisionHeight() != 2 {
		t.Fatalf("esperava altura 2, got %+v", heights)
	}
	if cs.GetLatestHeight().GetRevisionHeight() != 2 {
		t.Fatalf("esperava GetLatestHeight()==2, got %d", cs.GetLatestHeight().GetRevisionHeight())
	}
}
