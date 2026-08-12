package core

import (
	"context"
	"errors"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
)

func TestGetPacketsFromEventsWithIBCv10Attributes(t *testing.T) {
	events := []abci.Event{
		{
			Type: channeltypes.EventTypeSendPacket,
			Attributes: []abci.EventAttribute{
				{Key: channeltypes.AttributeKeyDataHex, Value: "68656c6c6f"},
				{Key: channeltypes.AttributeKeyTimeoutHeight, Value: "0-0"},
				{Key: channeltypes.AttributeKeyTimeoutTimestamp, Value: "1"},
				{Key: channeltypes.AttributeKeySequence, Value: "1"},
				{Key: channeltypes.AttributeKeySrcPort, Value: "transfer"},
				{Key: channeltypes.AttributeKeySrcChannel, Value: "channel-2"},
				{Key: channeltypes.AttributeKeyDstPort, Value: "transfer"},
				{Key: channeltypes.AttributeKeyDstChannel, Value: "channel-1"},
				{Key: "packet_channel_ordering", Value: "ORDER_UNORDERED"},
			},
		},
	}

	packets, err := GetPacketsFromEvents(events, channeltypes.EventTypeSendPacket)
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != 1 {
		t.Fatalf("len(packets) = %d; want 1", len(packets))
	}

	packet := packets[0]
	if string(packet.Data) != "hello" {
		t.Errorf("packet.Data = %q; want %q", packet.Data, "hello")
	}
	if packet.Sequence != 1 {
		t.Errorf("packet.Sequence = %d; want 1", packet.Sequence)
	}
	if packet.SourceChannel != "channel-2" || packet.DestinationChannel != "channel-1" {
		t.Errorf("unexpected channels: %s -> %s", packet.SourceChannel, packet.DestinationChannel)
	}
}

func TestGetPacketAcknowledgementsFromEventsWithIBCv10Attributes(t *testing.T) {
	events := []abci.Event{
		{
			Type: channeltypes.EventTypeWriteAck,
			Attributes: []abci.EventAttribute{
				{Key: channeltypes.AttributeKeyDataHex, Value: "68656c6c6f"},
				{Key: channeltypes.AttributeKeySequence, Value: "1"},
				{Key: channeltypes.AttributeKeySrcPort, Value: "transfer"},
				{Key: channeltypes.AttributeKeySrcChannel, Value: "channel-2"},
				{Key: channeltypes.AttributeKeyDstPort, Value: "transfer"},
				{Key: channeltypes.AttributeKeyDstChannel, Value: "channel-1"},
				{Key: channeltypes.AttributeKeyAckHex, Value: "7b7d"},
				{Key: "msg_index", Value: "0"},
			},
		},
	}

	acks, err := GetPacketAcknowledgementsFromEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(acks) != 1 {
		t.Fatalf("len(acks) = %d; want 1", len(acks))
	}
	if string(acks[0].Data()) != "{}" {
		t.Errorf("ack.Data() = %q; want %q", acks[0].Data(), "{}")
	}
}

func TestRunUntilComplete(t *testing.T) {
	runtimeError := errors.New("runtime error")

	tests := []struct {
		name    string
		fn      func(int) (bool, error)
		attempt int
		cancel  bool
		err     error
	}{
		{
			name: "Complete immediately",
			fn: func(_ int) (bool, error) {
				return true, nil
			},
			attempt: 1,
			cancel:  false,
			err:     nil,
		},
		{
			name: "Complete on the second try",
			fn: func(attempt int) (bool, error) {
				if attempt == 2 {
					return true, nil
				} else {
					return false, nil
				}
			},
			attempt: 2,
			cancel:  false,
			err:     nil,
		},
		{
			name: "Complete on the third try",
			fn: func(attempt int) (bool, error) {
				if attempt == 3 {
					return true, nil
				} else {
					return false, nil
				}
			},
			attempt: 3,
			cancel:  false,
			err:     nil,
		},
		{
			name: "Error immediately",
			fn: func(_ int) (bool, error) {
				return false, runtimeError
			},
			attempt: 1,
			cancel:  false,
			err:     runtimeError,
		},
		{
			name: "Error on the second try",
			fn: func(attempt int) (bool, error) {
				if attempt == 2 {
					return false, runtimeError
				} else {
					return false, nil
				}
			},
			attempt: 2,
			cancel:  false,
			err:     runtimeError,
		},
		{
			name: "Error immediately with complete true",
			fn: func(_ int) (bool, error) {
				return true, runtimeError
			},
			attempt: 1,
			cancel:  false,
			err:     runtimeError,
		},
		{
			name: "Cancelled",
			fn: func(_ int) (bool, error) {
				return false, nil
			},
			attempt: 1,
			cancel:  true,
			err:     context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if tt.cancel {
				cancel()
			} else {
				defer cancel()
			}

			attempt := 0
			fn := func() (bool, error) {
				attempt++
				return tt.fn(attempt)
			}
			if err := runUntilComplete(ctx, time.Millisecond, fn); err != tt.err {
				t.Errorf("err = %v; want %v", err, tt.err)
			}
			if attempt != tt.attempt {
				t.Errorf("attempt = %v; want %v", attempt, tt.attempt)
			}
		})
	}
}
