package commands

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

// --- fixtures --------------------------------------------------------------

// recordingEditor is the sweeper's Discord half. Edits are handed over a
// channel rather than appended to a slice: the loop test reads them from
// another goroutine, and a slice shared that way would be a data race even
// where the assertion happens to pass.
type recordingEditor struct {
	edits   chan *discordgo.MessageEdit
	editErr error
}

func newRecordingEditor() *recordingEditor {
	return &recordingEditor{edits: make(chan *discordgo.MessageEdit, 4)}
}

func (e *recordingEditor) ChannelMessageEditComplex(m *discordgo.MessageEdit, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	e.edits <- m
	if e.editErr != nil {
		return nil, e.editErr
	}
	return &discordgo.Message{ID: m.ID, ChannelID: m.Channel}, nil
}

// took reports the one edit the sweeper sent, or fails.
func (e *recordingEditor) took(t *testing.T) *discordgo.MessageEdit {
	t.Helper()
	select {
	case edit := <-e.edits:
		return edit
	case <-time.After(2 * time.Second):
		t.Fatal("the sweeper edited no message")
		return nil
	}
}

// idle reports that the sweeper edited nothing. Only sound right after a
// synchronous sweepIdleBoards, where "not yet" and "never" are the same.
func (e *recordingEditor) idle(t *testing.T) {
	t.Helper()
	select {
	case edit := <-e.edits:
		t.Fatalf("the sweeper edited message %q, want no edit", edit.ID)
	default:
	}
}

// fixedResolver is a board that resolves to a number the test chose. The
// sweeper's contract is "AutoResolve once, pay exactly what it returns" —
// pinning it to a real game's rules would test the game instead.
type fixedResolver struct {
	payout   int64
	resolved int
}

func (f *fixedResolver) AutoResolve() int64 {
	f.resolved++
	return f.payout
}

// sweepFixture is an account with one staked board, on this test's own store
// and its own manager — never casino.Default()/DefaultSessions().
func sweepFixture(t *testing.T, state any, bet int64, ref casino.MessageRef) (*flakyBank, *casino.SessionManager, time.Time) {
	t.Helper()
	bank := &flakyBank{Store: casino.New(filepath.Join(t.TempDir(), "casino.json"))}
	opened := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	if err := bank.EnsureCasinoAccess("g1", "u1", opened); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	if err := bank.OpenGame("g1", "u1", string(casino.GameHighLow), bet, opened); err != nil {
		t.Fatalf("OpenGame: %v", err)
	}
	// The manager's clock is frozen at the deal, so LastActionAt is `opened`
	// and the sweeper's own clock alone decides what is expired.
	mgr := casino.NewSessionManager(func() time.Time { return opened }, casino.DefaultSessionTTL)
	if _, err := mgr.Open("g1", "u1", casino.GameHighLow, state, ref); err != nil {
		t.Fatalf("opening the board: %v", err)
	}
	return bank, mgr, opened
}

func sweepChipsOf(t *testing.T, bank *flakyBank) (chips, escrow int64) {
	t.Helper()
	view, err := bank.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	return view.Account.Chips, view.Account.Escrow
}

// --- one pass --------------------------------------------------------------

