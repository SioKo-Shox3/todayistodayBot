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

// testEscrowSession is the board a test stakes chips for when it opens an
// escrow directly on the store instead of going through a command (C-3b).
// Any settlement in the same test must name the same board, exactly as the
// command layer does with the ID casino.SessionManager handed it.
const testEscrowSession = "test-board"

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

// sweepFixture is an account with one staked high&low board, on this test's
// own store and its own manager — never casino.Default()/DefaultSessions().
func sweepFixture(t *testing.T, state any, bet int64, ref casino.MessageRef) (*flakyBank, *casino.SessionManager, time.Time) {
	t.Helper()
	bank, mgr, _, opened := sweepFixtureFor(t, casino.GameHighLow, state, bet, ref)
	return bank, mgr, opened
}

// sweepFixtureFor is sweepFixture for a board of any game, and it hands back
// the board itself: a stake and the board that owns it name each other
// (C-3b), so a test that needs to settle the stake from the outside needs the
// board's ID to do it with.
func sweepFixtureFor(t *testing.T, game casino.GameKind, state any, bet int64, ref casino.MessageRef) (*flakyBank, *casino.SessionManager, *casino.Session, time.Time) {
	t.Helper()
	bank := &flakyBank{Store: casino.New(filepath.Join(t.TempDir(), "casino.json"))}
	opened := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	if err := bank.EnsureCasinoAccess("g1", "u1", opened); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	// Opened in the command layer's own order (C-3b): the board's ID is
	// minted first, the stake is marked with it, and the same ID opens the
	// board — one marked write, no gap.
	sessionID, err := casino.NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	if err := bank.OpenGame("g1", "u1", string(game), sessionID, bet, opened); err != nil {
		t.Fatalf("OpenGame: %v", err)
	}
	// The manager's clock is frozen at the deal, so LastActionAt is `opened`
	// and the sweeper's own clock alone decides what is expired.
	mgr := casino.NewSessionManager(func() time.Time { return opened }, casino.DefaultSessionTTL)
	session, err := mgr.OpenWithID(sessionID, "g1", "u1", game, state, ref)
	if err != nil {
		t.Fatalf("opening the board: %v", err)
	}
	return bank, mgr, session, opened
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

// A settlement the store refuses leaves the stake in escrow. What must NOT
// happen is an edit telling the player they were paid — and (C2-10) the board
// must stay, because it is the only record of the payout that was decided.
func TestSweepIdleBoardsSaysNothingWhenTheSettlementFails(t *testing.T) {
	board := &fixedResolver{payout: 250}
	bank, mgr, opened := sweepFixture(t, board, 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	bank.settleErr = errors.New("the store refused this payout")
	editor := newRecordingEditor()

	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

	editor.idle(t)
	if chips, escrow := sweepChipsOf(t, bank); chips != 900 || escrow != 100 {
		t.Errorf("account: got %d chips / %d escrow, want 900 / 100 — the stake is still on the table", chips, escrow)
	}
	if mgr.Len() != 1 {
		t.Errorf("%d boards left, want the unsettled one kept for the next pass", mgr.Len())
	}
}

// C2-10: the payout is only real once the store has it. A board dropped on a
// failed settlement took the payout with it and left the stake in escrow,
// where it also blocked every new game until the next restart. The next pass
// now pays the SAME amount and only then lets the board go.
func TestSweepIdleBoardsRetriesTheSameSettlementAtTheNextPass(t *testing.T) {
	board := &fixedResolver{payout: 173}
	bank, mgr, opened := sweepFixture(t, board, 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	editor := newRecordingEditor()

	bank.settleErr = errors.New("the store refused this payout")
	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))
	editor.idle(t)

	// The store is well again, and the sweeper comes back one interval later.
	bank.settleErr = nil
	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL+CasinoSweepInterval))

	if board.resolved != 1 {
		t.Errorf("AutoResolve was called %d times, want exactly 1 — a retry must pay the payout the first pass decided, not deal a new one", board.resolved)
	}
	if bank.settles != 2 {
		t.Errorf("SettleGame was called %d times, want 2 (one refusal, one retry)", bank.settles)
	}
	if chips, escrow := sweepChipsOf(t, bank); chips != 1073 || escrow != 0 {
		t.Errorf("account: got %d chips / %d escrow, want 1073 / 0 (1000 - ベット100 + ポット173)", chips, escrow)
	}
	if mgr.Len() != 0 {
		t.Errorf("%d boards left after the retry settled, want 0", mgr.Len())
	}

	// The closing message is the retry's job too: the player learns the hand
	// is over on the pass that actually paid them.
	edit := editor.took(t)
	if edit.ID != "m1" {
		t.Errorf("the retry edited %q, want the board's own message m1", edit.ID)
	}
	if embed := (*edit.Embeds)[0]; !strings.Contains(embed.Description, "配当: 173枚") {
		t.Errorf("the closing line does not name the retried payout: %q", embed.Description)
	}
}

