package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	chantypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
	ibctm "github.com/cosmos/ibc-go/v8/modules/light-clients/07-tendermint"
	"github.com/hyperledger-labs/yui-relayer/log"
)

type relayLogOptions struct {
	path           string
	healthInterval time.Duration
}
type relayLogOptionsKey struct{}
type relayMonitorKey struct{}

// WithRelayLogOptions configures service observability, without changing chain configuration.
func WithRelayLogOptions(ctx context.Context, path string, interval time.Duration) context.Context {
	return context.WithValue(ctx, relayLogOptionsKey{}, relayLogOptions{path, interval})
}

type relaySubmission struct {
	chain        Chain
	id           MsgID
	packet       chantypes.Packet
	kind         string
	ack          []byte
	sent, warned time.Time
}

type relayMonitor struct {
	mu                   sync.Mutex
	logger               *log.RelayLogger
	src, dst             string
	interval, freshness  time.Duration
	lastCycle, lastCheck time.Time
	lastCheckAttempt     time.Time
	healthError          error
	started              time.Time
	lastError            string
	state                string
	srcHeight, dstHeight string
	packets, acks        int
	seen                 map[string]time.Time
	pending              []*relaySubmission
	confirming           int
}

func newRelayMonitor(ctx context.Context, src, dst string, relayInterval time.Duration) *relayMonitor {
	opts, ok := ctx.Value(relayLogOptionsKey{}).(relayLogOptions)
	if !ok {
		opts = relayLogOptions{src + "-" + dst, 30 * time.Second}
	}
	freshness := 2 * opts.healthInterval
	if limit := 2*relayInterval + 10*time.Second; limit > freshness {
		freshness = limit
	}
	return &relayMonitor{logger: &log.RelayLogger{Logger: log.GetLogger().With("path", opts.path)},
		src: src, dst: dst, interval: opts.healthInterval, freshness: freshness, state: "iniciando", started: time.Now(), seen: map[string]time.Time{}}
}

func (m *relayMonitor) once(origin string, p chantypes.Packet, stage string) bool {
	key := fmt.Sprintf("%s/%s/%s/%d/%s", origin, p.SourcePort, p.SourceChannel, p.Sequence, stage)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.seen[key]; exists {
		m.seen[key] = now
		return false
	}
	if len(m.seen) >= 8192 {
		oldestKey := ""
		oldest := now
		for k, at := range m.seen {
			if oldestKey == "" || at.Before(oldest) {
				oldestKey, oldest = k, at
			}
		}
		delete(m.seen, oldestKey)
	}
	m.seen[key] = now
	return true
}

func packetLogAttrs(p chantypes.Packet) []any {
	attrs := []any{"port", p.SourcePort, "src_channel", p.SourceChannel, "dst_channel", p.DestinationChannel, "sequence", p.Sequence}
	if p.DestinationPort != p.SourcePort {
		attrs = append(attrs, "dst_port", p.DestinationPort)
	}
	// Only extract correlation fields; never print the payload or proof in compact mode.
	if len(p.Data) <= 12288 {
		var data struct {
			Action    string `json:"action"`
			PaymentID string `json:"payment_id"`
			OrderID   string `json:"id_pedido"`
		}
		if json.Unmarshal(p.Data, &data) == nil {
			if data.Action != "" {
				attrs = append(attrs, "action", data.Action)
			}
			if data.OrderID != "" {
				attrs = append(attrs, "id_pedido", data.OrderID)
			} else if data.PaymentID != "" {
				attrs = append(attrs, "payment_id", data.PaymentID)
			}
		}
	}
	return attrs
}

func (m *relayMonitor) event(ctx context.Context, event, message, from, to string, p chantypes.Packet, extra ...any) {
	attrs := append([]any{"relay_event", event, "from", from, "to", to}, packetLogAttrs(p)...)
	attrs = append(attrs, extra...)
	m.logger.InfoContext(ctx, message, attrs...)
}

func ackStatus(bz []byte) (result, reason string) {
	var ack chantypes.Acknowledgement
	if err := chantypes.SubModuleCdc.UnmarshalJSON(bz, &ack); err != nil {
		return "desconhecido", "formato de ACK nao reconhecido"
	}
	if ack.ValidateBasic() != nil {
		return "desconhecido", "ACK invalido"
	}
	if ack.Success() {
		var execution struct {
			Status  string `json:"status"`
			OrderID string `json:"id_pedido"`
			Reason  string `json:"reason"`
		}
		if len(ack.GetResult()) <= 4096 && json.Unmarshal(ack.GetResult(), &execution) == nil && execution.OrderID != "" {
			switch execution.Status {
			case "success":
				return "sucesso", ""
			case "rejected":
				return "rejeitado", execution.Reason
			case "reverted":
				return "revertido", execution.Reason
			default:
				return "desconhecido", "status de execucao nao reconhecido"
			}
		}
		return "sucesso", ""
	}
	return "erro", ack.GetError()
}

