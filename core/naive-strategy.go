package core

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"

	retry "github.com/avast/retry-go"
	sdk "github.com/cosmos/cosmos-sdk/types"
	chantypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	host "github.com/cosmos/ibc-go/v8/modules/core/24-host"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	"github.com/hyperledger-labs/yui-relayer/internal/telemetry"
	"github.com/hyperledger-labs/yui-relayer/log"
	"github.com/hyperledger-labs/yui-relayer/otelcore/semconv"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	api "go.opentelemetry.io/otel/metric"
	"golang.org/x/sync/errgroup"
)

// NaiveStrategy is an implementation of Strategy.
type NaiveStrategy struct {
	Ordered      bool
	MaxTxSize    uint64 // maximum permitted size of the msgs in a bundled relay transaction
	MaxMsgLength uint64 // maximum amount of messages in a bundled relay transaction
	srcNoAck     bool
	dstNoAck     bool

	metrics naiveStrategyMetrics
}

type naiveStrategyMetrics struct {
	srcBacklog PacketInfoList
	dstBacklog PacketInfoList
}

var _ StrategyI = (*NaiveStrategy)(nil)

func NewNaiveStrategy(srcNoAck, dstNoAck bool) *NaiveStrategy {
	return &NaiveStrategy{
		srcNoAck: srcNoAck,
		dstNoAck: dstNoAck,
	}
}

// GetType implements Strategy
func (st *NaiveStrategy) GetType() string {
	return "naive"
}

func (st *NaiveStrategy) SetupRelay(ctx context.Context, src, dst *ProvableChain) error {
	logger := GetChannelPairLogger(src, dst)
	if err := src.SetupForRelay(ctx); err != nil {
		logger.ErrorContext(ctx, "failed to setup for src", err)
		return err
	}
	if err := dst.SetupForRelay(ctx); err != nil {
		logger.ErrorContext(ctx, "failed to setup for dst", err)
		return err
	}
	return nil
}

func getQueryContext(ctx context.Context, chain *ProvableChain, sh SyncHeaders, useFinalizedHeader bool) (QueryContext, error) {
	if useFinalizedHeader {
		return sh.GetQueryContext(ctx, chain.ChainID()), nil
	} else {
		height, err := chain.LatestHeight(ctx)
		if err != nil {
			return nil, err
		}
		return NewQueryContext(ctx, height), nil
	}
}

