package log

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestCompactHidesPollingAndPayloads(t *testing.T) {
	var out bytes.Buffer
	l := &RelayLogger{Logger: slog.New(NewCompactHandler(&out))}
	l.Info("no packets to relay", "msgs", "private payload")
	l.Debug("grpc dial trace")
	l.Info("Pacote detectado", "relay_event", "packet_detected", "sequence", 42, "msgs", "private payload", "stack", "private stack")
	l.Error("Envio falhou", fmt.Errorf("RPC indisponivel\nretry"))
	text := out.String()
	for _, hidden := range []string{"no packets", "private payload", "private stack", "grpc dial", "source="} {
		if strings.Contains(text, hidden) {
			t.Fatalf("unexpected diagnostic %q: %s", hidden, text)
		}
	}
	if !strings.Contains(text, "sequence=42") || !strings.Contains(text, "RPC indisponivel retry") {
		t.Fatal(text)
	}
	if strings.Count(text, "\n") != 2 {
		t.Fatal("each event must occupy one line")
	}
}

func TestCompactGroupsAreImmutableAndHealthIsVisible(t *testing.T) {
	var out bytes.Buffer
	base := slog.New(NewCompactHandler(&out)).With("path", "a-b")
	child := base.WithGroup("src").With("chain_id", "a")
	child.Warn("Falha", "error", "rpc")
	base.InfoContext(context.Background(), "Relay ativo", "relay_event", "health", "state", "ativo")
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if !strings.Contains(lines[0], "src.chain_id=a") || strings.Contains(lines[1], "src.") || !strings.Contains(lines[1], "HEALTH") {
		t.Fatal(out.String())
	}
}
