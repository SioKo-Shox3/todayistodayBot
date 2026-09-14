package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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

// casinoTimeoutNotice is the line that says WHY this board ended without the
// player pressing anything. It sits above the game's own result rendering:
// the result answers "what happened to my chips", this answers "why now".
const casinoTimeoutNotice = "操作がないまま時間切れになったので自動で決着しました。"

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
// 消す → edit, in that order. Sweep has marked each board expired and handed
// it to this goroutine alone — every press path refuses an expired board — so
// the state below may be read and resolved without the manager's lock, and a
// button pressed a moment too late gets ErrSessionNotFound instead of a
// second settlement.
//
// The board is removed only AFTER the settlement has landed. A settlement
// that fails leaves the board expired, carrying the payout it already
// resolved, and the next pass (CasinoSweepInterval later) pays that same
// number: dropping the board here would lose the payout AND leave the stake
// in escrow, where it blocks every new game until the next restart.
//
// One failing board never stops the pass.
func sweepIdleBoards(editor boardEditor, mgr *casino.SessionManager, bank casinoBank, now time.Time) {
	for _, session := range mgr.Sweep(now) {
		settled, err := settleSweptBoard(bank, session)
		switch {
		case errors.Is(err, errBoardCannotResolve):
			// Unreachable while every registered game either resolves itself
			// or settles itself. Nothing can decide what this board owes, so
			// retrying it forever would only keep its owner locked out of new
			// games: drop it and leave the escrow to RefundStaleEscrows at the
			// next restart. That is worth a loud line.
			slog.Error("casino: a swept board cannot resolve itself", "game", string(session.Game), "state", fmt.Sprintf("%T", session.State))
			mgr.Remove(session.ID)
			continue
		case errors.Is(err, casino.ErrNoGameInProgress):
			// The escrow is already closed: a press got there first, or an
			// earlier retry paid and failed on something after the payout.
			// There is nothing left to pay, so stop retrying this board.
			slog.Warn("casino: a timed-out board had no stake left to settle", "game", string(session.Game))
			mgr.Remove(session.ID)
			continue
		case err != nil:
			slog.Error("casino: settling a timed-out board failed, retrying at the next sweep", "game", string(session.Game), "error", redactInteractionError(err))
			continue
		}
		mgr.Remove(session.ID)
		editTimedOutBoard(editor, session, settled)
	}
}

// errBoardCannotResolve marks a swept board that neither resolves nor settles
// itself, so the loop above can tell it apart from a settlement the store
// refused — the first is a programming error to drop, the second is worth
// retrying at the next pass.
var errBoardCannotResolve = errors.New("commands: a swept board can neither resolve nor settle itself")

// timedOutBoardSettler is the third optional half of ComponentHandler: a game
// whose expired board is NOT closed by AutoResolve + Store.SettleGame.
//
// The duel is the one that needs it (設計書 C-3b §4.5). Its timeout is a
// WITHDRAWAL, not a payout: the challenger's stake goes back whole through
// casino.DeclineDuel, which unlike SettleGame cannot cap the refund away, and
// the opponent — who staked nothing on a challenge they never accepted — must
// not be settled at all. Neither of those fits a single "what does this board
// owe its one player" number, which is the whole of AutoResolver's contract.
//
// session is the board Sweep handed to the sweeper's goroutine alone, so the
// implementation may read and write its State without the manager's lock —
// the same licence the AutoResolve path uses.
type timedOutBoardSettler interface {
	SettleTimedOutBoard(session *casino.Session) (casino.SettleResult, error)
}

