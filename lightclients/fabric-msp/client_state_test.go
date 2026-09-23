package fabric_msp_test

import (
	"testing"

	"github.com/cosmos/ibc-go/v8/modules/core/exported"

	fabricmsp "github.com/hyperledger-labs/yui-relayer/lightclients/fabric-msp"
)

var _ exported.ClientState = (*fabricmsp.ClientState)(nil)
var _ exported.ConsensusState = (*fabricmsp.ConsensusState)(nil)
var _ exported.ClientMessage = (*fabricmsp.Header)(nil)

func newTestClientState(t *testing.T) *fabricmsp.ClientState {
	t.Helper()
	header := fabricmsp.NewChaincodeHeader(1, 1700000000)
	info := fabricmsp.NewChaincodeInfo("channel-all", fabricmsp.NewChaincodeID("path", "cc_ibc", "1.0"), []byte("endorsement-policy"), []byte("ibc-policy"))
	msps := fabricmsp.NewMSPInfos([]fabricmsp.MSPInfo{
		fabricmsp.NewMSPInfo("Org1MSP", []byte("config"), []byte("policy")),
	})
	return fabricmsp.NewClientState("fabric-msp-0", header, info, msps)
}

func TestClientTypeAndHeight(t *testing.T) {
	cs := newTestClientState(t)

	if cs.ClientType() != fabricmsp.ClientType {
		t.Fatalf("expected client type %q, got %q", fabricmsp.ClientType, cs.ClientType())
	}

	height := cs.GetLatestHeight()
	if height.GetRevisionHeight() != 1 {
		t.Fatalf("expected latest height 1, got %d", height.GetRevisionHeight())
	}
}

func TestValidate(t *testing.T) {
	cs := newTestClientState(t)
	if err := cs.Validate(); err != nil {
		t.Fatalf("expected valid client state, got error: %v", err)
	}

	blank := fabricmsp.NewClientState("", cs.LastChaincodeHeader, cs.LastChaincodeInfo, cs.LastMspInfos)
	if err := blank.Validate(); err == nil {
		t.Fatal("expected error for blank client id, got nil")
	}
}

func TestHeaderValidateBasic(t *testing.T) {
	empty := fabricmsp.NewHeader(nil, nil, nil)
	if err := empty.ValidateBasic(); err == nil {
		t.Fatal("expected error for header with all fields nil, got nil")
	}

	chaincodeHeader := fabricmsp.NewChaincodeHeader(2, 1700000001)
	withHeader := fabricmsp.NewHeader(&chaincodeHeader, nil, nil)
	if err := withHeader.ValidateBasic(); err != nil {
		t.Fatalf("expected valid header, got error: %v", err)
	}
	if withHeader.GetHeight().GetRevisionHeight() != 2 {
		t.Fatalf("expected header height 2, got %d", withHeader.GetHeight().GetRevisionHeight())
	}
}

func TestMSPInfosLookup(t *testing.T) {
	msps := fabricmsp.NewMSPInfos([]fabricmsp.MSPInfo{
		fabricmsp.NewMSPInfo("Org1MSP", []byte("config"), []byte("policy")),
	})
	if !msps.HasMSPID("Org1MSP") {
		t.Fatal("expected Org1MSP to be present")
	}
	if msps.HasMSPID("Org2MSP") {
		t.Fatal("expected Org2MSP to be absent")
	}
	if _, err := msps.FindMSPInfo("Org2MSP"); err == nil {
		t.Fatal("expected error looking up absent MSPID")
	}
}