// The retry stops at the store's own "there is no game here": an escrow that
// a press (or an earlier retry that failed after the payout landed) already
// closed has nothing left to pay, and retrying it forever would keep its
// owner locked out of new games.
func TestSweepIdleBoardsDropsABoardWhoseStakeIsAlreadyGone(t *testing.T) {
	board := &fixedResolver{payout: 250}
	bank, mgr, session, opened := sweepFixtureFor(t, casino.GameHighLow, board, 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	editor := newRecordingEditor()

	// Somebody else closed the escrow between the timeout and this pass —
	// this board's own stake, named by this board (C-3b).
	if _, err := bank.Store.SettleGame("g1", "u1", session.ID, 100); err != nil {
		t.Fatalf("closing the escrow behind the sweeper: %v", err)
	}

	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

	if mgr.Len() != 0 {
		t.Errorf("%d boards left, want 0 — a board with no stake left must not be retried forever", mgr.Len())
	}
	if chips, escrow := sweepChipsOf(t, bank); chips != 1000 || escrow != 0 {
		t.Errorf("account: got %d chips / %d escrow, want 1000 / 0 — the sweeper must not pay a second time", chips, escrow)
	}
	editor.idle(t)
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
// guessing a payout for it would invent chips. It is dropped — no retry can
// ever help it — with its stake left for RefundStaleEscrows.
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
	if mgr.Len() != 0 {
		t.Errorf("%d boards left, want 0 — an unresolvable board must not be retried forever", mgr.Len())
	}
	editor.idle(t)
}

// C2-10: while a board waits for its settlement to be retried it belongs to
// the sweeper, so the buttons under it must answer the ⌛ closing line rather
// than play a hand whose payout is already decided.
func TestSweepIdleBoardsLeavesTheTimedOutBoardUnpressable(t *testing.T) {
	c, bank := newHighLowCommandOnBank(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)
	editor := newRecordingEditor()

	bank.settleErr = errors.New("the store refused this payout")
	sweepIdleBoards(editor, c.sessions, bank, time.Now().Add(2*casino.DefaultSessionTTL))
	if bank.settles != 1 {
		t.Fatalf("SettleGame was called %d times, want the one refusal the rest of this test builds on", bank.settles)
	}

	if got := c.press(t, r, sessionID, highLowActionCashOut).Data.Content; got != highLowSessionOverMessage {
		t.Errorf("pressing a board that is waiting for its retry answered %q, want %q", got, highLowSessionOverMessage)
	}
	if got := highLowEscrowOf(t, bank); got != 100 {
		t.Errorf("escrow after the refused press: got %d, want the stake (100) still held for the retry", got)
	}

	// And the retry still pays: the press did not consume the board.
	bank.settleErr = nil
	sweepIdleBoards(editor, c.sessions, bank, time.Now().Add(2*casino.DefaultSessionTTL))
	if bank.settles != 2 {
		t.Errorf("SettleGame was called %d times, want 2 (the refusal and the retry)", bank.settles)
	}
	if got := highLowEscrowOf(t, bank); got != 0 {
		t.Errorf("escrow after the retry: got %d, want 0", got)
	}
	if c.sessions.Len() != 0 {
		t.Errorf("%d boards left after the retry settled, want 0", c.sessions.Len())
	}
}

// --- 評価者の指摘(反復 1): 時間切れのボタンは無効化して残す ----------------

// 設計書 §4/§8: 決着したメッセージのボタンは無効化(disabled)して残す. The
// sweeper used to clear the row instead, which loses the hand the player was
// looking at. It now asks the game that registered the custom_id prefix to
// redraw its own buttons greyed out.
func TestSweepIdleBoardsKeepsTheGamesButtonsDisabled(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)
	RegisterComponent(&BlackjackCommand{}) // the real renderer, not a stand-in

	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	// A blackjack board, so the sweeper has to route by the session's own
	// game rather than by the fixture's default.
	bank, mgr, session, opened := sweepFixtureFor(t, casino.GameBlackjack, blackjackProbe(100, seed), 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})

	editor := newRecordingEditor()
	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

	edit := editor.took(t)
	if edit.Components == nil || len(*edit.Components) != 1 {
		t.Fatal("the timed-out board lost its buttons instead of keeping them disabled")
	}
	row, isRow := (*edit.Components)[0].(discordgo.ActionsRow)
	if !isRow {
		t.Fatalf("the kept components are %T, want discordgo.ActionsRow", (*edit.Components)[0])
	}
	if len(row.Components) != 3 {
		t.Fatalf("the row kept %d buttons, want blackjack's 3", len(row.Components))
	}
	for _, component := range row.Components {
		button, isButton := component.(discordgo.Button)
		if !isButton {
			t.Fatalf("the row holds a %T, want discordgo.Button", component)
		}
		if !button.Disabled {
			t.Errorf("button %q is still pressable on a board that is over", button.Label)
		}
		if game, id, _, ok := ParseCustomID(button.CustomID); !ok || game != string(casino.GameBlackjack) || id != session.ID {
			t.Errorf("button %q carries custom_id %q, want this blackjack board's", button.Label, button.CustomID)
		}
	}
}

