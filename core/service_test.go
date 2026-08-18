package core_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	chantypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	mocktypes "github.com/datachainlab/ibc-mock-client/modules/light-clients/xx-mock/types"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/hyperledger-labs/yui-relayer/chains/tendermint"
	"github.com/hyperledger-labs/yui-relayer/core"
	"github.com/hyperledger-labs/yui-relayer/internal/telemetry"
	"github.com/hyperledger-labs/yui-relayer/log"
	"github.com/hyperledger-labs/yui-relayer/provers/mock"
)

// NaiveStrategyWrap wraps NaiveStrategy to capture intermediate outputs for testing
type NaiveStrategyWrap struct {
	inner *core.NaiveStrategy

	unrelayedPacketsOut          *core.RelayPackets
	processTimeoutPacketsOut     *core.RelayPackets
	unrelayedAcknowledgementsOut *core.RelayPackets
	sendInSrc                    []string
	sendInDst                    []string
}

func (s *NaiveStrategyWrap) GetType() string { return s.inner.GetType() }

func (s *NaiveStrategyWrap) SetupRelay(ctx context.Context, src, dst *core.ProvableChain) error {
	return s.inner.SetupRelay(ctx, src, dst)
}

func (s *NaiveStrategyWrap) UnrelayedPackets(ctx context.Context, src, dst *core.ProvableChain, sh core.SyncHeaders, includeRelayedButUnfinalized bool) (*core.RelayPackets, error) {
	ret, err := s.inner.UnrelayedPackets(ctx, src, dst, sh, includeRelayedButUnfinalized)
	s.unrelayedPacketsOut = ret
	return ret, err
}

func (s *NaiveStrategyWrap) ProcessTimeoutPackets(ctx context.Context, src, dst *core.ProvableChain, sh core.SyncHeaders, rp *core.RelayPackets) error {
	err := s.inner.ProcessTimeoutPackets(ctx, src, dst, sh, rp)
	s.processTimeoutPacketsOut = rp
	return err
}

func (s *NaiveStrategyWrap) RelayPackets(ctx context.Context, src, dst *core.ProvableChain, isSrcToDst bool, packets core.PacketInfoList, sh core.SyncHeaders, doExecuteRelay bool) ([]sdk.Msg, error) {
	return s.inner.RelayPackets(ctx, src, dst, isSrcToDst, packets, sh, doExecuteRelay)
}

func (s *NaiveStrategyWrap) RelayTimeoutPackets(ctx context.Context, chain *core.ProvableChain, counterparty *core.ProvableChain, packets core.PacketInfoList, sh core.SyncHeaders, doExecuteTimeout bool) ([]sdk.Msg, error) {
	return s.inner.RelayTimeoutPackets(ctx, chain, counterparty, packets, sh, doExecuteTimeout)
}

func (s *NaiveStrategyWrap) UnrelayedAcknowledgements(ctx context.Context, src, dst *core.ProvableChain, sh core.SyncHeaders, includeRelayedButUnfinalized bool) (*core.RelayPackets, error) {
	ret, err := s.inner.UnrelayedAcknowledgements(ctx, src, dst, sh, includeRelayedButUnfinalized)
	s.unrelayedAcknowledgementsOut = ret
	return ret, err
}

func (s *NaiveStrategyWrap) RelayAcknowledgements(ctx context.Context, src, dst *core.ProvableChain, isSrcToDst bool, packets core.PacketInfoList, sh core.SyncHeaders, doExecuteAck bool) ([]sdk.Msg, error) {
	return s.inner.RelayAcknowledgements(ctx, src, dst, isSrcToDst, packets, sh, doExecuteAck)
}

func (s *NaiveStrategyWrap) UpdateClients(ctx context.Context, src, dst *core.ProvableChain, isSrcToDst bool, doExecuteRelay, doExecuteAck, doExecuteTimeout bool, sh core.SyncHeaders, doRefresh bool) ([]sdk.Msg, error) {
	return s.inner.UpdateClients(ctx, src, dst, isSrcToDst, doExecuteRelay, doExecuteAck, doExecuteTimeout, sh, doRefresh)
}

