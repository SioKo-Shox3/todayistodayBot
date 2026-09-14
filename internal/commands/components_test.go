package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

func TestBuildCustomID_MatchesTheDocumentedShape(t *testing.T) {
	got := BuildCustomID("highlow", "8f3a", "high")
	if want := "casino:highlow:8f3a:high"; got != want {
		t.Fatalf("BuildCustomID = %q, want %q", got, want)
	}
}

func TestParseCustomID_RoundTrip(t *testing.T) {
	tests := []struct {
		game, sessionID, action string
	}{
		{"highlow", "8f3a", "high"},
		{"highlow", "0123456789abcdef0123456789abcdef", "cashout"},
		{"blackjack", "a1", "double"},
	}
	for _, tc := range tests {
		t.Run(tc.game+"/"+tc.action, func(t *testing.T) {
			game, sessionID, action, ok := ParseCustomID(BuildCustomID(tc.game, tc.sessionID, tc.action))
			if !ok {
				t.Fatalf("ParseCustomID(BuildCustomID(%q,%q,%q)) reported ok=false", tc.game, tc.sessionID, tc.action)
			}
			if game != tc.game || sessionID != tc.sessionID || action != tc.action {
				t.Fatalf("round trip changed the parts: got (%q,%q,%q), want (%q,%q,%q)",
					game, sessionID, action, tc.game, tc.sessionID, tc.action)
			}
		})
	}
}

func TestParseCustomID_Boundaries(t *testing.T) {
	// An ID of exactly 100 characters is still valid; 101 is not. Build both
	// from the real generator so the boundary is measured on what ships.
	const prefix = "casino:highlow:" // 15
	const suffix = ":high"           // 5
	atLimit := BuildCustomID("highlow", strings.Repeat("a", componentIDMaxLen-len(prefix)-len(suffix)), "high")
	if len(atLimit) != componentIDMaxLen {
		t.Fatalf("test setup: atLimit is %d characters, want %d", len(atLimit), componentIDMaxLen)
	}
	overLimit := atLimit + "a"

	// The same boundary in multi-byte characters: Discord counts characters,
	// so a 100-character / 260-byte ID is legal and must round trip. A
	// byte-counting limit would reject it.
	multibyteAtLimit := BuildCustomID("highlow", strings.Repeat("あ", componentIDMaxLen-len(prefix)-len(suffix)), "high")
	if got := utf8.RuneCountInString(multibyteAtLimit); got != componentIDMaxLen {
		t.Fatalf("test setup: multibyteAtLimit is %d characters, want %d", got, componentIDMaxLen)
	}
	if len(multibyteAtLimit) <= componentIDMaxLen {
		t.Fatalf("test setup: multibyteAtLimit is %d bytes, want more than %d", len(multibyteAtLimit), componentIDMaxLen)
	}
	multibyteOverLimit := BuildCustomID("highlow", strings.Repeat("あ", componentIDMaxLen-len(prefix)-len(suffix)+1), "high")

	tests := []struct {
		name string
		id   string
		ok   bool
	}{
		{name: "exactly 100 characters", id: atLimit, ok: true},
		{name: "101 characters", id: overLimit, ok: false},
		{name: "exactly 100 multibyte characters", id: multibyteAtLimit, ok: true},
		{name: "101 multibyte characters", id: multibyteOverLimit, ok: false},
		{name: "too few parts", id: "casino:highlow:8f3a", ok: false},
		{name: "one part", id: "casino", ok: false},
		{name: "empty", id: "", ok: false},
		{name: "too many parts", id: "casino:highlow:8f3a:high:extra", ok: false},
		{name: "foreign namespace", id: "other:highlow:8f3a:high", ok: false},
		{name: "empty session id", id: "casino:highlow::high", ok: false},
		{name: "empty action", id: "casino:highlow:8f3a:", ok: false},
		{name: "empty game", id: "casino::8f3a:high", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, ok := ParseCustomID(tc.id); ok != tc.ok {
				t.Fatalf("ParseCustomID(%q) ok = %v, want %v", tc.id, ok, tc.ok)
			}
		})
	}
}

// stubComponentHandler records the dispatch it received.
type stubComponentHandler struct {
	prefix      string
	calls       int
	gotSession  string
	gotAction   string
	returnedErr error
}

func (h *stubComponentHandler) Prefix() string { return h.prefix }

func (h *stubComponentHandler) HandleComponent(_ *discordgo.Session, _ *discordgo.InteractionCreate, sessionID, action string) error {
	h.calls++
	h.gotSession = sessionID
	h.gotAction = action
	return h.returnedErr
}

func TestRegisterComponent_DuplicatePrefixPanics(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)

	RegisterComponent(&stubComponentHandler{prefix: "highlow"})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("RegisterComponent accepted a duplicate prefix, want panic")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "highlow") {
			t.Fatalf("panic message does not name the duplicate prefix: %v", r)
		}
	}()
	RegisterComponent(&stubComponentHandler{prefix: "highlow"})
}

func TestRegisterComponent_DistinctPrefixesCoexist(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)

	RegisterComponent(&stubComponentHandler{prefix: "highlow"})
	RegisterComponent(&stubComponentHandler{prefix: "blackjack"})

	if len(registeredComponents) != 2 {
		t.Fatalf("expected 2 registered component handlers, got %d", len(registeredComponents))
	}
}

var errStubHandler = errors.New("stub component handler failed")

