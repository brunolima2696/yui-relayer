package core

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	chantypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	"github.com/hyperledger-labs/yui-relayer/log"
)

type logTestID struct{ TxHash string }

func (*logTestID) Reset()           {}
func (m *logTestID) String() string { return m.TxHash }
func (*logTestID) ProtoMessage()    {}
func (*logTestID) Is_MsgID()        {}

type logTestResult struct{ ok bool }

func (logTestResult) BlockHeight() clienttypes.Height { return clienttypes.NewHeight(0, 10) }
func (m logTestResult) Status() (bool, string)        { return m.ok, "rejected" }
func (logTestResult) Events() []MsgEventLog           { return nil }

type logTestChain struct {
	Chain
	id     string
	result MsgResult
	err    error
}

func (c logTestChain) ChainID() string                                        { return c.id }
func (c logTestChain) GetMsgResult(context.Context, MsgID) (MsgResult, error) { return c.result, c.err }

func setupLogMonitor(t *testing.T) (*relayMonitor, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	if err := log.InitLoggerWithWriter("INFO", "compact", &out, false); err != nil {
		t.Fatal(err)
	}
	return newRelayMonitor(context.Background(), "a", "b", 3*time.Second), &out
}
func logTestPacket() chantypes.Packet {
	return chantypes.Packet{Sequence: 7, SourcePort: "transfer", SourceChannel: "channel-0", DestinationPort: "transfer", DestinationChannel: "channel-2"}
}

func TestRelayLogOrderIDWithoutVP(t *testing.T) {
	m, out := setupLogMonitor(t)
	p := logTestPacket()
	p.SourcePort = "fabric-events"
	p.DestinationPort = "fabric-events"
	p.Data = []byte(`{"version":3,"id_pedido":"0x1111","action":"executeTransaction","vp":{"raw_transaction":"PRIVATE_SIGNATURE","padding":"` + strings.Repeat("x", 4500) + `"}}`)
	m.observe(context.Background(), &RelayPackets{Src: PacketInfoList{&PacketInfo{Packet: p}}}, &RelayPackets{})
	if !strings.Contains(out.String(), "id_pedido=0x1111") || !strings.Contains(out.String(), "action=executeTransaction") || strings.Contains(out.String(), "PRIVATE_SIGNATURE") || strings.Contains(out.String(), "padding") {
		t.Fatal(out.String())
	}
}
func TestRelayLogDoesNotRepeatDetectedPacketsOrAcks(t *testing.T) {
	m, out := setupLogMonitor(t)
	p := logTestPacket()
	packets := &RelayPackets{Src: PacketInfoList{&PacketInfo{Packet: p}}}
	acks := &RelayPackets{Dst: PacketInfoList{&PacketInfo{Packet: p, Acknowledgement: chantypes.NewResultAcknowledgement([]byte{1}).Acknowledgement()}}}
	for i := 0; i < 3; i++ {
		m.observe(context.Background(), packets, acks)
	}
	for _, event := range []string{"Pacote detectado", "RecvPacket confirmado", "ACK disponivel"} {
		if strings.Count(out.String(), event) != 1 {
			t.Fatal(out.String())
		}
	}
	if !strings.Contains(out.String(), "from=b | to=a") {
		t.Fatal("ACK must flow back to the packet origin")
	}
}
func TestRelayLogConfirmsOnlySuccessfulReceipts(t *testing.T) {
	for _, ok := range []bool{true, false} {
		m, out := setupLogMonitor(t)
		c := logTestChain{id: "a", result: logTestResult{ok: ok}}
		msg := &chantypes.MsgAcknowledgement{Packet: logTestPacket()}
		m.sent(context.Background(), c, []sdk.Msg{msg}, []MsgID{&logTestID{TxHash: "ABC"}})
		if strings.Contains(out.String(), "ACK confirmado") {
			t.Fatal("submission is not confirmation")
		}
		m.confirm(context.Background())
		if strings.Contains(out.String(), "ACK confirmado") != ok {
			t.Fatal(out.String())
		}
		if !ok && !strings.Contains(out.String(), "Transacao IBC rejeitada") {
			t.Fatal(out.String())
		}
	}
}
func TestRelayLogKeepsUnknownConfirmationsPending(t *testing.T) {
	m, out := setupLogMonitor(t)
	c := logTestChain{id: "a", err: fmt.Errorf("not committed")}
	m.sent(context.Background(), c, []sdk.Msg{&chantypes.MsgAcknowledgement{Packet: logTestPacket()}}, []MsgID{&logTestID{TxHash: "ABC"}})
	m.confirm(context.Background())
	if len(m.pending) != 1 || strings.Contains(out.String(), "ACK confirmado") {
		t.Fatal(out.String())
	}
}

func TestRelayLogAckCommitDoesNotImplyApplicationSuccess(t *testing.T) {
	m, out := setupLogMonitor(t)
	c := logTestChain{id: "a", result: logTestResult{ok: true}}
	ack := chantypes.NewErrorAcknowledgement(fmt.Errorf("application rejected"))
	msg := &chantypes.MsgAcknowledgement{Packet: logTestPacket(), Acknowledgement: ack.Acknowledgement()}
	m.sent(context.Background(), c, []sdk.Msg{msg}, []MsgID{&logTestID{TxHash: "ABC"}})
	m.confirm(context.Background())
	if !strings.Contains(out.String(), "ACK confirmado na origem") || !strings.Contains(out.String(), "result=erro") {
		t.Fatal(out.String())
	}
}
func TestRelayLogDetectsStaleHealth(t *testing.T) {
	m, out := setupLogMonitor(t)
	m.state = "ativo"
	m.lastCycle = time.Now().Add(-5 * time.Minute)
	m.health(context.Background())
	if !strings.Contains(out.String(), "degradado") || !strings.Contains(out.String(), "sem progresso recente") {
		t.Fatal(out.String())
	}
}

func TestRelayLogSignedPaymentAckOutcome(t *testing.T) {
	for _, test := range []struct{ status, want string }{{"success", "sucesso"}, {"reverted", "revertido"}, {"rejected", "rejeitado"}} {
		ack := chantypes.NewResultAcknowledgement([]byte(`{"id_pedido":"0x1234","status":"` + test.status + `","reason":"test"}`))
		got, _ := ackStatus(ack.Acknowledgement())
		if got != test.want {
			t.Fatalf("%s became %s", test.status, got)
		}
	}
}

func TestRelayLogSignedPaymentHashAndPrivacy(t *testing.T) {
	m, out := setupLogMonitor(t)
	p := logTestPacket()
	p.Data = []byte(`{"version":3,"action":"executeTransaction","id_pedido":"0x1111","vp":{"private_payload":"DO_NOT_LOG"}}`)
	hash := "0x" + strings.Repeat("a", 64)
	ack := chantypes.NewResultAcknowledgement([]byte(`{"id_pedido":"0x1111","status":"rejected","hash":"` + hash + `","reason":"invalid nonce"}`))
	m.observeAck(context.Background(), "a", "b", &PacketInfo{Packet: p, Acknowledgement: ack.Acknowledgement()})
	if !strings.Contains(out.String(), "result=rejeitado") || !strings.Contains(out.String(), "evm_hash="+hash) || strings.Contains(out.String(), "DO_NOT_LOG") {
		t.Fatal(out.String())
	}
	bad := chantypes.NewResultAcknowledgement([]byte(`{"id_pedido":"0x1111","status":"unknown"}`))
	if result, _ := ackStatus(bad.Acknowledgement()); result == "sucesso" {
		t.Fatal("unknown execution status reported as payment success")
	}
}