// A game that does not draw its own closing row still gets its live buttons
// removed — a nil Components field would leave a PLAYABLE row under a board
// that is over.
func TestSweepIdleBoardsClearsTheButtonsOfAGameThatCannotRedrawThem(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)

	board := &fixedResolver{payout: 250}
	bank, mgr, opened := sweepFixture(t, board, 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	editor := newRecordingEditor()

	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

	edit := editor.took(t)
	if edit.Components == nil || len(*edit.Components) != 0 {
		t.Error("an unrenderable board must have its live buttons cleared")
	}
}

// --- C2-12: 時間切れの決着は結果を見せる ------------------------------------

// The sweeper's edit REPLACES the board, so the money line alone erased the
// hand: the dealer's hole card is turned over by AutoResolve and, once the
// board embed is gone, appears nowhere else. The closing message must carry
// the game's own result — both hands face up and the verdict — for a win, a
// loss and a push alike.
func TestSweepIdleBoardsShowsTheTimedOutHandAndItsVerdict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result casino.BlackjackResult
	}{
		{"勝ち", casino.BlackjackPlayerWin},
		{"負け", casino.BlackjackDealerWin},
		{"プッシュ", casino.BlackjackPush},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetComponentsForTest()
			t.Cleanup(resetComponentsForTest)
			RegisterComponent(&BlackjackCommand{}) // the real renderer, not a stand-in

			seed := blackjackAutoStandSeedFor(t, tc.result)
			// The same hand the sweeper will resolve, played out here so the
			// assertions below name the game's numbers rather than restate
			// blackjack's rules.
			probe := blackjackProbe(100, seed)
			wantPayout := probe.AutoResolve()

			bank, mgr, _, opened := sweepFixtureFor(t, casino.GameBlackjack, blackjackProbe(100, seed), 100, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})

			editor := newRecordingEditor()
			sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

			embed := (*editor.took(t).Embeds)[0]
			if embed.Title != casinoTimeoutTitle {
				t.Errorf("title: got %q, want %q", embed.Title, casinoTimeoutTitle)
			}
			if !strings.Contains(embed.Description, casinoTimeoutNotice) {
				t.Errorf("the closing message does not say why the hand ended:\n%s", embed.Description)
			}
			if want := blackjackHand(probe.Dealer()); !strings.Contains(embed.Description, want) {
				t.Errorf("the dealer's hole card is still hidden: want %q in\n%s", want, embed.Description)
			}
			if strings.Contains(embed.Description, blackjackHiddenCard) {
				t.Errorf("a settled hand still shows a face-down card:\n%s", embed.Description)
			}
			if want := blackjackHeadline(snapshotBlackjack(probe)); !strings.Contains(embed.Description, want) {
				t.Errorf("the closing message does not say who won: want %q in\n%s", want, embed.Description)
			}
			if want := casinoPayoutLine(wantPayout, 900+wantPayout); !strings.Contains(embed.Description, want) {
				t.Errorf("the closing message does not name the money: want %q in\n%s", want, embed.Description)
			}
		})
	}
}

