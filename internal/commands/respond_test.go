package commands

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// failingTransport fails every request the way a dead network does: net/http
// wraps the returned error in *url.Error together with the full request URL,
// which for an interaction reply is /interactions/<id>/<token>/callback.
type failingTransport struct{ err error }

func (t failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

// failingSession builds a session whose every REST call fails with err.
func failingSession(t *testing.T, err error) *discordgo.Session {
	t.Helper()
	s, newErr := discordgo.New("Bot test-bot-token")
	if newErr != nil {
		t.Fatalf("discordgo.New: %v", newErr)
	}
	s.Client = &http.Client{Transport: failingTransport{err: err}}
	return s
}

func tokenInteraction() *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID:    "1",
		Token: "TEST_TOKEN",
		Type:  discordgo.InteractionApplicationCommand,
	}}
}

// TestRespond_ReturnedErrorCarriesNoToken covers the path the evaluator found:
// cmd/bot logs whatever Handle returns, so the error that leaves respond —
// not just the ones this package logs itself — has to be token-free. Both
// hiding places are exercised: the URL net/http attaches, and the cause it
// wraps.
func TestRespond_ReturnedErrorCarriesNoToken(t *testing.T) {
	tests := []struct {
		name  string
		cause error
	}{
		{name: "plain transport failure", cause: errors.New("connection reset")},
		{name: "cause quoting the token", cause: errors.New("request token TEST_TOKEN")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := failingSession(t, tc.cause)
			err := respond(s, tokenInteraction().Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{Content: "x"},
			})
			if err == nil {
				t.Fatal("respond returned nil, want the transport failure")
			}
			if strings.Contains(err.Error(), "TEST_TOKEN") {
				t.Fatalf("respond leaked the token in its returned error: %q", err)
			}
		})
	}
}

// TestHandle_ReturnedErrorLoggedByCallerCarriesNoToken reproduces the caller
// exactly: run a handler against a dead transport and put its return value
// through slog the way cmd/bot's interaction handler does.
func TestHandle_ReturnedErrorLoggedByCallerCarriesNoToken(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	s := failingSession(t, errors.New("connection reset"))
	err := (&PingCommand{}).Handle(s, tokenInteraction())
	if err == nil {
		t.Fatal("Handle returned nil, want the transport failure")
	}
	logger.Error("command handler returned an error", "name", "ping", "error", err)

	if strings.Contains(buf.String(), "TEST_TOKEN") {
		t.Fatalf("the caller's log line kept the token: %q", buf.String())
	}
}

// TestNoDirectInteractionRespond pins the rule to the whole package rather
// than to the commands that happened to exist when it was written: a handler
// that calls s.InteractionRespond itself returns the raw error and puts the
// token back into cmd/bot's log. The animation path in slot.go talks to a
// slotResponder interface and logs through redactInteractionError, so it is
// not matched by this pattern.
func TestNoDirectInteractionRespond(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, f := range files {
		// casino_shared.go is where respond() itself makes the call.
		if strings.HasSuffix(f, "_test.go") || f == "casino_shared.go" {
			continue
		}
		src, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("read %s: %v", f, readErr)
		}
		if strings.Contains(string(src), "s.InteractionRespond(") {
			t.Errorf("%s calls s.InteractionRespond directly; use respond() so the returned error is redacted", f)
		}
	}
}
