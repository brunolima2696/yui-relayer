package fabric_msp_test

import (
	"encoding/hex"
	"testing"
	"time"

	"cosmossdk.io/store/dbadapter"
	dbm "github.com/cosmos/cosmos-db"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	commitmenttypes "github.com/cosmos/ibc-go/v8/modules/core/23-commitment/types"

	fmsp "github.com/hyperledger-labs/yui-relayer/lightclients/fabric-msp"
)

func TestVerifyMembership_RealFabricLedgerKey(t *testing.T) {
	sid, policyBytes, configs, mspConfigBytes := buildTestPolicyAndConfigs(t)

	const icsPath = "connections/connection-0"
	value := []byte("connection-end-bytes")

	// Mesma transformação que fabricstore.StoreByName + FabricDB.Set
	// aplicam de verdade (T30): prefixo "s/k:<store>/" + hex.
	ledgerKey := hex.EncodeToString(append([]byte("s/k:ibc/"), []byte(icsPath)...))

	proposalBytes := buildEndorsedProposal(t, "cc_ibc", ledgerKey, value)
	proof := signCommitmentProof(t, sid, proposalBytes)

	info := fmsp.NewChaincodeInfo("channel-all", fmsp.NewChaincodeID("", "cc_ibc", ""), policyBytes, policyBytes)
	msps := fmsp.NewMSPInfos([]fmsp.MSPInfo{fmsp.NewMSPInfo("Org1MSP", mspConfigBytes, nil)})
	initialHeader := fmsp.NewChaincodeHeader(1, time.Now().Unix())
	cs := fmsp.NewClientState("fabric-msp-0", initialHeader, info, msps)
	_ = configs

	clientStore := dbadapter.Store{DB: dbm.NewMemDB()}
	ctx := sdk.NewContext(nil, cmtproto.Header{Time: time.Now()}, false, nil)

	path := commitmenttypes.NewMerklePath(icsPath)

	proofBz, err := gogoproto.Marshal(&proof)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.VerifyMembership(ctx, clientStore, nil, cs.GetLatestHeight(), 0, 0, proofBz, path, value); err != nil {
		t.Fatalf("esperava VerifyMembership aceitar contra a chave real do ledger (hex+prefixada), erro: %v", err)
	}

	// Prova que o caminho ANTIGO (T29, path ICS-24 cru sem transformar)
	// não bate mais - documenta o porquê da mudança, não só o novo
	// caso feliz.
	rawKeyProposal := buildEndorsedProposal(t, "cc_ibc", icsPath, value)
	rawKeyProof := signCommitmentProof(t, sid, rawKeyProposal)
	rawKeyProofBz, err := gogoproto.Marshal(&rawKeyProof)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.VerifyMembership(ctx, clientStore, nil, cs.GetLatestHeight(), 0, 0, rawKeyProofBz, path, value); err == nil {
		t.Fatal("esperava que um commitment gravado com o path ICS-24 cru (não transformado) falhasse - real ledger key é hex+prefixado")
	}
}