// 設計書 §5: Sweep → AutoResolve → 精算 → メッセージを編集.
func TestSweepIdleBoardsSettlesAnIdleBoardAndClosesItsMessage(t *testing.T) {
	board := &fixedResolver{payout: 250}
	bank, mgr, opened := sweepFixture(t, board, 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	editor := newRecordingEditor()

	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

	if board.resolved != 1 {
		t.Errorf("AutoResolve was called %d times, want exactly 1", board.resolved)
	}
	if chips, escrow := sweepChipsOf(t, bank); chips != 1150 || escrow != 0 {
		t.Errorf("account: got %d chips / %d escrow, want 1150 / 0 (1000 - ベット100 + 配当250)", chips, escrow)
	}
	if mgr.Len() != 0 {
		t.Errorf("%d boards are still live, want 0: a settled board must not stay pressable", mgr.Len())
	}

	edit := editor.took(t)
	if edit.Channel != "c1" || edit.ID != "m1" {
		t.Errorf("edited %s/%s, want c1/m1 — the sweeper must edit the board's own message", edit.Channel, edit.ID)
	}
	if edit.Embeds == nil || len(*edit.Embeds) != 1 {
		t.Fatal("the edit carries no single embed")
	}
	embed := (*edit.Embeds)[0]
	if embed.Title != casinoTimeoutTitle {
		t.Errorf("title: got %q, want %q", embed.Title, casinoTimeoutTitle)
	}
	if !strings.Contains(embed.Description, "配当: 250枚") || !strings.Contains(embed.Description, "残高: 1150枚") {
		t.Errorf("the closing line does not name what was paid: %q", embed.Description)
	}
	if edit.Components == nil || len(*edit.Components) != 0 {
		t.Error("the edit leaves buttons on a board that is over")
	}
}

// A real hand, to prove the type assertion in the sweeper matches what the
// games actually put in a session: blackjack auto-stands.
func TestSweepIdleBoardsAutoStandsARealBlackjackHand(t *testing.T) {
	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	wantPayout := blackjackProbe(100, seed).AutoResolve()

	bank, mgr, opened := sweepFixture(t, blackjackProbe(100, seed), 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	editor := newRecordingEditor()

	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

	if chips, escrow := sweepChipsOf(t, bank); chips != 900+wantPayout || escrow != 0 {
		t.Errorf("account: got %d chips / %d escrow, want %d / 0 (1000 - 100 + 自動スタンドの配当 %d)", chips, escrow, 900+wantPayout, wantPayout)
	}
	editor.took(t)
}

// The TTL belongs to the manager; the sweeper must not settle a board that
// Sweep did not hand it.
func TestSweepIdleBoardsLeavesALiveBoardAlone(t *testing.T) {
	board := &fixedResolver{payout: 250}
	bank, mgr, opened := sweepFixture(t, board, 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	editor := newRecordingEditor()

	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL-time.Second))

	if board.resolved != 0 {
		t.Errorf("AutoResolve was called %d times on a live board, want 0", board.resolved)
	}
	if bank.settles != 0 {
		t.Errorf("SettleGame was called %d times on a live board, want 0", bank.settles)
	}
	if chips, escrow := sweepChipsOf(t, bank); chips != 900 || escrow != 100 {
		t.Errorf("account: got %d chips / %d escrow, want 900 / 100 — the stake stays on the table", chips, escrow)
	}
	if mgr.Len() != 1 {
		t.Errorf("%d boards are live, want the untouched one", mgr.Len())
	}
	editor.idle(t)
}

// The chips are the part that must survive; the message is not. A board whose
// message was never recorded still settles.
func TestSweepIdleBoardsSettlesABoardWithNoMessageToEdit(t *testing.T) {
	board := &fixedResolver{payout: 250}
	bank, mgr, opened := sweepFixture(t, board, 100, casino.MessageRef{ChannelID: "c1"})
	editor := newRecordingEditor()

	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

	if chips, escrow := sweepChipsOf(t, bank); chips != 1150 || escrow != 0 {
		t.Errorf("account: got %d chips / %d escrow, want 1150 / 0", chips, escrow)
	}
	editor.idle(t)
}

// An edit that Discord refuses is logged and nothing more — the settlement
// already happened and must not be replayed.
func TestSweepIdleBoardsPaysOnceWhenTheEditFails(t *testing.T) {
	board := &fixedResolver{payout: 250}
	bank, mgr, opened := sweepFixture(t, board, 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	editor := newRecordingEditor()
	editor.editErr = errors.New("the edit never arrived")

	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

	if bank.settles != 1 {
		t.Errorf("SettleGame was called %d times, want exactly 1", bank.settles)
	}
	if chips, escrow := sweepChipsOf(t, bank); chips != 1150 || escrow != 0 {
		t.Errorf("account: got %d chips / %d escrow, want 1150 / 0", chips, escrow)
	}
	editor.took(t)
}

// A settlement the store refuses leaves the board gone and the stake in
// escrow, where RefundStaleEscrows returns it at the next restart. What must
// NOT happen is an edit telling the player they were paid.
func TestSweepIdleBoardsSaysNothingWhenTheSettlementFails(t *testing.T) {
	board := &fixedResolver{payout: 250}
	bank, mgr, opened := sweepFixture(t, board, 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	bank.settleErr = errors.New("the store refused this payout")
	editor := newRecordingEditor()

	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

	editor.idle(t)
	if chips, escrow := sweepChipsOf(t, bank); chips != 900 || escrow != 100 {
		t.Errorf("account: got %d chips / %d escrow, want 900 / 100 — the stake waits for RefundStaleEscrows", chips, escrow)
	}
}

// --- the loop --------------------------------------------------------------

// The goroutine cmd/bot/main.go starts: one pass per tick, and a return on
// ctx.Done() so the shutdown path can wait for it before closing the session.
func TestRunSessionSweeperSweepsOnEveryTickAndStopsWithTheContext(t *testing.T) {
	board := &fixedResolver{payout: 250}
	bank, mgr, opened := sweepFixture(t, board, 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	editor := newRecordingEditor()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A clock the sweeper reads and the test never writes: the board is
		// expired from the first tick onwards.
		runSessionSweeper(ctx, editor, mgr, bank, ticks, func() time.Time { return opened.Add(casino.DefaultSessionTTL) })
	}()

	ticks <- opened
	if edit := editor.took(t); edit.ID != "m1" {
		t.Errorf("the tick edited %q, want the expired board m1", edit.ID)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the sweeper did not return after its context was cancelled")
	}
}

// A session carrying something that is not a board cannot be resolved, and
// guessing a payout for it would invent chips. It is dropped (Sweep already
// removed it) with its stake left for RefundStaleEscrows.
func TestSweepIdleBoardsPaysNothingForAStateItCannotResolve(t *testing.T) {
	bank, mgr, opened := sweepFixture(t, struct{}{}, 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	editor := newRecordingEditor()

	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

	if bank.settles != 0 {
		t.Errorf("SettleGame was called %d times for an unresolvable board, want 0", bank.settles)
	}
	if chips, escrow := sweepChipsOf(t, bank); chips != 900 || escrow != 100 {
		t.Errorf("account: got %d chips / %d escrow, want 900 / 100", chips, escrow)
	}
	editor.idle(t)
}