func (s *NaiveStrategyWrap) Send(ctx context.Context, src, dst core.Chain, msgs *core.RelayMsgs) {
	// format message objects as strings to be easily comparable
	format := func(msgs []sdk.Msg) []string {
		ret := []string{}
		for _, msg := range msgs {
			var desc string
			switch m := msg.(type) {
			case *clienttypes.MsgUpdateClient:
				desc = fmt.Sprintf("MsgUpdateClient(%s)", m.ClientId)
			case *chantypes.MsgRecvPacket:
				desc = fmt.Sprintf("MsgRecvPacket(%v)", m.Packet.GetSequence())
			case *chantypes.MsgTimeout:
				desc = fmt.Sprintf("MsgTimeout(%v)", m.Packet.GetSequence())
			default:
				desc = fmt.Sprintf("%s()", reflect.TypeOf(msg).Elem().Name())
			}
			ret = append(ret, desc)
		}
		return ret
	}
	s.sendInSrc = format(msgs.Src)
	s.sendInDst = format(msgs.Dst)
	s.inner.Send(ctx, src, dst, msgs)
}

/**
 * create mock ProvableChain with our MockProver and gomock's MockChain.
 * about height:
 *   LatestHeight: 100
 *     NextSequenceRecv: 20
 *   LatestFinalizedHeight: 90
 *     NextSequenceRecv: 10
 *   Timestamp: height + 10000 // not that the timestamp is not used for testing but it is required to run code
 */
const (
	FINALIZED_HEIGHT                  = 90
	LATEST_HEIGHT                     = 100
	LATEST_TIMESTAMP                  = 10100
	TIMEDOUT_HEIGHT                   = 9
	NOT_TIMEDOUT_HEIGHT               = 9999
	NEXT_SEQ_RECV_AT_FINALIZED_HEIGHT = 11
	NEXT_SEQ_RECV_AT_LATEST_HEIGHT    = 22
)

var _CHAIN_STATE = struct {
	latestHeader  mocktypes.Header
	finalityDelay uint64
	sequenceRecvs map[uint64]uint64
}{
	latestHeader: mocktypes.Header{
		Height:    clienttypes.NewHeight(1, LATEST_HEIGHT),
		Timestamp: uint64(LATEST_TIMESTAMP),
	},
	finalityDelay: 10,
	sequenceRecvs: map[uint64]uint64{ // note that nextSequenceRecv is +1
		LATEST_HEIGHT:    NEXT_SEQ_RECV_AT_LATEST_HEIGHT - 1,
		FINALIZED_HEIGHT: NEXT_SEQ_RECV_AT_FINALIZED_HEIGHT - 1,
	},
}