// blackjackAutoStandSeedFor finds a shoe whose playable hand ends in `want`
// once the sweeper auto-stands it. The predicate resolves a throwaway copy:
// what a test needs to name is the hand AFTER the dealer has played.
func blackjackAutoStandSeedFor(t *testing.T, want casino.BlackjackResult) int64 {
	t.Helper()
	return blackjackSeedWhere(t, "a playable hand that auto-stands into the wanted result", func(g *casino.BlackjackGame) bool {
		if g.State() != casino.BlackjackPlaying {
			return false
		}
		g.AutoResolve()
		return g.Result() == want
	})
}

// The same for high&low: AutoResolve is a cash-out, so the closing message
// carries the card the board stopped on, the streak it paid for, and the
// money — not just the money.
func TestSweepIdleBoardsShowsTheTimedOutBoardsCardAndStreak(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)
	RegisterComponent(&HighLowCommand{})

	c, bank := newHighLowCommandOnBank(t)
	r := &fakeHighLowResponder{}
	startHighLow(t, c, r, 100)
	editor := newRecordingEditor()

	sweepIdleBoards(editor, c.sessions, bank, time.Now().Add(2*casino.DefaultSessionTTL))

	embed := (*editor.took(t).Embeds)[0]
	if embed.Title != casinoTimeoutTitle {
		t.Errorf("title: got %q, want %q", embed.Title, casinoTimeoutTitle)
	}
	if !strings.Contains(embed.Description, casinoTimeoutNotice) {
		t.Errorf("the closing message does not say why the board ended:\n%s", embed.Description)
	}
	// The board the command dealt: same bet, same generator, same first card.
	probe := casino.NewHighLow(100, zeroRng{})
	if want := probe.Current().String(); !strings.Contains(embed.Description, want) {
		t.Errorf("the closing message does not show the final card %q:\n%s", want, embed.Description)
	}
	if !strings.Contains(embed.Description, "💰 キャッシュアウト(0連勝)") {
		t.Errorf("the closing message does not name the auto cash-out and its streak:\n%s", embed.Description)
	}
	// The pot is the untouched bet, and it comes back on top of the 900 left.
	if want := casinoPayoutLine(100, 1000); !strings.Contains(embed.Description, want) {
		t.Errorf("the closing message does not name the money: want %q in\n%s", want, embed.Description)
	}
}

// --- the stake names its board from the first write (C3B-18) ---------------

// escrowMarkRecorder is a real store that remembers the mark a stake was
// opened with. That mark is not observable afterwards — the board's ID is the
// only name a later caller ever uses — and it is exactly what this change is
// about: the stake used to be opened under a provisional mark and handed to
// its board by a SECOND call, leaving a window in which nothing presenting a
// real board ID could settle it.
type escrowMarkRecorder struct {
	*flakyBank
	openedWith string
}

func (b *escrowMarkRecorder) OpenGame(guildID, userID, game, sessionID string, bet int64, now time.Time) error {
	if err := b.flakyBank.OpenGame(guildID, userID, game, sessionID, bet, now); err != nil {
		return err
	}
	b.openedWith = sessionID
	return nil
}

