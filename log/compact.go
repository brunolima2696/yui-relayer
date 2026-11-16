package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"google.golang.org/grpc/grpclog"
)

// CompactHandler keeps workflow events and warnings/errors, hiding polling and payload dumps.
// Child handlers share the writer lock, but never mutate each other's attributes.
type CompactHandler struct {
	writer io.Writer
	mu     *sync.Mutex
	attrs  []slog.Attr
	group  string
}

func NewCompactHandler(writer io.Writer) *CompactHandler {
	return &CompactHandler{writer: writer, mu: &sync.Mutex{}}
}
func (*CompactHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func collectCompactAttrs(attrs []slog.Attr, prefix string, out *[]slog.Attr) {
	for _, attr := range attrs {
		attr.Value = attr.Value.Resolve()
		if attr.Value.Kind() == slog.KindGroup {
			p := prefix
			if attr.Key != "" {
				p += attr.Key + "."
			}
			collectCompactAttrs(attr.Value.Group(), p, out)
		} else if attr.Key != "" {
			attr.Key = prefix + attr.Key
			*out = append(*out, attr)
		}
	}
}

func (h *CompactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copy := *h
	copy.attrs = append([]slog.Attr(nil), h.attrs...)
	collectCompactAttrs(attrs, h.group, &copy.attrs)
	return &copy
}
func (h *CompactHandler) WithGroup(name string) slog.Handler {
	copy := *h
	if name != "" {
		copy.group += name + "."
	}
	return &copy
}

func oneLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}

func (h *CompactHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := append([]slog.Attr(nil), h.attrs...)
	var recordAttrs []slog.Attr
	record.Attrs(func(a slog.Attr) bool { recordAttrs = append(recordAttrs, a); return true })
	collectCompactAttrs(recordAttrs, h.group, &attrs)
	event := ""
	for _, attr := range attrs {
		if attr.Key == "relay_event" {
			event = attr.Value.String()
		}
	}
	if record.Level < slog.LevelWarn && event == "" {
		return nil
	}
	level := record.Level.String()
	if event == "health" {
		level = "HEALTH"
	}
	line := fmt.Sprintf("%s %-6s %s", record.Time.Format("15:04:05"), level, oneLine(record.Message, 180))
	for _, attr := range attrs {
		key := attr.Key
		leaf := key[strings.LastIndex(key, ".")+1:]
		switch leaf {
		case "path", "from", "to", "chain", "chain_id", "chain-id", "client_id", "connection_id", "port_id", "channel_id", "port", "channel", "src_channel", "dst_channel", "dst_port", "sequence", "tx", "evm_hash", "height", "action", "payment_id", "id_pedido", "result", "reason", "error", "state", "pending_packets", "pending_acks", "pending_confirmations", "last_cycle", "src_height", "dst_height", "channels", "try", "try_limit":
			value := oneLine(attr.Value.String(), 240)
			if value != "" {
				line += " | " + key + "=" + value
			}
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := fmt.Fprintln(h.writer, line)
	return err
}

// Send gRPC diagnostics through the same logger, instead of Fabric's verbose stderr trace.
type grpcWriter struct{ level slog.Level }

func (w grpcWriter) Write(bz []byte) (int, error) {
	if logger := GetLogger(); logger != nil {
		logger.Log(context.Background(), w.level, "gRPC", "error", strings.TrimSpace(string(bz)))
	}
	return len(bz), nil
}
func configureGRPCLogging() {
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(
		grpcWriter{slog.LevelDebug}, grpcWriter{slog.LevelWarn}, grpcWriter{slog.LevelError}))
}