func (st *NaiveStrategy) UnrelayedPackets(ctx context.Context, src, dst *ProvableChain, sh SyncHeaders, includeRelayedButUnfinalized bool) (*RelayPackets, error) {
	ctx, span := tracer.Start(ctx, "NaiveStrategy.UnrelayedPackets", WithChannelPairAttributes(src, dst))
	defer span.End()
	logger := GetChannelPairLogger(src, dst)
	now := time.Now()
	var (
		eg         = new(errgroup.Group)
		srcPackets PacketInfoList
		dstPackets PacketInfoList
	)

	srcCtx, err := getQueryContext(ctx, src, sh, true)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	dstCtx, err := getQueryContext(ctx, dst, sh, true)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	eg.Go(func() error {
		return retry.Do(func() error {
			var err error
			now := time.Now()
			srcPackets, err = src.QueryUnfinalizedRelayPackets(srcCtx, dst)
			if err != nil {
				return fmt.Errorf("failed to query unfinalized relay packets on src chain: %w", err)
			}
			logger.TimeTrackContext(ctx, now, "QueryUnfinalizedRelayPackets", "queried_chain", "src", "num_packets", len(srcPackets))
			return nil
		}, rtyAtt, rtyDel, rtyErr, retry.Context(ctx), retry.OnRetry(func(n uint, err error) {
			logger.InfoContext(ctx,
				"retrying to query unfinalized packet relays",
				"direction", "src",
				"height", srcCtx.Height().GetRevisionHeight(),
				"try", n+1,
				"try_limit", rtyAttNum,
				"error", err.Error(),
			)
		}))
	})

	eg.Go(func() error {
		return retry.Do(func() error {
			var err error
			now := time.Now()
			dstPackets, err = dst.QueryUnfinalizedRelayPackets(dstCtx, src)
			if err != nil {
				return fmt.Errorf("failed to query unfinalized relay packets on dst chain: %w", err)
			}
			logger.TimeTrackContext(ctx, now, "QueryUnfinalizedRelayPackets", "queried_chain", "dst", "num_packets", len(dstPackets))
			return nil
		}, rtyAtt, rtyDel, rtyErr, retry.Context(ctx), retry.OnRetry(func(n uint, err error) {
			logger.InfoContext(ctx,
				"retrying to query unfinalized packet relays",
				"direction", "dst",
				"height", dstCtx.Height().GetRevisionHeight(),
				"try", n+1,
				"try_limit", rtyAttNum,
				"error", err.Error(),
			)
		}))
	})

	if err := eg.Wait(); err != nil {
		logger.ErrorContext(ctx, "error querying packet commitments", err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	if err := st.metrics.updateBacklogMetrics(ctx, src, dst, srcPackets, dstPackets); err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	// If includeRelayedButUnfinalized is true, this function should return packets of which RecvPacket is not finalized yet.
	// In this case, filtering packets by QueryUnreceivedPackets is not needed because QueryUnfinalizedRelayPackets
	// has already returned packets that completely match this condition.
	if !includeRelayedButUnfinalized {
		srcCtx, err := getQueryContext(ctx, src, sh, false)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		dstCtx, err := getQueryContext(ctx, dst, sh, false)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}

		eg.Go(func() error {
			now := time.Now()
			seqs, err := dst.QueryUnreceivedPackets(dstCtx, srcPackets.ExtractSequenceList())
			if err != nil {
				return fmt.Errorf("failed to query unreceived packets on dst chain: %w", err)
			}
			logger.TimeTrackContext(ctx, now, "QueryUnreceivedPackets", "queried_chain", "dst", "num_seqs", len(seqs))
			srcPackets = srcPackets.Filter(seqs)
			return nil
		})

		eg.Go(func() error {
			now := time.Now()
			seqs, err := src.QueryUnreceivedPackets(srcCtx, dstPackets.ExtractSequenceList())
			if err != nil {
				return fmt.Errorf("failed to query unreceived packets on src chain: %w", err)
			}
			logger.TimeTrackContext(ctx, now, "QueryUnreceivedPackets", "queried_chain", "src", "num_seqs", len(seqs))
			dstPackets = dstPackets.Filter(seqs)
			return nil
		})

		if err := eg.Wait(); err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
	}

	defer logger.TimeTrackContext(ctx, now, "UnrelayedPackets", "num_src", len(srcPackets), "num_dst", len(dstPackets))

	return &RelayPackets{
		Src: srcPackets,
		Dst: dstPackets,
	}, nil
}

// ProcessTimeoutPackets checks for timed out packets and classifies them into timeout queues.
// Packets in rp.Src that have timed out are moved to rp.SrcTimeout.
// Packets in rp.Dst that have timed out are moved to rp.DstTimeout.
// For ordered channels, only the first timed out packet is selected, and preceding packets are kept for relay.
func (st *NaiveStrategy) ProcessTimeoutPackets(ctx context.Context, src, dst *ProvableChain, sh SyncHeaders, rp *RelayPackets) error {
	ctx, span := tracer.Start(ctx, "NaiveStrategy.ProcessTimeoutPackets", WithChannelPairAttributes(src, dst))
	defer span.End()
	logger := GetChannelPairLogger(src, dst)

	var (
		srcLatestHeight             ibcexported.Height
		srcLatestTimestamp          uint64
		srcLatestFinalizedHeight    ibcexported.Height
		srcLatestFinalizedTimestamp uint64
		dstLatestHeight             ibcexported.Height
		dstLatestTimestamp          uint64
		dstLatestFinalizedHeight    ibcexported.Height
		dstLatestFinalizedTimestamp uint64
	)

	// Get dst chain's heights and timestamps for checking src→dst packets timeout
	if len(rp.Src) > 0 {
		h, err := dst.LatestHeight(ctx)
		if err != nil {
			logger.ErrorContext(ctx, "failed to get dst.LatestHeight", err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		dstLatestHeight = h

		t, err := dst.Timestamp(ctx, dstLatestHeight)
		if err != nil {
			logger.ErrorContext(ctx, "failed to get dst.Timestamp of latestHeight", err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		dstLatestTimestamp = uint64(t.UnixNano())

		dstLatestFinalizedHeight = sh.GetLatestFinalizedHeader(dst.ChainID()).GetHeight()
		t, err = dst.Timestamp(ctx, dstLatestFinalizedHeight)
		if err != nil {
			logger.ErrorContext(ctx, "failed to get dst.Timestamp of latestFinalizedHeight", err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		dstLatestFinalizedTimestamp = uint64(t.UnixNano())
	}

	// Get src chain's heights and timestamps for checking dst→src packets timeout
	if len(rp.Dst) > 0 {
		h, err := src.LatestHeight(ctx)
		if err != nil {
			logger.ErrorContext(ctx, "failed to get src.LatestHeight", err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		srcLatestHeight = h

		t, err := src.Timestamp(ctx, srcLatestHeight)
		if err != nil {
			logger.ErrorContext(ctx, "failed to get src.Timestamp of latestHeight", err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		srcLatestTimestamp = uint64(t.UnixNano())

		srcLatestFinalizedHeight = sh.GetLatestFinalizedHeader(src.ChainID()).GetHeight()
		t, err = src.Timestamp(ctx, srcLatestFinalizedHeight)
		if err != nil {
			logger.ErrorContext(ctx, "failed to get src.Timestamp of latestFinalizedHeight", err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		srcLatestFinalizedTimestamp = uint64(t.UnixNano())
	}

	isTimeout := func(p *PacketInfo, height ibcexported.Height, timestamp uint64) bool {
		return (!p.TimeoutHeight.IsZero() && p.TimeoutHeight.LTE(height)) ||
			(p.TimeoutTimestamp != 0 && p.TimeoutTimestamp <= timestamp)
	}

	// Process src→dst packets (check timeout against dst chain)
	var srcPackets, srcTimeoutPackets PacketInfoList
	for i, p := range rp.Src {
		if isTimeout(p, dstLatestFinalizedHeight, dstLatestFinalizedTimestamp) {
			// Packet has timed out at finalized height
			if src.Path().GetOrder() == chantypes.ORDERED {
				// For ordered channel, timeout will close the channel.
				// Only process the first timed out packet, and only if previous packets are received.
				if i == 0 {
					res, err := dst.QueryNextSequenceReceive(NewQueryContext(ctx, dstLatestFinalizedHeight))
					if err != nil {
						logger.ErrorContext(ctx, "failed to QueryNextSequenceReceive for src timeout", err, "height", dstLatestFinalizedHeight)
					} else if res.NextSequenceReceive == p.Sequence {
						srcTimeoutPackets = PacketInfoList{p}
					}
				}
				break
			} else {
				// For unordered channel, collect all timed out packets
				srcTimeoutPackets = append(srcTimeoutPackets, p)
			}
		} else if isTimeout(p, dstLatestHeight, dstLatestTimestamp) {
			// Packet is timed out at latest height but not yet finalized
			if src.Path().GetOrder() == chantypes.ORDERED {
				break
			}
			// For unordered channels, skip this packet and continue processing others
		} else {
			// Packet is not timed out - keep for relay
			srcPackets = append(srcPackets, p)
		}
	}

	// Process dst→src packets (check timeout against src chain)
	var dstPackets, dstTimeoutPackets PacketInfoList
	for i, p := range rp.Dst {
		if isTimeout(p, srcLatestFinalizedHeight, srcLatestFinalizedTimestamp) {
			// Packet has timed out at finalized height
			if dst.Path().GetOrder() == chantypes.ORDERED {
				// For ordered channel, timeout will close the channel.
				if i == 0 {
					res, err := src.QueryNextSequenceReceive(NewQueryContext(ctx, srcLatestFinalizedHeight))
					if err != nil {
						logger.ErrorContext(ctx, "failed to QueryNextSequenceReceive for dst timeout", err, "height", srcLatestFinalizedHeight)
					} else if res.NextSequenceReceive == p.Sequence {
						dstTimeoutPackets = PacketInfoList{p}
					}
				}
				break
			} else {
				dstTimeoutPackets = append(dstTimeoutPackets, p)
			}
		} else if isTimeout(p, srcLatestHeight, srcLatestTimestamp) {
			if dst.Path().GetOrder() == chantypes.ORDERED {
				break
			}
		} else {
			dstPackets = append(dstPackets, p)
		}
	}

	// Update the RelayPackets
	rp.Src = srcPackets
	rp.Dst = dstPackets
	rp.SrcTimeout = srcTimeoutPackets
	rp.DstTimeout = dstTimeoutPackets

	logger.InfoContext(ctx, "processed timeout packets",
		"src_relay", len(srcPackets),
		"dst_relay", len(dstPackets),
		"src_timeout", len(srcTimeoutPackets),
		"dst_timeout", len(dstTimeoutPackets),
	)

	return nil
}

func (st *NaiveStrategy) RelayPackets(ctx context.Context, src, dst *ProvableChain, isSrcToDst bool, packets PacketInfoList, sh SyncHeaders, doExecuteRelay bool) ([]sdk.Msg, error) {
	var (
		fromChain, toChain *ProvableChain
	)
	if isSrcToDst {
		fromChain = src
		toChain = dst
	} else {
		fromChain = dst
		toChain = src
	}

	ctx, span := tracer.Start(ctx, "NaiveStrategy.RelayPackets", WithChannelPairAttributesAndKey("from", fromChain, "to", toChain))
	defer span.End()
	logger := GetChannelPairLoggerRelative(fromChain, toChain)
	defer logger.TimeTrackContext(ctx, time.Now(), "RelayPackets", "num", len(packets))

	var msgs []sdk.Msg

	fromCtx := sh.GetQueryContext(ctx, fromChain.ChainID())

	toAddress, err := toChain.GetAddressString()
	if err != nil {
		logger.ErrorContext(ctx, "error getting address", err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	if doExecuteRelay {
		msgs, err = collectPackets(fromCtx, fromChain, packets, toAddress)
		if err != nil {
			logger.ErrorContext(ctx, "error collecting packets", err)
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
	}

	{ // log
		num := len(msgs)
		if num == 0 {
			logger.InfoContext(ctx, "no packets to relay")
		} else {
			logPacketsRelayed(ctx, logger, num, "Packets")
		}
	}

	return msgs, nil
}

// RelayTimeoutPackets creates MsgTimeout messages for timed out packets.
// The packets parameter contains packets that have timed out and need to be returned to the source chain.
// chain is the source chain where MsgTimeout will be sent.
// counterparty is the destination chain where the packet was supposed to be received.
func (st *NaiveStrategy) RelayTimeoutPackets(ctx context.Context, chain *ProvableChain, counterparty *ProvableChain, packets PacketInfoList, sh SyncHeaders, doExecuteTimeout bool) ([]sdk.Msg, error) {
	ctx, span := tracer.Start(ctx, "NaiveStrategy.RelayTimeoutPackets", WithChannelPairAttributesAndKey("chain", chain, "counterparty", counterparty))
	defer span.End()
	logger := GetChannelPairLoggerRelative(chain, counterparty)
	defer logger.TimeTrackContext(ctx, time.Now(), "RelayTimeoutPackets", "num", len(packets))

	if len(packets) == 0 || !doExecuteTimeout {
		return nil, nil
	}

	var msgs []sdk.Msg

	// Query context is from counterparty chain (where packet receipt/sequence is proven)
	counterpartyCtx := sh.GetQueryContext(ctx, counterparty.ChainID())

	chainAddress, err := chain.GetAddressString()
	if err != nil {
		logger.ErrorContext(ctx, "error getting chain address", err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	msgs, err = collectTimeoutPackets(counterpartyCtx, chain, counterparty, packets, chainAddress)
	if err != nil {
		logger.ErrorContext(ctx, "error collecting timeout packets", err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	if len(msgs) > 0 {
		logPacketsRelayed(ctx, logger, len(msgs), "Timeout Packets")
	} else {
		logger.InfoContext(ctx, "no timeout packets to relay")
	}

	return msgs, nil
}

func (st *NaiveStrategy) UnrelayedAcknowledgements(ctx context.Context, src, dst *ProvableChain, sh SyncHeaders, includeRelayedButUnfinalized bool) (*RelayPackets, error) {
	ctx, span := tracer.Start(ctx, "NaiveStrategy.UnrelayedAcknowledgements", WithChannelPairAttributes(src, dst))
	defer span.End()
	logger := GetChannelPairLogger(src, dst)
	now := time.Now()
	var (
		eg      = new(errgroup.Group)
		srcAcks PacketInfoList
		dstAcks PacketInfoList
	)

	srcCtx, err := getQueryContext(ctx, src, sh, true)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	dstCtx, err := getQueryContext(ctx, dst, sh, true)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	if !st.dstNoAck {
		eg.Go(func() error {
			return retry.Do(func() error {
				var err error
				now := time.Now()
				srcAcks, err = src.QueryUnfinalizedRelayAcknowledgements(srcCtx, dst)
				if err != nil {
					return fmt.Errorf("failed to query unfinalized relay acknowledgements on src chain: %w", err)
				}
				logger.TimeTrackContext(ctx, now, "QueryUnfinalizedRelayAcknowledgements", "queried_chain", "src", "num_packets", len(srcAcks))
				return nil
			}, rtyAtt, rtyDel, rtyErr, retry.Context(ctx), retry.OnRetry(func(n uint, err error) {
				logger.InfoContext(ctx,
					"retrying to query unfinalized ack relays",
					"direction", "src",
					"height", srcCtx.Height().GetRevisionHeight(),
					"try", n+1,
					"try_limit", rtyAttNum,
					"error", err.Error(),
				)
				sh.Updates(ctx, src, dst)
			}))
		})
	}

	if !st.srcNoAck {
		eg.Go(func() error {
			return retry.Do(func() error {
				var err error
				now := time.Now()
				dstAcks, err = dst.QueryUnfinalizedRelayAcknowledgements(dstCtx, src)
				if err != nil {
					return fmt.Errorf("failed to query unfinalized relay acknowledgements on dst chain: %w", err)
				}
				logger.TimeTrackContext(ctx, now, "QueryUnfinalizedRelayAcknowledgements", "queried_chain", "dst", "num_packets", len(dstAcks))
				return nil
			}, rtyAtt, rtyDel, rtyErr, retry.Context(ctx), retry.OnRetry(func(n uint, err error) {
				logger.InfoContext(ctx,
					"retrying to query unfinalized ack relays",
					"direction", "dst",
					"height", dstCtx.Height().GetRevisionHeight(),
					"try", n+1,
					"try_limit", rtyAttNum,
					"error", err.Error(),
				)
				sh.Updates(ctx, src, dst)
			}))
		})
	}

	if err := eg.Wait(); err != nil {
		logger.ErrorContext(ctx, "error querying packet commitments", err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	// If includeRelayedButUnfinalized is true, this function should return packets of which AcknowledgePacket is not finalized yet.
	// In this case, filtering packets by QueryUnreceivedAcknowledgements is not needed because QueryUnfinalizedRelayAcknowledgements
	// has already returned packets that completely match this condition.
	if !includeRelayedButUnfinalized {
		srcCtx, err := getQueryContext(ctx, src, sh, false)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		dstCtx, err := getQueryContext(ctx, dst, sh, false)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}

		if !st.dstNoAck {
			eg.Go(func() error {
				now := time.Now()
				seqs, err := dst.QueryUnreceivedAcknowledgements(dstCtx, srcAcks.ExtractSequenceList())
				if err != nil {
					return fmt.Errorf("failed to query unreceived acknowledgements on dst chain: %w", err)
				}
				logger.TimeTrackContext(ctx, now, "QueryUnreceivedAcknowledgements", "queried_chain", "dst", "num_seqs", len(seqs))
				srcAcks = srcAcks.Filter(seqs)
				return nil
			})
		}

		if !st.srcNoAck {
			eg.Go(func() error {
				now := time.Now()
				seqs, err := src.QueryUnreceivedAcknowledgements(srcCtx, dstAcks.ExtractSequenceList())
				if err != nil {
					return fmt.Errorf("failed to query unreceived acknowledgements on src chain: %w", err)
				}
				logger.TimeTrackContext(ctx, now, "QueryUnreceivedAcknowledgements", "queried_chain", "src", "num_seqs", len(seqs))
				dstAcks = dstAcks.Filter(seqs)
				return nil
			})
		}

		if err := eg.Wait(); err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
	}

	defer logger.TimeTrackContext(ctx, now, "UnrelayedAcknowledgements", "num_src", len(srcAcks), "num_dst", len(dstAcks))

	return &RelayPackets{
		Src: srcAcks,
		Dst: dstAcks,
	}, nil
}

func collectPackets(ctx QueryContext, chain *ProvableChain, packets PacketInfoList, signer string) ([]sdk.Msg, error) {
	logger := GetChannelLogger(chain)
	var msgs []sdk.Msg
	for _, p := range packets {
		commitment := chantypes.CommitPacket(chain.Codec(), &p.Packet)
		path := host.PacketCommitmentPath(p.SourcePort, p.SourceChannel, p.Sequence)
		proof, proofHeight, err := chain.ProveState(ctx, path, commitment)
		if err != nil {
			logger.ErrorContext(ctx.Context(), "failed to ProveState", err,
				"height", ctx.Height(),
				"path", path,
				"commitment", commitment,
			)
			return nil, err
		}
		msg := chantypes.NewMsgRecvPacket(p.Packet, proof, proofHeight, signer)
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// collectTimeoutPackets creates MsgTimeout messages for timed out packets.
// ctx is the query context for the counterparty chain (where packet receipt/sequence is proven).
// chain is the source chain where MsgTimeout will be sent.
// counterparty is the destination chain where the packet was supposed to be received.
// packets are the timed out packets that originated from chain.
func collectTimeoutPackets(ctx QueryContext, chain *ProvableChain, counterparty *ProvableChain, packets PacketInfoList, signer string) ([]sdk.Msg, error) {
	logger := GetChannelLogger(chain)
	var msgs []sdk.Msg

	for _, p := range packets {
		var path string
		var commitment []byte
		var nextSequenceRecv uint64

		if chain.Path().GetOrder() == chantypes.ORDERED {
			// For ordered channels, prove the next sequence receive
			path = host.NextSequenceRecvPath(p.DestinationPort, p.DestinationChannel)
			commitment = make([]byte, 8)
			binary.BigEndian.PutUint64(commitment, p.Sequence)
			nextSequenceRecv = p.Sequence
		} else {
			// For unordered channels, prove absence of packet receipt
			path = host.PacketReceiptPath(p.DestinationPort, p.DestinationChannel, p.Sequence)
			commitment = []byte{} // Empty commitment represents absence
			nextSequenceRecv = 1  // Not used for unordered but ibc-go expects non-zero
		}

		proof, proofHeight, err := counterparty.ProveState(ctx, path, commitment)
		if err != nil {
			logger.ErrorContext(ctx.Context(), "failed to ProveState for timeout", err,
				"height", ctx.Height(),
				"path", path,
				"commitment", commitment,
			)
			return nil, err
		}

		msg := chantypes.NewMsgTimeout(p.Packet, nextSequenceRecv, proof, proofHeight, signer)
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func logPacketsRelayed(ctx context.Context, logger *log.RelayLogger, num int, obj string) {
	logger.InfoContext(ctx,
		fmt.Sprintf("★ %s are scheduled for relay", obj),
		"count", num)
}

func (st *NaiveStrategy) RelayAcknowledgements(ctx context.Context, src, dst *ProvableChain, isSrcToDst bool, packets PacketInfoList, sh SyncHeaders, doExecuteAck bool) ([]sdk.Msg, error) {
	var (
		fromChain, toChain *ProvableChain
		noAck              bool
	)
	if isSrcToDst {
		fromChain = src
		toChain = dst
		noAck = st.srcNoAck
	} else {
		fromChain = dst
		toChain = src
		noAck = st.dstNoAck
	}

	ctx, span := tracer.Start(ctx, "NaiveStrategy.RelayAcknowledgements", WithChannelPairAttributesAndKey("from", fromChain, "to", toChain))
	defer span.End()
	logger := GetChannelPairLoggerRelative(fromChain, toChain)
	defer logger.TimeTrackContext(ctx, time.Now(), "RelayAcknowledgements", "num", len(packets))

	var msgs []sdk.Msg

	fromCtx := sh.GetQueryContext(ctx, fromChain.ChainID())

	toAddress, err := toChain.GetAddressString()
	if err != nil {
		logger.ErrorContext(ctx, "error getting address", err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	if !noAck && doExecuteAck {
		msgs, err = collectAcks(fromCtx, fromChain, packets, toAddress)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
	}

	{ // log
		num := len(msgs)
		if num == 0 {
			logger.InfoContext(ctx, "no acknowledgements to relay")
		} else {
			logPacketsRelayed(ctx, logger, num, "Acknowledgements")
		}
	}

	return msgs, nil
}

func collectAcks(ctx QueryContext, chain *ProvableChain, packets PacketInfoList, signer string) ([]sdk.Msg, error) {
	logger := GetChannelLogger(chain)
	var msgs []sdk.Msg

	for _, p := range packets {
		commitment := chantypes.CommitAcknowledgement(p.Acknowledgement)
		path := host.PacketAcknowledgementPath(p.DestinationPort, p.DestinationChannel, p.Sequence)
		proof, proofHeight, err := chain.ProveState(ctx, path, commitment)
		if err != nil {
			logger.ErrorContext(ctx.Context(), "failed to ProveState", err,
				"height", ctx.Height(),
				"path", path,
				"commitment", commitment,
			)
			return nil, err
		}
		msg := chantypes.NewMsgAcknowledgement(p.Packet, p.Acknowledgement, proof, proofHeight, signer)
		msgs = append(msgs, msg)
	}

	return msgs, nil
}

func (st *NaiveStrategy) UpdateClients(ctx context.Context, src, dst *ProvableChain, isSrcToDst bool, doExecuteRelay, doExecuteAck, doExecuteTimeout bool, sh SyncHeaders, doRefresh bool) ([]sdk.Msg, error) {
	var (
		fromChain, toChain *ProvableChain
		noAck              bool
	)
	if isSrcToDst {
		fromChain = src
		toChain = dst
		noAck = st.srcNoAck
	} else {
		fromChain = dst
		toChain = src
		noAck = st.dstNoAck
	}

	ctx, span := tracer.Start(ctx, "NaiveStrategy.UpdateClients", WithChannelPairAttributesAndKey("from", fromChain, "to", toChain))
	defer span.End()
	logger := GetChannelPairLoggerRelative(fromChain, toChain)

	var msgs []sdk.Msg

	needsUpdate := doExecuteRelay || (doExecuteAck && !noAck) || doExecuteTimeout

	// check if LC refresh is needed
	if !needsUpdate && doRefresh {
		var err error
		needsUpdate, err = fromChain.CheckRefreshRequired(ctx, toChain)
		if err != nil {
			err = fmt.Errorf("failed to check if the LC on the toChain chain needs to be refreshed: %v", err)
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
	}

	if needsUpdate {
		toAddress, err := toChain.GetAddressString()
		if err != nil {
			err = fmt.Errorf("failed to get relayer address on toChain chain: %v", err)
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		hs, err := sh.SetupHeadersForUpdate(ctx, fromChain, toChain)
		if err != nil {
			err = fmt.Errorf("failed to set up headers for updating client on toChain chain: %v", err)
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		if len(hs) > 0 {
			msgs = toChain.Path().UpdateClients(hs, toAddress)
		}
	}

	if len(msgs) > 0 {
		logger.InfoContext(ctx, "client on toChain chain was scheduled for update", "num_sent_msgs", len(msgs))
	}

	return msgs, nil
}

func (st *NaiveStrategy) Send(ctx context.Context, src, dst Chain, msgs *RelayMsgs) {
	ctx, span := tracer.Start(ctx, "NaiveStrategy.Send", WithChannelPairAttributes(src, dst))
	defer span.End()
	logger := GetChannelPairLogger(src, dst)

	msgs.MaxTxSize = st.MaxTxSize
	msgs.MaxMsgLength = st.MaxMsgLength
	msgs.Send(ctx, src, dst)

	logger.InfoContext(ctx, "msgs relayed",
		slog.Group("src", "msg_count", len(msgs.Src)),
		slog.Group("dst", "msg_count", len(msgs.Dst)),
	)
}

func (st *naiveStrategyMetrics) updateBacklogMetrics(ctx context.Context, src, dst ChainInfo, newSrcBacklog, newDstBacklog PacketInfoList) error {
	srcAttrs := []attribute.KeyValue{
		semconv.ChainIDKey.String(src.ChainID()),
		semconv.DirectionKey.String("src"),
	}
	dstAttrs := []attribute.KeyValue{
		semconv.ChainIDKey.String(dst.ChainID()),
		semconv.DirectionKey.String("dst"),
	}

	telemetry.BacklogSizeGauge.Set(int64(len(newSrcBacklog)), srcAttrs...)
	telemetry.BacklogSizeGauge.Set(int64(len(newDstBacklog)), dstAttrs...)

	if len(newSrcBacklog) > 0 {
		oldestHeight := newSrcBacklog[0].EventHeight
		oldestTimestamp, err := src.Timestamp(ctx, oldestHeight)
		if err != nil {
			return fmt.Errorf("failed to get the timestamp of block[%d] on the src chain: %v", oldestHeight, err)
		}
		telemetry.BacklogOldestTimestampGauge.Set(oldestTimestamp.UnixNano(), srcAttrs...)
	} else {
		telemetry.BacklogOldestTimestampGauge.Set(0, srcAttrs...)
	}
	if len(newDstBacklog) > 0 {
		oldestHeight := newDstBacklog[0].EventHeight
		oldestTimestamp, err := dst.Timestamp(ctx, oldestHeight)
		if err != nil {
			return fmt.Errorf("failed to get the timestamp of block[%d] on the dst chain: %v", oldestHeight, err)
		}
		telemetry.BacklogOldestTimestampGauge.Set(oldestTimestamp.UnixNano(), dstAttrs...)
	} else {
		telemetry.BacklogOldestTimestampGauge.Set(0, dstAttrs...)
	}

	srcReceivedPackets := st.srcBacklog.Subtract(newSrcBacklog.ExtractSequenceList())
	telemetry.ReceivePacketsFinalizedCounter.Add(ctx, int64(len(srcReceivedPackets)), api.WithAttributes(srcAttrs...))
	st.srcBacklog = newSrcBacklog

	dstReceivedPackets := st.dstBacklog.Subtract(newDstBacklog.ExtractSequenceList())
	telemetry.ReceivePacketsFinalizedCounter.Add(ctx, int64(len(dstReceivedPackets)), api.WithAttributes(dstAttrs...))
	st.dstBacklog = newDstBacklog

	return nil
}