// The board a command publishes must be the board its chips were staked for,
// in all three games. While the mark was handed over in a second step the two
// could differ, and a settlement arriving in that gap named an ID the account
// had never heard of: refused, and the stake left behind with no board to
// return it.
func TestCasinoOpenStakesTheChipsForTheBoardItPublishes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start func(t *testing.T, bank casinoBank, mgr *casino.SessionManager) string
	}{
		{
			name: "highlow",
			start: func(t *testing.T, bank casinoBank, mgr *casino.SessionManager) string {
				c := &HighLowCommand{store: bank, sessions: mgr, newGame: func(bet int64) *casino.HighLowGame { return casino.NewHighLow(bet, zeroRng{}) }}
				return startHighLow(t, c, &fakeHighLowResponder{}, 100)
			},
		},
		{
			name: "blackjack",
			start: func(t *testing.T, bank casinoBank, mgr *casino.SessionManager) string {
				c := &BlackjackCommand{store: bank, sessions: mgr, newGame: func(bet int64) *casino.BlackjackGame { return casino.NewBlackjack(bet, blackjackRngFor(1)) }}
				return startBlackjack(t, c, &fakeCasinoResponder{}, 100)
			},
		},
		{
			name: "duel",
			start: func(t *testing.T, bank casinoBank, mgr *casino.SessionManager) string {
				c := &DuelCommand{store: bank, sessions: mgr, flip: func() bool { return true }}
				return startDuel(t, c, &fakeCasinoResponder{}, 100)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bank := &escrowMarkRecorder{flakyBank: &flakyBank{Store: casino.New(filepath.Join(t.TempDir(), "casino.json"))}}
			mgr := casino.NewSessionManager(time.Now, casino.DefaultSessionTTL)

			sessionID := tc.start(t, bank, mgr)
			if bank.openedWith != sessionID {
				t.Fatalf("the chips were staked for %q but the board went live as %q — for that gap the stake belongs to nobody", bank.openedWith, sessionID)
			}
			if _, live := mgr.Get(sessionID); !live {
				t.Fatalf("board %q is not live after the start", sessionID)
			}
		})
	}
}

