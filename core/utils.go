package core

import (
	"context"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
)

func GetPacketsFromEvents(events []abci.Event, eventType string) ([]channeltypes.Packet, error) {
	var packets []channeltypes.Packet
	for _, ev := range events {
		if ev.Type != eventType {
			continue
		}
		// NOTE: Attributes of packet are included in one event.
		var (
			packet channeltypes.Packet
			err    error
		)
		for _, attr := range ev.Attributes {
			v := string(attr.Value)
			switch string(attr.Key) {
			case channeltypes.AttributeKeyData:
				if packet.Data == nil {
					packet.Data = []byte(attr.Value)
				}
			case channeltypes.AttributeKeyDataHex:
				var bz []byte
				bz, err = hex.DecodeString(attr.Value)
				if err != nil {
					return nil, err
				}
				packet.Data = bz
			case channeltypes.AttributeKeyTimeoutHeight:
				parts := strings.Split(v, "-")
				packet.TimeoutHeight = clienttypes.NewHeight(
					strToUint64(parts[0]),
					strToUint64(parts[1]),
				)
			case channeltypes.AttributeKeyTimeoutTimestamp:
				packet.TimeoutTimestamp = strToUint64(v)
			case channeltypes.AttributeKeySequence:
				packet.Sequence = strToUint64(v)
			case channeltypes.AttributeKeySrcPort:
				packet.SourcePort = v
			case channeltypes.AttributeKeySrcChannel:
				packet.SourceChannel = v
			case channeltypes.AttributeKeyDstPort:
				packet.DestinationPort = v
			case channeltypes.AttributeKeyDstChannel:
				packet.DestinationChannel = v
			}
			if err != nil {
				return nil, err
			}
		}
		if err := packet.ValidateBasic(); err != nil {
			return nil, err
		}
		packets = append(packets, packet)
	}
	return packets, nil
}

func FindPacketFromEventsBySequence(events []abci.Event, eventType string, seq uint64) (*channeltypes.Packet, error) {
	packets, err := GetPacketsFromEvents(events, eventType)
	if err != nil {
		return nil, err
	}
	for _, packet := range packets {
		if packet.Sequence == seq {
			return &packet, nil
		}
	}
	return nil, nil
}

type packetAcknowledgement struct {
	srcPortID    string
	srcChannelID string
	dstPortID    string
	dstChannelID string
	sequence     uint64
	data         []byte
}

func (ack packetAcknowledgement) Data() []byte {
	return ack.data
}

func GetPacketAcknowledgementsFromEvents(events []abci.Event) ([]packetAcknowledgement, error) {
	var acks []packetAcknowledgement
	for _, ev := range events {
		if ev.Type != channeltypes.EventTypeWriteAck {
			continue
		}
		var (
			ack packetAcknowledgement
			err error
		)
		for _, attr := range ev.Attributes {
			v := string(attr.Value)
			switch string(attr.Key) {
			case channeltypes.AttributeKeySequence:
				ack.sequence = strToUint64(v)
			case channeltypes.AttributeKeySrcPort:
				ack.srcPortID = v
			case channeltypes.AttributeKeySrcChannel:
				ack.srcChannelID = v
			case channeltypes.AttributeKeyDstPort:
				ack.dstPortID = v
			case channeltypes.AttributeKeyDstChannel:
				ack.dstChannelID = v
			case channeltypes.AttributeKeyAck:
				if ack.data == nil {
					ack.data = []byte(attr.Value)
				}
			case channeltypes.AttributeKeyAckHex:
				ack.data, err = hex.DecodeString(attr.Value)
			}
			if err != nil {
				return nil, err
			}
		}
		acks = append(acks, ack)
	}
	return acks, nil
}

func FindPacketAcknowledgementFromEventsBySequence(events []abci.Event, seq uint64) (*packetAcknowledgement, error) {
	acks, err := GetPacketAcknowledgementsFromEvents(events)
	if err != nil {
		return nil, err
	}
	for _, ack := range acks {
		if ack.sequence == seq {
			return &ack, nil
		}
	}
	return nil, nil
}

func strToUint64(s string) uint64 {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		panic(err)
	}
	return uint64(v)
}

func wait(ctx context.Context, d time.Duration) error {
	// NOTE: We can use time.After with Go 1.23 or later
	// cf. https://pkg.go.dev/time@go1.23.0#After
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func runUntilComplete(ctx context.Context, interval time.Duration, fn func() (bool, error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if complete, err := fn(); err != nil {
		return err
	} else if complete {
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if complete, err := fn(); err != nil {
				return err
			} else if complete {
				return nil
			}
		}
	}
}
