package commands

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

func TestRequireGuildContext_EmptyGuildID(t *testing.T) {
	msg := requireGuildContext(&discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: ""}})
	if msg != "❌ このコマンドはサーバー内で使用してください。" {
		t.Fatalf("unexpected message for a DM invocation: %q", msg)
	}
}

func TestRequireGuildContext_NonEmptyGuildID(t *testing.T) {
	if msg := requireGuildContext(&discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "g1"}}); msg != "" {
		t.Fatalf("expected no message inside a guild, got %q", msg)
	}
}

func TestRequireAdministrator_NilMember(t *testing.T) {
	msg := requireAdministrator(&discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "g1"}})
	if msg != "❌ このコマンドはサーバー管理者のみ使用できます" {
		t.Fatalf("unexpected message for a nil Member: %q", msg)
	}
}

func TestRequireAdministrator_MissingPermission(t *testing.T) {
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		GuildID: "g1",
		Member:  &discordgo.Member{Permissions: discordgo.PermissionSendMessages},
	}}
	if msg := requireAdministrator(i); msg != "❌ このコマンドはサーバー管理者のみ使用できます" {
		t.Fatalf("unexpected message for a non-administrator: %q", msg)
	}
}

func TestRequireAdministrator_HasPermission(t *testing.T) {
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		GuildID: "g1",
		Member:  &discordgo.Member{Permissions: discordgo.PermissionSendMessages | discordgo.PermissionAdministrator},
	}}
	if msg := requireAdministrator(i); msg != "" {
		t.Fatalf("expected no message for an administrator, got %q", msg)
	}
}

func TestSparkline_Empty(t *testing.T) {
	if got := sparkline(nil); got != "" {
		t.Fatalf("expected an empty string for an empty series, got %q", got)
	}
}

func TestSparkline_FlatSeries(t *testing.T) {
	if got := sparkline([]int{5, 5, 5}); got != "▁▁▁" {
		t.Fatalf("expected the lowest bar for every point of a flat series, got %q", got)
	}
}

func TestSparkline_IncreasingSeries(t *testing.T) {
	// lo=1, hi=3, span=2 → idx = (v-1)*7/2 → 0, 3, 7.
	if got := sparkline([]int{1, 2, 3}); got != "▁▄█" {
		t.Fatalf("unexpected sparkline for an increasing series: %q", got)
	}
}

func TestRateRankCommentary_TodayIsHighest_Rank1(t *testing.T) {
	history := []casino.DailyRate{{Rate: 90}, {Rate: 100}}
	if got := rateRankCommentary(history); got != "今日のレートは直近2日で1番目の高値です" {
		t.Fatalf("unexpected commentary: %q", got)
	}
}

func TestRateRankCommentary_TodayIsSecondHighest_Rank2(t *testing.T) {
	history := []casino.DailyRate{{Rate: 110}, {Rate: 100}}
	if got := rateRankCommentary(history); got != "今日のレートは直近2日で2番目の高値です" {
		t.Fatalf("unexpected commentary: %q", got)
	}
}

func TestRateRankCommentary_WindowCapsAt7Entries(t *testing.T) {
	// The three leading 200s are OUTSIDE the 7-entry window, so today (70) is
	// the highest of the window. Without the cap the rank would be 4.
	history := []casino.DailyRate{
		{Rate: 200}, {Rate: 200}, {Rate: 200},
		{Rate: 10}, {Rate: 20}, {Rate: 30}, {Rate: 40}, {Rate: 50}, {Rate: 60}, {Rate: 70},
	}
	if got := rateRankCommentary(history); got != "今日のレートは直近7日で1番目の高値です" {
		t.Fatalf("unexpected commentary: %q", got)
	}
}

func TestRateRankCommentary_Empty(t *testing.T) {
	if got := rateRankCommentary(nil); got != "" {
		t.Fatalf("expected an empty string for an empty history, got %q", got)
	}
}

func TestTrendStreak_Empty(t *testing.T) {
	if got := trendStreak(nil); got != 0 {
		t.Fatalf("expected 0 for an empty history, got %d", got)
	}
}

func TestTrendStreak_SingleEntry(t *testing.T) {
	if got := trendStreak([]casino.DailyRate{{Trend: casino.TrendBull}}); got != 1 {
		t.Fatalf("expected 1 for a single entry, got %d", got)
	}
}

