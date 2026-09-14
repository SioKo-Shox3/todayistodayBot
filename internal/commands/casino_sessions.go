package commands

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

// --- the idle sweeper (設計書 §5) ------------------------------------------
//
// A board is abandoned far more often than it is finished: the player walks
// away with chips in escrow, and nothing on the account says the game is over.
// The sweeper is what closes that hole while the process lives —
// casino.Store.RefundStaleEscrows only closes it across a restart.
//
// Everything here is Discord-facing wiring for casino.SessionManager.Sweep,
// which is itself discordgo-free: the manager decides WHICH boards expired,
// this file decides what happens to them.

// CasinoSweepInterval is how often the sweeper looks for expired boards
// (設計書 §5). It is deliberately much shorter than casino.DefaultSessionTTL:
// the interval is the extra time a timed-out player waits for their chips, so
// it bounds the error of the three-minute promise rather than the promise.
const CasinoSweepInterval = 30 * time.Second

// casinoTimeoutTitle is what an auto-resolved board becomes (設計書 §5).
const casinoTimeoutTitle = "⌛ 時間切れ — 自動決着"

// boardEditor is the Discord surface the sweeper needs. It edits with the BOT
// token rather than an interaction token: the board is swept three minutes
// after the last press, and by the time a later sweep runs the interaction
// token may be gone entirely (they live 15 minutes). *discordgo.Session
// satisfies it; the tests supply a recorder.
type boardEditor interface {
	ChannelMessageEditComplex(m *discordgo.MessageEdit, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

// RunSessionSweeper settles every board that goes idle, until ctx is done.
// It BLOCKS, so cmd/bot/main.go runs it as the goroutine 設計書 §5 asks for,
// under the same context as the 9am announcement scheduler.
//
// This is the only place that supplies the real clock and the real ticker,
// exactly as StartCasinoAnnounceScheduler is for the announcement pass.
func RunSessionSweeper(ctx context.Context, s *discordgo.Session, mgr *casino.SessionManager, store *casino.Store, interval time.Duration) {
	if interval <= 0 {
		interval = CasinoSweepInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	runSessionSweeper(ctx, s, mgr, store, ticker.C, time.Now)
}

// runSessionSweeper is RunSessionSweeper's testable core: the ticks and the
// clock are parameters, so a test drives a sweep exactly when it wants one
// instead of waiting 30 real seconds for a ticker it cannot see.
func runSessionSweeper(ctx context.Context, editor boardEditor, mgr *casino.SessionManager, bank casinoBank, ticks <-chan time.Time, now func() time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			sweepIdleBoards(editor, mgr, bank, now())
		}
	}
}

// sweepIdleBoards runs one pass: 設計書 §5's Sweep → AutoResolve → settle →
// edit, in that order. Sweep has already removed each board from the manager
// and handed it to this goroutine alone, so the state below may be read and
// resolved without the manager's lock, and a button pressed a moment too late
// gets ErrSessionNotFound instead of a second settlement.
//
// One failing board never stops the pass: the others are already out of the
// manager and nothing else will come back for them.
func sweepIdleBoards(editor boardEditor, mgr *casino.SessionManager, bank casinoBank, now time.Time) {
	for _, session := range mgr.Sweep(now) {
		resolver, resolvable := session.State.(casino.AutoResolver)
		if !resolvable {
			// Unreachable while every registered game implements it; the
			// board is already gone, so the escrow it holds now waits for
			// RefundStaleEscrows at the next restart. That is worth a loud line.
			slog.Error("casino: a swept board cannot resolve itself", "game", string(session.Game), "state", fmt.Sprintf("%T", session.State))
			continue
		}

		// Chips before pixels: a settlement that is not persisted must not be
		// announced, and an edit that fails must never be retried into a
		// second payout (the same order settle() keeps for a press).
		settled, err := bank.SettleGame(session.GuildID, session.UserID, resolver.AutoResolve())
		if err != nil {
			slog.Error("casino: settling a timed-out board failed", "game", string(session.Game), "error", redactInteractionError(err))
			continue
		}
		editTimedOutBoard(editor, session, settled)
	}
}

// editTimedOutBoard replaces the board with its closing line. A failure is
// logged and nothing else: the chips have already landed, and the player's
// balance — not this message — is the record of that.
func editTimedOutBoard(editor boardEditor, session *casino.Session, settled casino.SettleResult) {
	if session.Ref.ChannelID == "" || session.Ref.MessageID == "" {
		// The board's message was never recorded (rememberBoardMessage's
		// lookup failed, or the reply never reached Discord). There is
		// nothing to edit; the settlement above is what mattered.
		slog.Warn("casino: a timed-out board has no message to edit", "game", string(session.Game))
		return
	}

	edit := discordgo.NewMessageEdit(session.Ref.ChannelID, session.Ref.MessageID)
	edit.SetEmbeds([]*discordgo.MessageEmbed{casinoTimeoutEmbed(settled)})
	// The buttons go rather than turn grey, which is the one place this
	// departs from 設計書 §4's "disable and keep": those buttons are the
	// GAME's (their labels carry its hand), and the sweeper only knows it
	// swept an AutoResolver. Rebuilding them here would mean teaching this
	// file every game's rendering — the coupling §5 keeps it free of.
	edit.Components = &[]discordgo.MessageComponent{}

	if _, err := editor.ChannelMessageEditComplex(edit); err != nil {
		slog.Error("discord: editing a timed-out board failed", "game", string(session.Game), "error", redactInteractionError(err))
	}
}

// casinoTimeoutEmbed is the closing message of an auto-resolved board. It
// names the money the settlement actually produced, and no game detail: the
// hand or the card row is still above it in the channel.
func casinoTimeoutEmbed(settled casino.SettleResult) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       casinoTimeoutTitle,
		Description: fmt.Sprintf("操作がないまま時間切れになったので自動で決着しました。\n配当: %d枚 / 残高: %d枚", settled.Payout, settled.Chips),
	}
}