func ackExecutionAttrs(bz []byte) []any {
	var ack chantypes.Acknowledgement
	if chantypes.SubModuleCdc.UnmarshalJSON(bz, &ack) != nil || !ack.Success() || len(ack.GetResult()) > 4096 {
		return nil
	}
	var result struct {
		Hash    string `json:"hash"`
		Status  string `json:"status"`
		OrderID string `json:"id_pedido"`
	}
	if json.Unmarshal(ack.GetResult(), &result) != nil || result.OrderID == "" || len(result.Hash) != 66 || result.Hash[:2] != "0x" {
		return nil
	}
	if result.Status != "success" && result.Status != "rejected" && result.Status != "reverted" {
		return nil
	}
	if _, err := hex.DecodeString(result.Hash[2:]); err != nil {
		return nil
	}
	return []any{"evm_hash", result.Hash}
}

func (m *relayMonitor) observeAck(ctx context.Context, origin, destination string, p *PacketInfo) {
	if m.once(origin, p.Packet, "recv_confirmed") {
		m.event(ctx, "recv_confirmed", "RecvPacket confirmado no destino", origin, destination, p.Packet)
	}
	if !m.once(origin, p.Packet, "ack_available") {
		return
	}
	result, reason := ackStatus(p.Acknowledgement)
	if result == "sucesso" {
		extra := append([]any{"result", result}, ackExecutionAttrs(p.Acknowledgement)...)
		m.event(ctx, "ack_available", "ACK disponivel no destino", destination, origin, p.Packet, extra...)
	} else {
		attrs := append([]any{"relay_event", "ack_available", "from", destination, "to", origin, "result", result, "reason", reason}, packetLogAttrs(p.Packet)...)
		attrs = append(attrs, ackExecutionAttrs(p.Acknowledgement)...)
		m.logger.WarnContext(ctx, "ACK de erro disponivel no destino", attrs...)
	}
}

func (m *relayMonitor) observe(ctx context.Context, packets, acks *RelayPackets) {
	m.mu.Lock()
	m.packets = len(packets.Src) + len(packets.Dst) + len(packets.SrcTimeout) + len(packets.DstTimeout)
	m.acks = len(acks.Src) + len(acks.Dst)
	m.mu.Unlock()
	for _, direction := range []struct {
		from, to string
		packets  PacketInfoList
	}{
		{m.src, m.dst, packets.Src}, {m.dst, m.src, packets.Dst},
	} {
		for _, p := range direction.packets {
			if m.once(direction.from, p.Packet, "detected") {
				m.event(ctx, "packet_detected", "Pacote detectado", direction.from, direction.to, p.Packet)
			}
		}
	}
	for _, direction := range []struct {
		origin, dest string
		acks         PacketInfoList
	}{
		{m.dst, m.src, acks.Src}, {m.src, m.dst, acks.Dst},
	} {
		for _, p := range direction.acks {
			m.observeAck(ctx, direction.origin, direction.dest, p)
		}
	}
}