func TestTrendStreak_ThreeConsecutive(t *testing.T) {
	history := []casino.DailyRate{
		{Trend: casino.TrendBull}, {Trend: casino.TrendBull}, {Trend: casino.TrendBull},
	}
	if got := trendStreak(history); got != 3 {
		t.Fatalf("expected 3, got %d", got)
	}
}

func TestTrendStreak_BrokenByOtherTrend(t *testing.T) {
	history := []casino.DailyRate{
		{Trend: casino.TrendBull}, {Trend: casino.TrendBear},
		{Trend: casino.TrendBull}, {Trend: casino.TrendBull},
	}
	if got := trendStreak(history); got != 2 {
		t.Fatalf("expected the streak to stop at the TrendBear entry, got %d", got)
	}
}

func TestMarketCommentary_Empty(t *testing.T) {
	if got := marketCommentary(nil); got != "" {
		t.Fatalf("expected an empty string for an empty history, got %q", got)
	}
}

func TestMarketCommentary_Bull3Days(t *testing.T) {
	history := []casino.DailyRate{
		{Trend: casino.TrendBull, Event: casino.EventNone},
		{Trend: casino.TrendBull, Event: casino.EventNone},
		{Trend: casino.TrendBull, Event: casino.EventNone},
	}
	if got := marketCommentary(history); got != "コイン強気3日目。天井はどこだ?" {
		t.Fatalf("unexpected commentary: %q", got)
	}
}

func TestMarketCommentary_Bear1Day(t *testing.T) {
	history := []casino.DailyRate{
		{Trend: casino.TrendBull, Event: casino.EventNone},
		{Trend: casino.TrendBear, Event: casino.EventNone},
	}
	if got := marketCommentary(history); got != "コイン弱気1日目。底値を狙うなら今か。" {
		t.Fatalf("unexpected commentary: %q", got)
	}
}

func TestMarketCommentary_Flat(t *testing.T) {
	history := []casino.DailyRate{
		{Trend: casino.TrendFlat, Event: casino.EventNone},
		{Trend: casino.TrendFlat, Event: casino.EventNone},
	}
	if got := marketCommentary(history); got != "凪2日目。動かない相場も相場だ。" {
		t.Fatalf("unexpected commentary: %q", got)
	}
}

func TestMarketCommentary_Surge(t *testing.T) {
	// A surge outranks the trend: the trend is still bull, but the headline
	// of the day is the event.
	history := []casino.DailyRate{
		{Trend: casino.TrendBull, Event: casino.EventNone},
		{Trend: casino.TrendBull, Event: casino.EventSurge},
	}
	got := marketCommentary(history)
	if got != "🚀 暴騰デー! コインが跳ねた。売り抜けるなら今日だ。" {
		t.Fatalf("unexpected commentary: %q", got)
	}
	if strings.Contains(got, "強気") {
		t.Fatalf("the surge wording must replace the trend wording, got %q", got)
	}
}

func TestMarketCommentary_Crash(t *testing.T) {
	history := []casino.DailyRate{
		{Trend: casino.TrendBear, Event: casino.EventNone},
		{Trend: casino.TrendBear, Event: casino.EventCrash},
	}
	if got := marketCommentary(history); got != "💥 暴落デー! コインが崩れた。拾いに行くか、様子を見るか。" {
		t.Fatalf("unexpected commentary: %q", got)
	}
}

func TestMarketCommentary_LegacyEmptyEventTreatedAsNone(t *testing.T) {
	// "" is what a hand-edited or pre-C-1 record carries; it must fall
	// through to the trend wording, not to a surge/crash headline.
	history := []casino.DailyRate{{Trend: casino.TrendBull, Event: ""}}
	if got := marketCommentary(history); got != "コイン強気1日目。天井はどこだ?" {
		t.Fatalf("unexpected commentary: %q", got)
	}
}