func componentInteraction(customID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID:    "1",
		Token: "TEST_TOKEN",
		Type:  discordgo.InteractionMessageComponent,
		Data: discordgo.MessageComponentInteractionData{
			CustomID:      customID,
			ComponentType: discordgo.ButtonComponent,
		},
		GuildID: "g1",
		Member:  &discordgo.Member{User: &discordgo.User{ID: "u1"}},
	}}
}

// capturingTransport answers every REST call with 204 and keeps the last
// request body, so a test can read the interaction response this package
// actually sent instead of asserting on a builder in isolation.
type capturingTransport struct{ body []byte }

func (t *capturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		t.body = b
	}
	return &http.Response{
		StatusCode: http.StatusNoContent,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

func capturingSession(t *testing.T) (*discordgo.Session, *capturingTransport) {
	t.Helper()
	s, err := discordgo.New("Bot test-bot-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	tr := &capturingTransport{}
	s.Client = &http.Client{Transport: tr}
	return s, tr
}

// sentResponse decodes the InteractionResponse the capturing transport saw.
func sentResponse(t *testing.T, tr *capturingTransport) discordgo.InteractionResponse {
	t.Helper()
	if len(tr.body) == 0 {
		t.Fatal("no interaction response was sent")
	}
	var resp discordgo.InteractionResponse
	if err := json.Unmarshal(tr.body, &resp); err != nil {
		t.Fatalf("decoding the sent response: %v (body %s)", err, tr.body)
	}
	return resp
}

func TestDispatchComponent_RoutesToTheRegisteredPrefix(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)

	h := &stubComponentHandler{prefix: "highlow"}
	RegisterComponent(h)
	RegisterComponent(&stubComponentHandler{prefix: "blackjack"})

	s, tr := capturingSession(t)
	if err := DispatchComponent(s, componentInteraction(BuildCustomID("highlow", "8f3a", "high"))); err != nil {
		t.Fatalf("DispatchComponent: %v", err)
	}
	if h.calls != 1 {
		t.Fatalf("expected the highlow handler to be called once, got %d", h.calls)
	}
	if h.gotSession != "8f3a" || h.gotAction != "high" {
		t.Fatalf("handler received (%q,%q), want (\"8f3a\",\"high\")", h.gotSession, h.gotAction)
	}
	if len(tr.body) != 0 {
		t.Fatalf("dispatch replied on its own instead of leaving the reply to the handler: %s", tr.body)
	}
}

func TestDispatchComponent_UnknownPrefixRepliesEphemerally(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)
	RegisterComponent(&stubComponentHandler{prefix: "highlow"})

	tests := []struct {
		name     string
		customID string
	}{
		{name: "unregistered game", customID: BuildCustomID("roulette", "8f3a", "spin")},
		{name: "foreign namespace", customID: "other:highlow:8f3a:high"},
		{name: "malformed id", customID: "casino:highlow"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, tr := capturingSession(t)
			if err := DispatchComponent(s, componentInteraction(tc.customID)); err != nil {
				t.Fatalf("DispatchComponent: %v", err)
			}
			resp := sentResponse(t, tr)
			if resp.Type != discordgo.InteractionResponseChannelMessageWithSource {
				t.Fatalf("unexpected response type %d", resp.Type)
			}
			if resp.Data == nil {
				t.Fatal("response carried no data")
			}
			if resp.Data.Content != "❌ このボタンは無効です" {
				t.Fatalf("unexpected content: %q", resp.Data.Content)
			}
			if resp.Data.Flags != discordgo.MessageFlagsEphemeral {
				t.Fatalf("expected the unknown-button reply to be ephemeral, got flags %d", resp.Data.Flags)
			}
		})
	}
}

func TestDispatchComponent_PropagatesTheHandlerError(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)

	boom := &stubComponentHandler{prefix: "highlow", returnedErr: errStubHandler}
	RegisterComponent(boom)

	s, tr := capturingSession(t)
	err := DispatchComponent(s, componentInteraction(BuildCustomID("highlow", "8f3a", "high")))
	if err != errStubHandler {
		t.Fatalf("DispatchComponent returned %v, want the handler's error", err)
	}
	if len(tr.body) != 0 {
		t.Fatalf("dispatch swallowed the handler error with a reply of its own: %s", tr.body)
	}
}

func TestRequireSessionOwner(t *testing.T) {
	const mismatch = "❌ これはあなたのゲームではありません"

	guild := func(id string) *discordgo.InteractionCreate {
		return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
			GuildID: "g1",
			Member:  &discordgo.Member{User: &discordgo.User{ID: id}},
		}}
	}
	dm := func(id string) *discordgo.InteractionCreate {
		return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
			User: &discordgo.User{ID: id},
		}}
	}

	tests := []struct {
		name    string
		i       *discordgo.InteractionCreate
		ownerID string
		want    string
	}{
		{name: "owner presses in a guild", i: guild("u1"), ownerID: "u1", want: ""},
		{name: "someone else presses in a guild", i: guild("u2"), ownerID: "u1", want: mismatch},
		{name: "owner presses in a DM", i: dm("u1"), ownerID: "u1", want: ""},
		{name: "someone else presses in a DM", i: dm("u2"), ownerID: "u1", want: mismatch},
		{name: "presser has no identity", i: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{}}, ownerID: "u1", want: mismatch},
		{name: "session has no owner", i: guild("u1"), ownerID: "", want: mismatch},
		{name: "neither side has an identity", i: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{}}, ownerID: "", want: mismatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := requireSessionOwner(tc.i, tc.ownerID); got != tc.want {
				t.Fatalf("requireSessionOwner = %q, want %q", got, tc.want)
			}
		})
	}
}
