package commands

import "testing"

func TestPingCommand_Definition(t *testing.T) {
	cmd := &PingCommand{}
	def := cmd.Definition()
	if def.Name != "ping" {
		t.Fatalf("expected command name 'ping', got %q", def.Name)
	}
	if def.Description == "" {
		t.Fatal("expected non-empty description")
	}
}

func TestPingCommand_ResponseText(t *testing.T) {
	cmd := &PingCommand{}
	if got := cmd.responseText(); got != "🏓 Pong!" {
		t.Fatalf("expected '🏓 Pong!', got %q", got)
	}
}