// TestRedactInteractionError_DropsTokens pins the one property every log site
// in this package depends on: whatever comes back, it does not carry the
// Discord token. The *url.Error case is what net/http actually hands back
// from a failed InteractionRespond; the string cases cover errors from other
// layers that merely quote a path.
func TestRedactInteractionError_DropsTokens(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantContain string
	}{
		{
			name:        "url.Error from InteractionRespond",
			err:         &url.Error{Op: "Post", URL: "https://discord.com/api/v9/interactions/1/TEST_TOKEN/callback", Err: errors.New("connection reset")},
			wantContain: "Post:",
		},
		{
			name:        "url.Error from a webhook edit",
			err:         &url.Error{Op: "Patch", URL: "https://discord.com/api/v9/webhooks/1/TEST_TOKEN/messages/@original", Err: errors.New("timeout")},
			wantContain: "Patch:",
		},
		{
			name:        "wrapped url.Error",
			err:         fmt.Errorf("reveal: %w", &url.Error{Op: "Post", URL: "https://discord.com/api/v9/interactions/1/TEST_TOKEN/callback", Err: errors.New("EOF")}),
			wantContain: "Post:",
		},
		{
			// The cause net/http wraps can quote the request itself, so the
			// URL is not the only place a token can hide inside *url.Error.
			name:        "url.Error whose cause carries the token",
			err:         &url.Error{Op: "Post", URL: "https://discord.com/api/v9/interactions/1/TEST_TOKEN/callback", Err: errors.New("request token TEST_TOKEN")},
			wantContain: "*errors.errorString",
		},
		{
			name:        "plain error quoting an interaction path",
			err:         errors.New("HTTP 401 Unauthorized on /interactions/1/TEST_TOKEN/callback"),
			wantContain: "[redacted]",
		},
		{
			name:        "plain error quoting a webhook path",
			err:         errors.New("HTTP 404 on /webhooks/1/TEST_TOKEN/messages/@original"),
			wantContain: "[redacted]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redactInteractionError(tc.err)
			if strings.Contains(got, "TEST_TOKEN") {
				t.Fatalf("redactInteractionError kept the token: %q", got)
			}
			if !strings.Contains(got, tc.wantContain) {
				t.Fatalf("redactInteractionError = %q, want it to still say %q", got, tc.wantContain)
			}
		})
	}
}

func TestRedactInteractionError_NilError(t *testing.T) {
	if got := redactInteractionError(nil); got != "<nil>" {
		t.Fatalf("redactInteractionError(nil) = %q, want \"<nil>\"", got)
	}
}

// --- the one door out of the manager (C3B-P4) -------------------------------

// closeBoardSourcePattern matches a call that retires a board directly:
// `c.sessions.Close(id)`, `sessions.Close(id)`, or any `.Close(sessionID)`.
// It is deliberately narrow enough not to fire on an io.Closer — a response
// closeBoardDoorFile is the ONE file allowed to take a board out of the
// manager, and managerRetirementMethods are the manager methods that do it.
// Close is a press's door and Remove is the sweeper's; both are reached from
// inside closeBoardIfSettled and from nowhere else (C3B-X1).
const closeBoardDoorFile = "casino_shared.go"

var managerRetirementMethods = map[string]bool{"Close": true, "Remove": true}

// A board must leave the manager through closeBoardIfSettled and nowhere else.
//
// Five stranded stakes were found one at a time, each on a route that closed a
// board while the account still held chips marked with it, and each fix
// repaired the route it was told about. Reviewing every new route for the same
// mistake is the thing that kept failing — so this reads the package's own
// source instead, and a new route that retires a board itself fails here
// rather than in three months' worth of escrow.
//
// It reads the sources as a SYNTAX TREE rather than as lines (C3B-X1). The
// line-wise version it replaces could be walked straight past by splitting the
// call — gofmt keeps
//
//	c.sessions.
//		Close(sessionID)
//
// exactly as written, and no line of it contains both halves. A guard that a
// formatter-stable rewrite defeats is not a guard. The receiver names are not
// guessed either: each file is asked which of its identifiers were DECLARED as
// *casino.SessionManager (fields, parameters, vars), so renaming the field
// cannot quietly empty the scan.
//
// _test.go is skipped: a test may retire a board to BUILD the state it tests.
func TestOnlyOneEntryPointRetiresABoard(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	fset := token.NewFileSet()
	scanned, doorCalls := 0, map[string]int{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		scanned++

		managers := sessionManagerNames(file)
		ast.Inspect(file, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			method, isSelector := call.Fun.(*ast.SelectorExpr)
			if !isSelector || !managerRetirementMethods[method.Sel.Name] {
				return true
			}
			receiver := trailingIdentOf(method.X)
			if !managers[receiver] {
				return true // somebody else's Close: an http body, a file
			}
			if name == closeBoardDoorFile {
				doorCalls[method.Sel.Name]++
				return true
			}
			t.Errorf("%s: %s.%s retires a board directly\n\tuse closeBoardIfSettled — retiring a board whose stake is still marked with it strands the chips until the next restart",
				fset.Position(call.Pos()), receiver, method.Sel.Name)
			return true
		})
	}

	if scanned == 0 {
		t.Fatal("scanned no sources — the guard would pass on an empty read, which is how a scan like this rots")
	}
	for method := range managerRetirementMethods {
		if doorCalls[method] == 0 {
			t.Errorf("%s no longer calls %s: the one door has moved, and this scan is now guarding nothing", closeBoardDoorFile, method)
		}
	}
}