func NewMockProvableChain(
	ctrl *gomock.Controller,
	name, order string,
	unfinalizedRelayPackets core.PacketInfoList,
	unreceivedPackets []uint64,
) *core.ProvableChain {
	chain := NewMockChain(ctrl)
	prover := mock.NewProver(chain, mock.ProverConfig{FinalityDelay: _CHAIN_STATE.finalityDelay})

	chain.EXPECT().ChainID().Return(name + "Chain").AnyTimes()
	chain.EXPECT().Codec().Return(nil).AnyTimes()
	chain.EXPECT().GetAddressString().Return("cosmos1relayer", nil).AnyTimes()
	chain.EXPECT().Path().Return(&core.PathEnd{
		ChainID:      name + "Chain",
		ClientID:     name + "Client",
		ConnectionID: name + "Conn",
		ChannelID:    name + "Chan",
		PortID:       name + "Port",
		Order:        order,
		Version:      name + "Version",
	}).AnyTimes()
	chain.EXPECT().LatestHeight(gomock.Any()).Return(_CHAIN_STATE.latestHeader.Height, nil).AnyTimes()
	chain.EXPECT().Timestamp(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, h ibcexported.Height) (time.Time, error) {
			return time.Unix(0, int64(10000+h.GetRevisionHeight())), nil
		}).AnyTimes()
	chain.EXPECT().QueryNextSequenceReceive(gomock.Any()).DoAndReturn(
		func(ctx core.QueryContext) (*chantypes.QueryNextSequenceReceiveResponse, error) {
			// get most recent sequence earlier than targetHeight
			var lastHeight uint64 = 0
			var lastSequence uint64 = 0
			for h, s := range _CHAIN_STATE.sequenceRecvs {
				if h <= ctx.Height().GetRevisionHeight() && lastHeight < h {
					lastHeight = h
					lastSequence = s
				}
			}
			return &chantypes.QueryNextSequenceReceiveResponse{
				NextSequenceReceive: lastSequence + 1,
				Proof:               []byte{},
				ProofHeight:         ctx.Height().(clienttypes.Height),
			}, nil
		}).AnyTimes()
	chain.EXPECT().QueryUnfinalizedRelayPackets(gomock.Any(), gomock.Any()).Return(unfinalizedRelayPackets, nil).AnyTimes()
	chain.EXPECT().QueryUnreceivedPackets(gomock.Any(), gomock.Any()).Return(unreceivedPackets, nil).AnyTimes()
	chain.EXPECT().QueryUnreceivedAcknowledgements(gomock.Any(), gomock.Any()).Return([]uint64{}, nil).AnyTimes()
	chain.EXPECT().QueryUnfinalizedRelayAcknowledgements(gomock.Any(), gomock.Any()).Return([]*core.PacketInfo{}, nil).AnyTimes()
	chain.EXPECT().SendMsgs(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, msgs []sdk.Msg) ([]core.MsgID, error) {
		var msgIDs []core.MsgID
		for range msgs {
			msgIDs = append(msgIDs, &tendermint.MsgID{TxHash: "", MsgIndex: 0})
		}
		return msgIDs, nil
	}).AnyTimes()
	return core.NewProvableChain(chain, prover)
}

type testCase struct {
	order                      string
	optimizeCount              uint64
	unfinalizedRelayPacketsSrc core.PacketInfoList
	unfinalizedRelayPacketsDst core.PacketInfoList
	expectSendSrc              []string
	expectSendDst              []string
}

func newPacketInfo(seq uint64, timeoutHeight uint64) *core.PacketInfo {
	return &core.PacketInfo{
		Packet: chantypes.NewPacket(
			[]byte{},
			seq,
			"srcPort",
			"srcChannel",
			"dstPort",
			"dstChannel",
			clienttypes.NewHeight(1, timeoutHeight),
			0, // timeoutTimestamp
		),
		EventHeight: clienttypes.NewHeight(1, 1),
	}
}