// settleSweptBoard turns an expired board into chips that have actually
// moved: the game's own closing transaction when it has one, and otherwise
// 設計書 §5's AutoResolve → SettleGame.
//
// Chips before pixels: the caller announces nothing until this has returned a
// persisted settlement, and an edit that fails is never retried into a second
// payout (the same order settle() keeps for a press).
func settleSweptBoard(bank casinoBank, session *casino.Session) (casino.SettleResult, error) {
	if handler, found := registeredComponents[string(session.Game)]; found {
		if settler, settles := handler.(timedOutBoardSettler); settles {
			return settler.SettleTimedOutBoard(session)
		}
	}

	if !session.PayoutResolved {
		resolver, resolvable := session.State.(casino.AutoResolver)
		if !resolvable {
			return casino.SettleResult{}, errBoardCannotResolve
		}
		// Once per board, never on a retry: AutoResolve draws cards, so a
		// second call would decide a different game than the one the first
		// pass already committed to.
		session.PendingPayout = resolver.AutoResolve()
		session.PayoutResolved = true
	}
	return bank.SettleGame(session.GuildID, session.UserID, session.PendingPayout)
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
	edit.SetEmbeds([]*discordgo.MessageEmbed{timedOutBoardEmbed(session, settled)})
	// 設計書 §4/§8: a settled board keeps its buttons, greyed out. The labels
	// are the GAME's (they carry its hand and its odds), so the sweeper asks
	// the game that registered this custom_id prefix to redraw them disabled
	// instead of learning every game's rendering itself.
	components := disabledComponentsFor(session)
	edit.Components = &components

	if _, err := editor.ChannelMessageEditComplex(edit); err != nil {
		slog.Error("discord: editing a timed-out board failed", "game", string(session.Game), "error", redactInteractionError(err))
	}
}

// timedOutBoardRenderer is the optional half of ComponentHandler: a game that
// implements it can redraw a swept board's buttons disabled for the closing
// edit. It is optional rather than part of ComponentHandler because the
// sweeper has an answer either way — a game that does not implement it simply
// loses its buttons, which is strictly what this file did before.
//
// state is the swept session's board. Sweep marked it expired and handed it
// to this goroutine alone (and the settlement above has already removed it),
// so reading it here needs no lock — the same licence sweepIdleBoards uses
// for AutoResolve.
type timedOutBoardRenderer interface {
	DisabledComponents(sessionID string, state any) []discordgo.MessageComponent
}

// disabledComponentsFor asks the game that owns session's custom_id prefix
// for its greyed-out buttons. The empty (non-nil) slice is what removes the
// live buttons when no game answers — a nil Components field would leave the
// PLAYABLE row under a board that is over.
func disabledComponentsFor(session *casino.Session) []discordgo.MessageComponent {
	if handler, found := registeredComponents[string(session.Game)]; found {
		if renderer, draws := handler.(timedOutBoardRenderer); draws {
			if components := renderer.DisabledComponents(session.ID, session.State); components != nil {
				return components
			}
		}
	}
	return []discordgo.MessageComponent{}
}

// timedOutBoardResultRenderer is the other optional half of a game's closing
// edit: the body. The sweeper's edit REPLACES the board, so a bare money line
// would leave the player with no record of the hand that was decided without
// them — the dealer's hole card is turned over by AutoResolve itself and, once
// the embed is gone, nowhere else. A game that implements this draws the same
// result it would have drawn for a press; the sweeper adds the ⌛ reason.
type timedOutBoardResultRenderer interface {
	TimedOutEmbed(state any, settled casino.SettleResult) *discordgo.MessageEmbed
}

// timedOutBoardEmbed is the closing message of an auto-resolved board: the
// game's own result rendering when it has one, and the money alone when it
// does not. Like disabledComponentsFor it routes on the session's game, and
// reads the swept state without a lock for the same reason (the board belongs
// to this goroutine, and the settlement above has already removed it).
func timedOutBoardEmbed(session *casino.Session, settled casino.SettleResult) *discordgo.MessageEmbed {
	if handler, found := registeredComponents[string(session.Game)]; found {
		if renderer, draws := handler.(timedOutBoardResultRenderer); draws {
			if embed := renderer.TimedOutEmbed(session.State, settled); embed != nil {
				return embed
			}
		}
	}
	return casinoTimeoutEmbed(settled)
}

// casinoTimeoutEmbed is the fallback closing message: the money the
// settlement actually produced, and no game detail, for a board whose game
// cannot draw its own result.
func casinoTimeoutEmbed(settled casino.SettleResult) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       casinoTimeoutTitle,
		Description: casinoTimeoutNotice + "\n" + casinoPayoutLine(settled.Payout, settled.Chips),
	}
}

// casinoTimedOutEmbed wraps a game's result body in the ⌛ closing message:
// same title for every game, the reason first, the result below it.
func casinoTimedOutEmbed(resultLines []string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       casinoTimeoutTitle,
		Description: strings.Join(append([]string{casinoTimeoutNotice, ""}, resultLines...), "\n"),
	}
}