func txLabel(id MsgID) string {
	v := reflect.ValueOf(id)
	if v.Kind() == reflect.Ptr && !v.IsNil() {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	for _, name := range []string{"TxHash", "TxHashHex", "TxId", "TxID", "Hash"} {
		f := v.FieldByName(name)
		if !f.IsValid() {
			continue
		}
		if f.Kind() == reflect.String {
			return f.String()
		}
		if (f.Kind() == reflect.Array || f.Kind() == reflect.Slice) && f.Type().Elem().Kind() == reflect.Uint8 {
			bz := make([]byte, f.Len())
			for i := range bz {
				bz[i] = byte(f.Index(i).Uint())
			}
			return "0x" + hex.EncodeToString(bz)
		}
	}
	return ""
}

func (m *relayMonitor) sent(ctx context.Context, chain Chain, msgs []sdk.Msg, ids []MsgID) {
	for i, msg := range msgs {
		var p chantypes.Packet
		var ack []byte
		kind := ""
		switch msg := msg.(type) {
		case *chantypes.MsgRecvPacket:
			p, kind = msg.Packet, "recv"
		case *chantypes.MsgAcknowledgement:
			p, kind = msg.Packet, "ack"
			ack = msg.Acknowledgement
		case *chantypes.MsgTimeout:
			p, kind = msg.Packet, "timeout"
		case *chantypes.MsgTimeoutOnClose:
			p, kind = msg.Packet, "timeout"
		}
		if kind == "" || i >= len(ids) || ids[i] == nil {
			continue
		}
		counterparty := m.src
		if chain.ChainID() == m.src {
			counterparty = m.dst
		}
		label := map[string]string{"recv": "RecvPacket enviado", "ack": "ACK enviado para a origem", "timeout": "Timeout enviado para a origem"}[kind]
		m.event(ctx, kind+"_submitted", label, counterparty, chain.ChainID(), p, "tx", txLabel(ids[i]))
		m.mu.Lock()
		if len(m.pending)+m.confirming < 4096 {
			m.pending = append(m.pending, &relaySubmission{chain: chain, id: ids[i], packet: p, kind: kind, ack: ack, sent: time.Now()})
		} else {
			m.logger.WarnContext(ctx, "Limite de confirmacoes monitoradas atingido", "chain", chain.ChainID())
		}
		m.mu.Unlock()
	}
}

// Receipt queries are bounded per relay round; observability must not stall packet delivery.
func (m *relayMonitor) confirm(ctx context.Context) {
	m.mu.Lock()
	pending := append([]*relaySubmission(nil), m.pending...)
	m.pending = nil
	m.confirming = len(pending)
	m.mu.Unlock()
	budget, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var remaining []*relaySubmission
	for _, submission := range pending {
		if budget.Err() != nil {
			remaining = append(remaining, submission)
			continue
		}
		result, err := submission.chain.GetMsgResult(budget, submission.id)
		if err != nil || result == nil {
			if ctx.Err() == nil && time.Since(submission.sent) >= 30*time.Second && (submission.warned.IsZero() || time.Since(submission.warned) >= time.Minute) {
				m.logger.WarnContext(ctx, "Confirmacao de transacao ainda pendente", "chain", submission.chain.ChainID(), "tx", txLabel(submission.id), "sequence", submission.packet.Sequence)
				submission.warned = time.Now()
			}
			remaining = append(remaining, submission)
			continue
		}
		ok, reason := result.Status()
		if !ok {
			m.logger.ErrorContext(ctx, "Transacao IBC rejeitada", fmt.Errorf("%s", reason), "chain", submission.chain.ChainID(), "tx", txLabel(submission.id), "sequence", submission.packet.Sequence)
			continue
		}
		counterparty := m.src
		if submission.chain.ChainID() == m.src {
			counterparty = m.dst
		}
		origin := submission.chain.ChainID()
		if submission.kind == "recv" {
			origin = counterparty
		}
		if m.once(origin, submission.packet, submission.kind+"_confirmed") {
			label := map[string]string{"recv": "RecvPacket confirmado no destino", "ack": "ACK confirmado na origem", "timeout": "Timeout confirmado na origem"}[submission.kind]
			extra := []any{"tx", txLabel(submission.id), "height", result.BlockHeight().String()}
			if submission.kind == "ack" {
				status, reason := ackStatus(submission.ack)
				extra = append(extra, "result", status)
				extra = append(extra, ackExecutionAttrs(submission.ack)...)
				if reason != "" {
					extra = append(extra, "reason", reason)
				}
			}
			m.event(ctx, submission.kind+"_confirmed", label, counterparty, submission.chain.ChainID(), submission.packet, extra...)
		}
		if submission.kind == "recv" {
			for _, event := range result.Events() {
				if ack, ok := event.(*EventWriteAcknowledgement); ok && ack.Sequence == submission.packet.Sequence {
					m.observeAck(ctx, origin, submission.chain.ChainID(), &PacketInfo{Packet: submission.packet, Acknowledgement: ack.Acknowledgement})
				}
			}
		}
	}
	m.mu.Lock()
	m.pending = append(remaining, m.pending...)
	m.confirming = 0
	m.mu.Unlock()
}

func logRelaySubmission(ctx context.Context, chain Chain, msgs []sdk.Msg, ids []MsgID) {
	if m, ok := ctx.Value(relayMonitorKey{}).(*relayMonitor); ok {
		m.sent(ctx, chain, msgs, ids)
	}
}

func (m *relayMonitor) check(ctx context.Context, src, dst *ProvableChain, sh SyncHeaders) (checkError error) {
	if m.interval == 0 {
		return nil
	}
	m.mu.Lock()
	due := m.lastCheckAttempt.IsZero() || time.Since(m.lastCheckAttempt) >= m.interval
	previousError := m.healthError
	m.mu.Unlock()
	if !due {
		return previousError
	}
	defer func() {
		m.mu.Lock()
		m.lastCheckAttempt, m.healthError = time.Now(), checkError
		m.mu.Unlock()
	}()
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, c := range []*ProvableChain{src, dst} {
		q := sh.GetQueryContext(bounded, c.ChainID())
		channel, err := c.QueryChannel(q)
		if err != nil {
			return fmt.Errorf("healthcheck do channel em %s: %w", c.ChainID(), err)
		}
		if channel == nil || channel.Channel == nil || channel.Channel.State != chantypes.OPEN {
			return fmt.Errorf("channel nao esta OPEN em %s", c.ChainID())
		}
		response, err := c.QueryClientState(q)
		if err != nil {
			return fmt.Errorf("healthcheck do client em %s: %w", c.ChainID(), err)
		}
		if response == nil || response.ClientState == nil {
			return fmt.Errorf("client ausente em %s", c.ChainID())
		}
		var state ibcexported.ClientState
		if err := c.Codec().UnpackAny(response.ClientState, &state); err != nil {
			return err
		}
		if err := state.Validate(); err != nil {
			return fmt.Errorf("client invalido em %s: %w", c.ChainID(), err)
		}
		if tm, ok := state.(*ibctm.ClientState); ok {
			if !tm.FrozenHeight.IsZero() {
				return fmt.Errorf("client Tendermint congelado em %s", c.ChainID())
			}
			consensus, err := c.QueryClientConsensusState(q, tm.LatestHeight)
			if err != nil {
				return err
			}
			var cs ibcexported.ConsensusState
			if consensus == nil || consensus.ConsensusState == nil {
				return fmt.Errorf("consensus state ausente em %s", c.ChainID())
			}
			if err := c.Codec().UnpackAny(consensus.ConsensusState, &cs); err != nil {
				return err
			}
			blockTime, err := c.Timestamp(bounded, q.Height())
			if err != nil {
				return err
			}
			// Fabric's logical height may stay unchanged while idle; its next transaction
			// will execute at the current time, not the old sequence timestamp.
			if now := time.Now(); now.After(blockTime) {
				blockTime = now
			}
			if !blockTime.Before(time.Unix(0, int64(cs.GetTimestamp())).Add(tm.TrustingPeriod)) {
				return fmt.Errorf("client Tendermint expirado em %s", c.ChainID())
			}
		}
	}
	m.mu.Lock()
	m.lastCheck = time.Now()
	m.mu.Unlock()
	return nil
}

func (m *relayMonitor) cycle(ctx context.Context, err error, sh SyncHeaders) {
	m.mu.Lock()
	previous := m.state
	if err != nil {
		m.state, m.lastError = "degradado", err.Error()
	} else {
		m.state, m.lastError, m.lastCycle = "ativo", "", time.Now()
		m.srcHeight = sh.GetLatestFinalizedHeader(m.src).GetHeight().String()
		m.dstHeight = sh.GetLatestFinalizedHeader(m.dst).GetHeight().String()
	}
	changed := previous != m.state
	m.mu.Unlock()
	if changed && m.interval > 0 {
		m.health(ctx)
	}
}

func (m *relayMonitor) health(ctx context.Context) {
	m.mu.Lock()
	state, reason := m.state, m.lastError
	lastCycle := "nenhum"
	if !m.lastCycle.IsZero() {
		lastCycle = time.Since(m.lastCycle).Round(time.Second).String()
	}
	if state == "ativo" && time.Since(m.lastCycle) > m.freshness {
		state, reason = "degradado", "ciclo de relay sem progresso recente"
	}
	if state == "iniciando" && time.Since(m.started) > m.freshness {
		state, reason = "degradado", "nenhum ciclo de relay concluido"
	}
	channels := "nao verificados"
	if !m.lastCheck.IsZero() {
		channels = "OPEN (ultima verificacao)"
	}
	attrs := []any{"relay_event", "health", "state", state, "last_cycle", lastCycle, "channels", channels,
		"src_height", m.srcHeight, "dst_height", m.dstHeight, "pending_packets", m.packets, "pending_acks", m.acks, "pending_confirmations", len(m.pending) + m.confirming}
	if reason != "" {
		attrs = append(attrs, "reason", reason)
	}
	m.mu.Unlock()
	if state == "degradado" {
		m.logger.WarnContext(ctx, "Relay degradado", attrs...)
	} else if state == "iniciando" {
		m.logger.InfoContext(ctx, "Relay iniciando", attrs...)
	} else {
		m.logger.InfoContext(ctx, "Relay ativo", attrs...)
	}
}

func (m *relayMonitor) startHealth(ctx context.Context) func() {
	if m.interval <= 0 {
		return func() {}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				m.health(ctx)
			}
		}
	}()
	return func() { close(stop); <-done }
}