// The scan has to be able to SEE a call the old line-wise one could not. This
// is that call, spelled the way gofmt leaves it, fed through the same reader.
func TestTheBoardScanSeesACallSplitAcrossLines(t *testing.T) {
	const source = `package commands

import "github.com/SioKo-Shox3/todayistodayBot/internal/casino"

type sneaky struct {
	sessions *casino.SessionManager
}

func (c *sneaky) drop(sessionID string) {
	c.sessions.
		Close(sessionID)
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "sneaky.go", source, 0)
	if err != nil {
		t.Fatalf("parsing the sample: %v", err)
	}

	managers := sessionManagerNames(file)
	if !managers["sessions"] {
		t.Fatalf("the declared manager was not found: got %v", managers)
	}

	found := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		method, isSelector := call.Fun.(*ast.SelectorExpr)
		if isSelector && managerRetirementMethods[method.Sel.Name] && managers[trailingIdentOf(method.X)] {
			found++
		}
		return true
	})
	if found != 1 {
		t.Fatalf("the scan found %d retiring calls in the sample, want 1 — a call split across lines walks past it", found)
	}

	// The same source read a line at a time is what used to be trusted, and
	// no single line of it carries both halves.
	for _, line := range strings.Split(source, "\n") {
		if strings.Contains(line, "sessions") && strings.Contains(line, "Close(") {
			t.Fatalf("a line-wise scan would have caught this after all: %q", strings.TrimSpace(line))
		}
	}
}

// sessionManagerNames answers which identifiers in this file were declared as
// *casino.SessionManager — struct fields, function parameters, vars. The scan
// asks the file rather than carrying a list of names it hopes is current.
func sessionManagerNames(file *ast.File) map[string]bool {
	names := map[string]bool{}
	collect := func(idents []*ast.Ident, typ ast.Expr) {
		if !isSessionManagerPointer(typ) {
			return
		}
		for _, ident := range idents {
			names[ident.Name] = true
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch declared := node.(type) {
		case *ast.Field:
			collect(declared.Names, declared.Type)
		case *ast.ValueSpec:
			collect(declared.Names, declared.Type)
		}
		return true
	})
	return names
}

// isSessionManagerPointer reports whether typ is written *casino.SessionManager.
func isSessionManagerPointer(typ ast.Expr) bool {
	pointer, isPointer := typ.(*ast.StarExpr)
	if !isPointer {
		return false
	}
	qualified, isQualified := pointer.X.(*ast.SelectorExpr)
	if !isQualified || qualified.Sel.Name != "SessionManager" {
		return false
	}
	pkg, isIdent := qualified.X.(*ast.Ident)
	return isIdent && pkg.Name == "casino"
}

// trailingIdentOf names the receiver of a call: "sessions" for c.sessions,
// "mgr" for mgr. Whitespace and line breaks are already gone by the time the
// tree is built, which is the whole reason this reads a tree.
func trailingIdentOf(expr ast.Expr) string {
	switch receiver := expr.(type) {
	case *ast.Ident:
		return receiver.Name
	case *ast.SelectorExpr:
		return receiver.Sel.Name
	case *ast.ParenExpr:
		return trailingIdentOf(receiver.X)
	}
	return ""
}

// The door's whole job: a board whose stake is still marked with it stays.
func TestCloseBoardIfSettledKeepsABoardThatStillHoldsItsStake(t *testing.T) {
	bank, sessions, sessionID := doorFixture(t, 100)

	if err := closeBoardIfSettled(bank, sessions, "g1", "u1", sessionID); !errors.Is(err, errBoardStillHoldsStake) {
		t.Fatalf("closeBoardIfSettled = %v, want errBoardStillHoldsStake", err)
	}
	if sessions.Len() != 1 {
		t.Fatalf("%d boards are live, want 1 — the board is the only thing that can give the stake back", sessions.Len())
	}
}

// ...and one whose stake has been settled goes.
func TestCloseBoardIfSettledDropsABoardWhoseStakeIsGone(t *testing.T) {
	bank, sessions, sessionID := doorFixture(t, 100)
	if _, err := bank.SettleGame("g1", "u1", sessionID, 100); err != nil {
		t.Fatalf("settling the stake: %v", err)
	}

	if err := closeBoardIfSettled(bank, sessions, "g1", "u1", sessionID); err != nil {
		t.Fatalf("closeBoardIfSettled = %v, want nil", err)
	}
	if sessions.Len() != 0 {
		t.Fatalf("%d boards are live, want 0 — a paid board has nothing left to release", sessions.Len())
	}
}

// A stake that names ANOTHER board is not this board's to wait for: this one
// can never settle it (every call names its board, and the store answers
// ErrEscrowMismatch), so holding the board open would only keep its owner out
// of new games without ever releasing a chip.
func TestCloseBoardIfSettledDropsABoardWhoseStakeNamesAnotherBoard(t *testing.T) {
	bank, sessions := doorStore(t)
	boardID, escrowID := doorSessionID(t), doorSessionID(t)
	doorBoard(t, sessions, boardID)
	if err := bank.OpenGame("g1", "u1", string(casino.GameBlackjack), escrowID, 100, time.Now()); err != nil {
		t.Fatalf("OpenGame: %v", err)
	}

	if err := closeBoardIfSettled(bank, sessions, "g1", "u1", boardID); err != nil {
		t.Fatalf("closeBoardIfSettled = %v, want nil", err)
	}
	if sessions.Len() != 0 {
		t.Fatalf("%d boards are live, want 0", sessions.Len())
	}
}

// An account that cannot be read is not evidence that dropping is safe.
func TestCloseBoardIfSettledKeepsABoardWhenTheStoreCannotBeRead(t *testing.T) {
	bank, sessions, sessionID := doorFixture(t, 100)
	unreadable := &escrowBlindBank{flakyBank: bank, escrowErr: errors.New("casino: read casino.json: i/o error")}

	if err := closeBoardIfSettled(unreadable, sessions, "g1", "u1", sessionID); err == nil {
		t.Fatal("closeBoardIfSettled = nil, want the store's error")
	}
	if sessions.Len() != 1 {
		t.Fatalf("%d boards are live, want 1 — an unreadable account decides nothing", sessions.Len())
	}
}

// The sweeper retires an EXPIRED board through this same door (C3B-X1). It
// used to call casino.SessionManager.Remove itself, which was a second way
// out of the manager and asked the account nothing; now the one question —
// "is the stake still marked with this board" — is asked of the sweep's
// boards as well, and Remove is reached from inside the door alone.
func TestCloseBoardIfSettledRetiresAnExpiredBoardWhoseStakeIsGone(t *testing.T) {
	bank, sessions, sessionID := doorFixture(t, 100)
	if _, err := bank.SettleGame("g1", "u1", sessionID, 100); err != nil {
		t.Fatalf("settling the stake: %v", err)
	}
	sessions.Release(sessionID) // end the open, so the sweep may take the board
	if expired := sessions.Sweep(time.Now().Add(2 * casino.DefaultSessionTTL)); len(expired) != 1 {
		t.Fatalf("%d boards expired, want 1", len(expired))
	}

	if err := closeBoardIfSettled(bank, sessions, "g1", "u1", sessionID); err != nil {
		t.Fatalf("closeBoardIfSettled = %v, want nil", err)
	}
	if sessions.Len() != 0 {
		t.Fatalf("%d boards are left, want 0 — the settlement landed, so the door must retire the sweep's board too", sessions.Len())
	}
}

// ...and the account still decides. An expired board whose stake is STILL
// marked with it is the one the sweep has not managed to pay yet: retiring it
// here would strand exactly those chips, which is the defect this whole door
// exists to make unreachable.
func TestCloseBoardIfSettledKeepsAnExpiredBoardThatStillHoldsItsStake(t *testing.T) {
	bank, sessions, sessionID := doorFixture(t, 100)
	sessions.Release(sessionID) // end the open, so the sweep may take the board
	if expired := sessions.Sweep(time.Now().Add(2 * casino.DefaultSessionTTL)); len(expired) != 1 {
		t.Fatalf("%d boards expired, want 1", len(expired))
	}

	if err := closeBoardIfSettled(bank, sessions, "g1", "u1", sessionID); !errors.Is(err, errBoardStillHoldsStake) {
		t.Fatalf("closeBoardIfSettled = %v, want errBoardStillHoldsStake", err)
	}
	if sessions.Len() != 1 {
		t.Fatalf("%d boards are left, want 1 — the board is the only thing that names the stake", sessions.Len())
	}
}

// doorFixture stakes `bet` chips on a live blackjack board and hands back the
// three things the door needs. The board is left HELD, exactly as it is during
// an open, which is when most of the door's callers run.
func doorFixture(t *testing.T, bet int64) (*flakyBank, *casino.SessionManager, string) {
	t.Helper()
	bank, sessions := doorStore(t)
	sessionID := doorSessionID(t)
	doorBoard(t, sessions, sessionID)
	if err := bank.OpenGame("g1", "u1", string(casino.GameBlackjack), sessionID, bet, time.Now()); err != nil {
		t.Fatalf("OpenGame: %v", err)
	}
	return bank, sessions, sessionID
}

// doorStore is one test's own store and board registry — never
// casino.Default()/DefaultSessions(), which point at the production file.
func doorStore(t *testing.T) (*flakyBank, *casino.SessionManager) {
	t.Helper()
	return &flakyBank{Store: casino.New(filepath.Join(t.TempDir(), "casino.json"))},
		casino.NewSessionManager(time.Now, casino.DefaultSessionTTL)
}

func doorSessionID(t *testing.T) string {
	t.Helper()
	id, err := casino.NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	return id
}

// doorBoard registers a blackjack board under id. The hand itself never
// matters here: the door decides from the ACCOUNT, and reading the game is
// exactly what it must not do.
func doorBoard(t *testing.T, sessions *casino.SessionManager, id string) {
	t.Helper()
	if _, err := sessions.OpenWithID(id, "g1", "u1", casino.GameBlackjack, casino.NewBlackjack(100, zeroRng{}), casino.MessageRef{ChannelID: "c1"}); err != nil {
		t.Fatalf("OpenWithID: %v", err)
	}
}

// escrowBlindBank is a store whose escrow READ fails while everything else
// works — the one state that must not be read as "dropping is safe".
type escrowBlindBank struct {
	*flakyBank
	escrowErr error
}

func (b *escrowBlindBank) EscrowHeldBy(guildID, userID, sessionID string) (bool, error) {
	return false, b.escrowErr
}

// The two lines a board answers a press with while its chips have not moved are
// not interchangeable. casinoSettleFailedMessage sits under a 🔁 the player can
// press, so it asks for a retry; a board refused by RefundPending has no such
// button left to reach — the idle sweep is the retry — so its line must tell
// the player to wait. Collapsing them back into one string (the state before
// C3B-X2) asks a player to press buttons that do nothing.
func TestTheRefundPendingLineTellsThePlayerToWaitRatherThanRetry(t *testing.T) {
	if casinoRefundPendingMessage == casinoSettleFailedMessage {
		t.Fatal("the waiting line and the refused-settlement line are the same string: a press with no retry behind it is asking the player to retry")
	}
	for _, retry := range []string{"もう一度", "再度", "やり直"} {
		if strings.Contains(casinoRefundPendingMessage, retry) {
			t.Errorf("the waiting line contains %q — the player has nothing to retry here, the sweep does it for them", retry)
		}
	}
}