func TestServe(t *testing.T) {
	log.InitLoggerWithWriter("debug", "text", os.Stdout, false)
	telemetry.InitializeMetrics()

	cases := map[string]testCase{
		"empty": {
			"ORDERED",
			1,
			[]*core.PacketInfo{},
			[]*core.PacketInfo{},
			[]string{},
			[]string{},
		},
		"single": { // all src packets are relayed to dst with leading UpdateClient message
			"ORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(1, NOT_TIMEDOUT_HEIGHT), // note that nextSequenceRecv is not checked in relaying normal packets
			},
			[]*core.PacketInfo{},
			[]string{},
			[]string{
				"MsgUpdateClient(dstClient)",
				"MsgRecvPacket(1)",
			},
		},
		"multi": { // multiple packets. The rest is the same as the "single" case.
			"ORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(1, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(2, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(3, NOT_TIMEDOUT_HEIGHT),
			},
			[]*core.PacketInfo{},
			[]string{},
			[]string{
				"MsgUpdateClient(dstClient)",
				"MsgRecvPacket(1)",
				"MsgRecvPacket(2)",
				"MsgRecvPacket(3)",
			},
		},
		"queued": { // packets less than optimizeCount are queued and not relayed
			"ORDERED",
			9,
			[]*core.PacketInfo{
				newPacketInfo(1, LATEST_HEIGHT+1),
			},
			[]*core.PacketInfo{},
			[]string{},
			[]string{},
		},
		"not timeout(at border height)": { // A packet which is not timeouted is normally relayed at 100th block.
			"ORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(1, LATEST_HEIGHT+1),
			},
			[]*core.PacketInfo{},
			[]string{},
			[]string{
				"MsgUpdateClient(dstClient)",
				"MsgRecvPacket(1)",
			},
		},
		"timeout and previous packet is finalized": { // timeout. Relay back to src channel as MsgTimeout with UpdateClient.
			"ORDERED",
			1,
			[]*core.PacketInfo{
				// timeout height of the packet is 90 and latest finalized height is 90. So it is timed out.
				// nextSequenceRecv at finalized height(=90) is 11 in _CHAIN_STATE config.
				newPacketInfo(NEXT_SEQ_RECV_AT_FINALIZED_HEIGHT, FINALIZED_HEIGHT),
			},
			[]*core.PacketInfo{},
			[]string{
				"MsgUpdateClient(srcClient)",
				fmt.Sprintf("MsgTimeout(%d)", NEXT_SEQ_RECV_AT_FINALIZED_HEIGHT),
			},
			[]string{},
		},
		"timeout but previous packet is not finalized": {
			"ORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(NEXT_SEQ_RECV_AT_FINALIZED_HEIGHT+1, FINALIZED_HEIGHT),
			},
			[]*core.PacketInfo{},
			[]string{},
			[]string{},
		},
		"timeout at latest block but not at finalized block(at lower border)": { // waiting relay in finalized block
			"ORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(11, FINALIZED_HEIGHT+1),
			},
			[]*core.PacketInfo{},
			[]string{},
			[]string{},
		},
		"timeout at latest block but not at finalized block(at higher border)": { // waiting relay in finalized block
			"ORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(11, LATEST_HEIGHT),
			},
			[]*core.PacketInfo{},
			[]string{},
			[]string{},
		},
		"multiple timeouts packets in ordered channel": { // In ordered channel, later packets from timeouted packets are not relayed
			"ORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(11, TIMEDOUT_HEIGHT),
				newPacketInfo(12, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(13, TIMEDOUT_HEIGHT),
			},
			[]*core.PacketInfo{},
			[]string{
				"MsgUpdateClient(srcClient)",
				"MsgTimeout(11)",
			},
			[]string{},
		},
		"relay preceding packets before timeouted one": { // In ordered channel, only preceding packets before timeout packets are relayed.
			"ORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(11, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(12, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(13, TIMEDOUT_HEIGHT),
			},
			[]*core.PacketInfo{},
			[]string{},
			[]string{
				"MsgUpdateClient(dstClient)",
				"MsgRecvPacket(11)",
				"MsgRecvPacket(12)",
			},
		},
		"multiple timeouts packets in ordered channel(both side)": {
			"ORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(11, TIMEDOUT_HEIGHT),
				newPacketInfo(12, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(13, TIMEDOUT_HEIGHT),
			},
			[]*core.PacketInfo{
				newPacketInfo(21, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(22, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(23, TIMEDOUT_HEIGHT),
			},
			[]string{
				"MsgUpdateClient(srcClient)",
				"MsgRecvPacket(21)",
				"MsgRecvPacket(22)",
				"MsgTimeout(11)",
			},
			[]string{},
		},
		"multiple timeout packets in unordered channel": { // In unordered channel, all timeout packets are backed and others are relayed.
			"UNORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(11, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(12, TIMEDOUT_HEIGHT),
				newPacketInfo(13, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(14, TIMEDOUT_HEIGHT),
			},
			[]*core.PacketInfo{},
			[]string{
				"MsgUpdateClient(srcClient)",
				"MsgTimeout(12)",
				"MsgTimeout(14)",
			},
			[]string{
				"MsgUpdateClient(dstClient)",
				"MsgRecvPacket(11)",
				"MsgRecvPacket(13)",
			},
		},
		"unordered channel: not-finalized timeout should not break loop": {
			// In unordered channels, each packet has its own timeout, so a packet that is
			// timed out at latest height but not yet finalized must not cause a break that
			// skips subsequent packets with different timeouts.
			// Chain state: latestHeight=100, finalizedHeight=90
			"UNORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(11, FINALIZED_HEIGHT+1),  // timed out at latest but NOT at finalized -> skip
				newPacketInfo(12, TIMEDOUT_HEIGHT),     // timed out at finalized -> timeout
				newPacketInfo(13, NOT_TIMEDOUT_HEIGHT), // not timed out -> relay
			},
			[]*core.PacketInfo{},
			[]string{
				"MsgUpdateClient(srcClient)",
				"MsgTimeout(12)",
			},
			[]string{
				"MsgUpdateClient(dstClient)",
				"MsgRecvPacket(13)",
			},
		},
		"multiple timeout packets in unordered channel(both side)": { // In unordered channel, all timeout packets are backed and others are relayed.
			"UNORDERED",
			1,
			[]*core.PacketInfo{
				newPacketInfo(11, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(12, TIMEDOUT_HEIGHT),
				newPacketInfo(13, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(14, TIMEDOUT_HEIGHT),
			},
			[]*core.PacketInfo{
				newPacketInfo(21, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(22, TIMEDOUT_HEIGHT),
				newPacketInfo(23, NOT_TIMEDOUT_HEIGHT),
				newPacketInfo(24, TIMEDOUT_HEIGHT),
			},
			[]string{
				"MsgUpdateClient(srcClient)",
				"MsgRecvPacket(21)",
				"MsgRecvPacket(23)",
				"MsgTimeout(12)",
				"MsgTimeout(14)",
			},
			[]string{
				"MsgUpdateClient(dstClient)",
				"MsgRecvPacket(11)",
				"MsgRecvPacket(13)",
				"MsgTimeout(22)",
				"MsgTimeout(24)",
			},
		},
	}
	for n, c := range cases {
		t.Run(n, func(t2 *testing.T) { testServe(t2, c) })
	}
}

func testServe(t *testing.T, tc testCase) {
	ctrl := gomock.NewController(t)

	var unreceivedPacketsSrc, unreceivedPacketsDst []uint64
	for _, p := range tc.unfinalizedRelayPacketsSrc {
		unreceivedPacketsSrc = append(unreceivedPacketsSrc, p.Sequence)
	}
	for _, p := range tc.unfinalizedRelayPacketsDst {
		unreceivedPacketsDst = append(unreceivedPacketsDst, p.Sequence)
	}
	src := NewMockProvableChain(ctrl, "src", tc.order, tc.unfinalizedRelayPacketsSrc, unreceivedPacketsDst)
	dst := NewMockProvableChain(ctrl, "dst", tc.order, tc.unfinalizedRelayPacketsDst, unreceivedPacketsSrc)

	st := &NaiveStrategyWrap{inner: core.NewNaiveStrategy(false, false)}
	sh, err := core.NewSyncHeaders(context.TODO(), src, dst)
	if err != nil {
		t.Fatalf("NewSyncHeaders: %v\n", err)
	}
	var forever time.Duration = 1<<63 - 1
	srv := core.NewRelayService(st, src, dst, sh, time.Minute, forever, tc.optimizeCount, forever, tc.optimizeCount)

	srv.Serve(context.TODO())

	t.Logf("UnrelayedPackets: %v\n", st.unrelayedPacketsOut)
	t.Logf("ProcessTimeoutPackets: %v\n", st.processTimeoutPacketsOut)
	t.Logf("UnrelayedAcknowledgementsOut: %v\n", st.unrelayedAcknowledgementsOut)
	t.Logf("Send.Src: %v\n", st.sendInSrc)
	t.Logf("Send.Dst: %v\n", st.sendInDst)

	assert.Equal(t, tc.expectSendSrc, st.sendInSrc, "Send.Src")
	assert.Equal(t, tc.expectSendDst, st.sendInDst, "Send.Dst")
}