// A sweep that lands the instant a board goes live — before anything else has
// touched the stake — must still return the chips. This is the shape C3B-13
// was sent back for: with the mark handed over in a second step, a board
// swept in that gap settled against a mark no board could present, the
// settlement was refused, the board was dropped, and the stake sat in escrow
// until the next restart.
func TestSweepingABoardTheInstantItOpensReturnsTheStake(t *testing.T) {
	for _, tc := range []struct {
		name string
		game casino.GameKind
		// wantChips is what the player holds once the sweep has settled a
		// board nobody ever pressed: the duel withdraws the stake whole, the
		// other two auto-resolve a hand that was dealt but never played.
		wantChips func(t *testing.T, chips int64)
	}{
		{
			name: "highlow",
			game: casino.GameHighLow,
			wantChips: func(t *testing.T, chips int64) {
				if chips != 1000 {
					t.Errorf("chips = %d, want 1000 — cashing out an untouched high&low board returns the stake", chips)
				}
			},
		},
		{
			name: "blackjack",
			game: casino.GameBlackjack,
			wantChips: func(t *testing.T, chips int64) {
				if chips < 900 {
					t.Errorf("chips = %d, want at least 900 — an auto-stood hand cannot lose more than its bet", chips)
				}
			},
		},
		{
			name: "duel",
			game: casino.GameDuel,
			wantChips: func(t *testing.T, chips int64) {
				if chips != 1000 {
					t.Errorf("chips = %d, want 1000 — a timed-out challenge is withdrawn whole", chips)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetComponentsForTest()
			t.Cleanup(resetComponentsForTest)

			bank, mgr, sessionID, opened := openBoardTheCommandWay(t, tc.game, 100)

			// No press, no hand-over, no second call of any kind: straight
			// from the open to the sweeper.
			sweepIdleBoards(newRecordingEditor(), mgr, bank, opened.Add(casino.DefaultSessionTTL))

			chips, escrow := sweepChipsOf(t, bank)
			if escrow != 0 {
				t.Errorf("escrow = %d after the sweep, want 0 — the stake was left behind with no board to return it", escrow)
			}
			tc.wantChips(t, chips)
			if mgr.Len() != 0 {
				t.Errorf("%d boards survived the sweep, want 0", mgr.Len())
			}
			if _, live := mgr.Get(sessionID); live {
				t.Errorf("board %q is still addressable after its settlement", sessionID)
			}
		})
	}
}

// openBoardTheCommandWay runs a real start path onto a frozen-clock manager,
// so the test can sweep the board it produced at an exact instant. It returns
// the bank the command staked on, the manager, the live board's ID, and the
// instant the board was opened.
func openBoardTheCommandWay(t *testing.T, game casino.GameKind, bet int64) (*flakyBank, *casino.SessionManager, string, time.Time) {
	t.Helper()

	bank := &flakyBank{Store: casino.New(filepath.Join(t.TempDir(), "casino.json"))}
	opened := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	mgr := casino.NewSessionManager(func() time.Time { return opened }, casino.DefaultSessionTTL)

	switch game {
	case casino.GameHighLow:
		c := &HighLowCommand{store: bank, sessions: mgr, newGame: func(b int64) *casino.HighLowGame { return casino.NewHighLow(b, zeroRng{}) }}
		RegisterComponent(c)
		return bank, mgr, startHighLow(t, c, &fakeHighLowResponder{}, bet), opened
	case casino.GameBlackjack:
		c := &BlackjackCommand{store: bank, sessions: mgr, newGame: func(b int64) *casino.BlackjackGame { return casino.NewBlackjack(b, blackjackRngFor(1)) }}
		RegisterComponent(c)
		return bank, mgr, startBlackjack(t, c, &fakeCasinoResponder{}, bet), opened
	case casino.GameDuel:
		c := &DuelCommand{store: bank, sessions: mgr, flip: func() bool { return true }}
		RegisterComponent(c)
		return bank, mgr, startDuel(t, c, &fakeCasinoResponder{}, bet), opened
	}
	t.Fatalf("no start path for %q", game)
	return nil, nil, "", opened
}

// --- the sweeper keeps a board it could not settle (C2-10) -----------------

// A settlement refused with ErrEscrowMismatch is a DISAGREEMENT about who owns
// the chips, not "there is nothing left to pay". Dropping the board on it
// threw away the payout the pass had already resolved AND left the stake in
// escrow, where it blocks every new game until the next restart. The board
// stays expired instead, so the next pass retries it.
func TestSweepKeepsABoardWhoseStakeBelongsToAnotherBoard(t *testing.T) {
	bank := &flakyBank{Store: casino.New(filepath.Join(t.TempDir(), "casino.json"))}
	opened := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	if err := bank.EnsureCasinoAccess("g1", "u1", opened); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	// The account's stake belongs to a board that is not the one below. With
	// the open a single marked write, that state is no longer reachable by
	// playing — which is the point: if it happens anyway, it is a real
	// inconsistency and the sweeper must not paper over it by dropping chips.
	if err := bank.OpenGame("g1", "u1", string(casino.GameHighLow), "some-other-board", 100, opened); err != nil {
		t.Fatalf("OpenGame: %v", err)
	}
	mgr := casino.NewSessionManager(func() time.Time { return opened }, casino.DefaultSessionTTL)
	session, err := mgr.OpenWithID("stranded-board", "g1", "u1", casino.GameHighLow, casino.NewHighLow(100, zeroRng{}), casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
	if err != nil {
		t.Fatalf("opening the board: %v", err)
	}

	logs := captureSlogOutput(t)
	sweepIdleBoards(newRecordingEditor(), mgr, bank, opened.Add(casino.DefaultSessionTTL))

	if mgr.Len() != 1 {
		t.Fatalf("%d boards survived the mismatch, want 1 — dropping it loses the resolved payout and strands the stake", mgr.Len())
	}
	// Keeping the board is silent by itself: nothing in the channel changes
	// and the retry looks like any other pass. The line is the only way an
	// operator learns that an account disagrees with its board about who owns
	// the chips — a state the open can no longer produce (設計書 C-3b), so one
	// that means a real inconsistency.
	assertSweepLoggedTheStakeMismatch(t, logs.String(), "stranded-board")
	if _, escrow := sweepChipsOf(t, bank); escrow != 100 {
		t.Errorf("escrow = %d, want 100 — a refused settlement must not move chips", escrow)
	}
	if bank.settles != 1 {
		t.Errorf("SettleGame was called %d times in the first pass, want 1", bank.settles)
	}

	// The next pass is the retry, and it pays as soon as the account agrees
	// again. The payout it hands over is the one the FIRST pass resolved:
	// AutoResolve draws cards, so a second call would decide another game.
	if _, err := bank.SettleGame("g1", "u1", "some-other-board", 100); err != nil {
		t.Fatalf("clearing the other board's stake: %v", err)
	}
	if err := bank.OpenGame("g1", "u1", string(casino.GameHighLow), session.ID, 100, opened); err != nil {
		t.Fatalf("re-staking for the stranded board: %v", err)
	}
	sweepIdleBoards(newRecordingEditor(), mgr, bank, opened.Add(2*casino.DefaultSessionTTL))

	if mgr.Len() != 0 {
		t.Errorf("%d boards survived the retry, want 0 — the next pass is the retry", mgr.Len())
	}
	if _, escrow := sweepChipsOf(t, bank); escrow != 0 {
		t.Errorf("escrow = %d after the retry, want 0", escrow)
	}
}

// assertSweepLoggedTheStakeMismatch checks that the pass said WHY it kept the
// board. sweepIdleBoards logs at Error level and names the board, so an
// operator reading the file can find the account without reproducing the
// sweep.
func assertSweepLoggedTheStakeMismatch(t *testing.T, logged, sessionID string) {
	t.Helper()
	line := ""
	for _, candidate := range strings.Split(logged, "\n") {
		if strings.Contains(candidate, "belongs to another board") {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Fatalf("the sweep kept the board without logging why; log was:\n%s", logged)
	}
	if !strings.Contains(line, "level=ERROR") {
		t.Errorf("the mismatch was logged below ERROR, so it is lost at the default level: %q", line)
	}
	if !strings.Contains(line, sessionID) {
		t.Errorf("the mismatch line does not name the board %q: %q", sessionID, line)
	}
}

// --- the same mismatch, reached through the duel (反復 7 の所見 1) ----------

// The duel withdraws through casino.DeclineDuel rather than SettleGame, and
// that call used to answer ErrNoGameInProgress — "there is nothing left to
// pay", which the sweeper drops the board on — whenever the escrow named
// ANOTHER GAME, even though the account was still holding 100 chips for it.
// Structurally that is the same situation as a stake marked for another duel
// board: somebody else owns the chips. Dropping the challenge on it threw
// away the withdrawal and left the stake behind, which is exactly what C2-10
// forbids.
func TestSweepKeepsADuelWhoseChallengerIsStakedForAnotherGame(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)

	bank := &flakyBank{Store: casino.New(filepath.Join(t.TempDir(), "casino.json"))}
	opened := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	if err := bank.EnsureCasinoAccess("g1", "u1", opened); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	// The challenger's chips are staked for a HIGH&LOW board. The duel below
	// may not take them back: that board is still live and will settle them.
	if err := bank.OpenGame("g1", "u1", string(casino.GameHighLow), "some-other-board", 100, opened); err != nil {
		t.Fatalf("OpenGame: %v", err)
	}

	mgr := casino.NewSessionManager(func() time.Time { return opened }, casino.DefaultSessionTTL)
	RegisterComponent(&DuelCommand{store: bank, sessions: mgr, flip: func() bool { return true }})
	if _, err := mgr.OpenWithID("stranded-duel", "g1", "u1", casino.GameDuel,
		&casino.DuelState{ChallengerID: "u1", OpponentID: "u2", Bet: 100, Stage: casino.DuelPending},
		casino.MessageRef{ChannelID: "c1", MessageID: "m1"}); err != nil {
		t.Fatalf("opening the challenge: %v", err)
	}

	logs := captureSlogOutput(t)
	sweepIdleBoards(newRecordingEditor(), mgr, bank, opened.Add(casino.DefaultSessionTTL))

	if mgr.Len() != 1 {
		t.Fatalf("%d challenges survived the mismatch, want 1 — dropping it strands the stake it could not return", mgr.Len())
	}
	chips, escrow := sweepChipsOf(t, bank)
	if escrow != 100 {
		t.Errorf("escrow = %d, want 100 — the other board's stake must not move", escrow)
	}
	if chips != 900 {
		t.Errorf("chips = %d, want 900 — a refused withdrawal must not refund anything", chips)
	}
	assertSweepLoggedTheStakeMismatch(t, logs.String(), "stranded-duel")

	// The state clears itself: once the board that owns those chips settles,
	// the account holds nothing and the next pass drops the challenge.
	if _, err := bank.SettleGame("g1", "u1", "some-other-board", 100); err != nil {
		t.Fatalf("clearing the other board's stake: %v", err)
	}
	sweepIdleBoards(newRecordingEditor(), mgr, bank, opened.Add(2*casino.DefaultSessionTTL))

	if mgr.Len() != 0 {
		t.Errorf("%d challenges survived the retry, want 0 — an empty escrow is nothing left to return", mgr.Len())
	}
}
