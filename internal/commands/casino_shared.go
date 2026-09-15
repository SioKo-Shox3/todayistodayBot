package commands

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino" // required by rateRankCommentary/marketCommentary below
	"github.com/bwmarrin/discordgo"
)

// guildOnlyContexts restricts a slash command to guild contexts (hidden in
// DMs client-side). Shared Definition() field for all 7 casino commands.
var guildOnlyContexts = []discordgo.InteractionContextType{discordgo.InteractionContextGuild}

// permPtr returns a pointer to p — ApplicationCommand.DefaultMemberPermissions is *int64.
func permPtr(p int64) *int64 { return &p }

// requireGuildContext returns a non-empty ❌ Japanese message if i was
// invoked outside a guild, or "" if the check passes. All 7 casino
// commands call this first, IN ADDITION to Contexts: &guildOnlyContexts in
// Definition() — Discord's client-side Contexts enforcement is not a
// substitute for this server-side check (設計書の二重ガード方針).
func requireGuildContext(i *discordgo.InteractionCreate) string {
	if i.GuildID == "" {
		return "❌ このコマンドはサーバー内で使用してください。"
	}
	return ""
}

// requireAdministrator returns a non-empty ❌ Japanese message (設計書の
// 文言と完全一致させる) if the invoking member lacks Administrator,
// or "" if the check passes. /casino-admin only, in addition to
// DefaultMemberPermissions in Definition().
func requireAdministrator(i *discordgo.InteractionCreate) string {
	if i.Member == nil || i.Member.Permissions&discordgo.PermissionAdministrator == 0 {
		return "❌ このコマンドはサーバー管理者のみ使用できます"
	}
	return ""
}

var sparkChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline renders values (oldest..newest rate integers) as a block-chart
// string (設計書). Each value is bucketed into 8 levels by its
// position within [min(values), max(values)]; a flat series renders the
// lowest bar for every point.
func sparkline(values []int) string {
	if len(values) == 0 {
		return ""
	}
	lo, hi := values[0], values[0]
	for _, v := range values {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	span := hi - lo
	var b strings.Builder
	for _, v := range values {
		idx := 0
		if span > 0 {
			idx = (v - lo) * (len(sparkChars) - 1) / span
		}
		b.WriteRune(sparkChars[idx])
	}
	return b.String()
}

// rateRankCommentary returns a one-line "参謀コメント" (設計書の例:
// 「今日のレートは直近7日で2番目の高値です」) ranking today's rate (the last
// entry of history) among up to the last 7 entries. history must be
// oldest..newest. Disclosed interpretation (設計書 gives an example
// sentence, not an exact algorithm): rank = 1-indexed position when the
// window is sorted descending by Rate.
//
// The empty guard is not decorative: every caller runs inside a discordgo
// handler goroutine, which discordgo starts WITHOUT recover
// (discordgo@v0.29.0/event.go:171), so an index-out-of-range here would
// kill the whole bot process. A doc-comment contract is not a guard.
func rateRankCommentary(history []casino.DailyRate) string {
	if len(history) == 0 {
		return ""
	}
	window := history
	if len(window) > 7 {
		window = window[len(window)-7:]
	}
	today := window[len(window)-1].Rate
	rank := 1
	for _, r := range window {
		if r.Rate > today {
			rank++
		}
	}
	return fmt.Sprintf("今日のレートは直近%d日で%d番目の高値です", len(window), rank)
}

// trendStreak counts how many consecutive trailing entries of history share
// the newest entry's TrendState (>=1 for non-empty history, 0 for empty).
func trendStreak(history []casino.DailyRate) int {
	if len(history) == 0 {
		return 0
	}
	last := history[len(history)-1].Trend
	streak := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Trend != last {
			break
		}
		streak++
	}
	return streak
}

// marketCommentary returns the 相場コメント line required by 設計書 L66
// (掲示embedの3要素目; example: 「コイン強気3日目。天井はどこだ?」).
// history must be oldest..newest; the last entry is today.
//
// It reads the PERSISTED casino.EventKind rather than guessing a surge or
// crash from the day-over-day percentage: the 70/140 clamp can hide a real
// event behind a small visible move (§3.2). An event outranks the trend
// because a surge/crash is the headline of that day. Both "" and
// casino.EventNone mean "no event" ("" can only come from a hand-edited or
// pre-C-1 record).
func marketCommentary(history []casino.DailyRate) string {
	if len(history) == 0 {
		return ""
	}
	today := history[len(history)-1]
	switch today.Event {
	case casino.EventSurge:
		return "🚀 暴騰デー! コインが跳ねた。売り抜けるなら今日だ。"
	case casino.EventCrash:
		return "💥 暴落デー! コインが崩れた。拾いに行くか、様子を見るか。"
	}
	streak := trendStreak(history)
	switch today.Trend {
	case casino.TrendBull:
		return fmt.Sprintf("コイン強気%d日目。天井はどこだ?", streak)
	case casino.TrendBear:
		return fmt.Sprintf("コイン弱気%d日目。底値を狙うなら今か。", streak)
	default:
		return fmt.Sprintf("凪%d日目。動かない相場も相場だ。", streak)
	}
}

// discordTokenInPath matches the secret segment of the two Discord REST
// paths that carry one in the URL itself: /interactions/<id>/<token>/... and
// /webhooks/<id>/<token>/.... The first group keeps the route (and the
// non-secret id) so a redacted line still says WHICH endpoint failed.
var discordTokenInPath = regexp.MustCompile(`(/(?:interactions|webhooks)/[^/\s]+/)[^/\s?]+`)

// redactInteractionError renders err for a log line with the Discord token
// removed. net/http puts the full request URL into *url.Error, so logging a
// failed InteractionRespond verbatim writes the interaction token — and a
// failed webhook call the webhook token — into the log file. The interaction
// token is short-lived and is not the bot token, but it authorises replies to
// that interaction for its lifetime and there is no reason to keep it.
//
// *url.Error is reduced to its Op plus the Go type of its cause; both the URL
// and the cause's own message are dropped, because net/http wraps whatever the
// transport produced and that message can quote the request — including the
// token — in a shape no path pattern matches. The type still says what went
// wrong (*net.OpError, *tls.CertificateVerificationError, …). Anything else
// keeps its message with the token segment of any embedded path replaced by
// [redacted], because errors from other layers (discordgo's own RESTError, a
// wrapped fmt.Errorf) can quote a path too. Every log site in this package
// passes its error through here, so no call site has to decide whether its
// error could contain a URL.
func redactInteractionError(err error) string {
	if err == nil {
		return "<nil>"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Sprintf("%s: %T", urlErr.Op, urlErr.Err)
	}
	return discordTokenInPath.ReplaceAllString(err.Error(), "${1}[redacted]")
}

// respond sends resp for the interaction and returns a token-free error.
// cmd/bot logs whatever Handle returns straight to slog, so returning the raw
// transport error would put the interaction token in the log from every
// command at once. Handlers in this package call this instead of
// s.InteractionRespond; the returned error is a plain message (nothing
// unwraps it) that has already been through redactInteractionError.
func respond(s *discordgo.Session, i *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
	return respondVia(s, i, resp)
}

// interactionResponder is the *discordgo.Session surface the button games
// need. Taking it as an interface is what lets a test observe the ORDER of
// a settlement and the message edit that follows it without a Discord
// connection — the same seam as slot.go's slotResponder, widened because a
// board also has to look its own message up (to remember the message ID for
// the idle sweeper) and post a public celebration.
type interactionResponder interface {
	InteractionRespond(i *discordgo.Interaction, resp *discordgo.InteractionResponse, options ...discordgo.RequestOption) error
	InteractionResponse(i *discordgo.Interaction, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageSend(channelID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error)
	FollowupMessageCreate(i *discordgo.Interaction, wait bool, data *discordgo.WebhookParams, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

// respondVia is respond() for a handler that holds the interface rather than
// the concrete session. It is the single place the games' replies leave the
// package, so the redaction rule holds for them too.
func respondVia(r interactionResponder, i *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
	if err := r.InteractionRespond(i, resp); err != nil {
		return errors.New(redactInteractionError(err))
	}
	return nil
}

// messageResponse wraps content as a plain, publicly visible reply.
func messageResponse(content string) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	}
}

// --- one board at a time (per-board serialization) --------------------------
//
// casino.SessionManager's lock guards EVERY board, so it must never be held
// across a Discord call (危険地帯). That leaves a window a button game cannot
// live with: two presses on the SAME board can apply their moves in order and
// then answer Discord out of order, repainting a finished hand with a playable
// one. boardLocks closes it with one lock PER BOARD, held from the move all
// the way through the reply. Boards are independent, so a slow reply on one
// never blocks another.
//
// The same lock is where a board parks a settlement it still owes. When
// casino.Store.SettleGame refuses the payout (a chip cap, a write failure) the
// session is already gone, and without a record the stake would sit in escrow
// until the next restart and every later press would answer "この盤面はもう
// 終了しています". pending keeps the computed payout so the next press retries
// the PAYMENT ONLY — the hand is never replayed.

// casinoActionSettle is the action of the 🔁 button a board grows while it
// owes a settlement. It takes no game action, so the handler routes it — like
// every other press on such a board — straight into the retry.
const casinoActionSettle = "settle"

// casinoSettleFailedMessage is what the board says while the chips have not
// landed. The 🔁 button under it is the retry.
const casinoSettleFailedMessage = "❌ 精算に失敗しました。もう一度お試しください。"

// casinoPayoutLine is the one line that names money. It exists so that the
// games and the sweeper cannot word it differently — and, more importantly,
// so that "there are no numbers to print yet" is a decision about whether to
// CALL it rather than a pair of zeroes formatted into it. A settlement that
// was refused produced neither number: printing 0/0 tells a player who is
// owed their pot that they were paid nothing.
func casinoPayoutLine(payout, balance int64) string {
	return fmt.Sprintf("配当: %d枚 / 残高: %d枚", payout, balance)
}

// casinoRedrawnMessage answers the press that spent itself repairing a stale
// board (casino.Session.NeedsRedraw). The press took no game action, and the
// picture it was aimed at is gone, so the player is told to choose again
// rather than left wondering why their button did nothing.
const casinoRedrawnMessage = "🔄 盤面を更新しました。もう一度選んでください"

// notifyRedrawn tells the presser, privately, that their press only refreshed
// the board. It is a FOLLOWUP because the interaction's single response was
// just spent on the redraw itself, and the redraw is the half that matters: a
// lost notice costs an explanation, a lost redraw costs the board, so this one
// is logged and never turns the press into a failure.
func notifyRedrawn(r interactionResponder, i *discordgo.InteractionCreate) {
	if _, err := r.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
		Content: casinoRedrawnMessage,
		Flags:   discordgo.MessageFlagsEphemeral,
	}); err != nil {
		slog.Error("discord: FollowupMessageCreate failed for a casino board redraw", "error", redactInteractionError(err))
	}
}

// boardLocks is the process-wide registry of per-board locks. The zero value
// is usable, so a command can hold one as a plain field.
type boardLocks struct {
	mu    sync.Mutex
	locks map[string]*boardLock
}

// boardLock is one board's turnstile plus the settlement it still owes.
// pending is read and written only by the goroutine holding mu.
type boardLock struct {
	mu      sync.Mutex
	waiting int
	pending any
}

// acquire returns the board's lock, ALREADY HELD. Every caller must pair it
// with release(id, lock), normally via defer.
func (b *boardLocks) acquire(id string) *boardLock {
	b.mu.Lock()
	if b.locks == nil {
		b.locks = make(map[string]*boardLock)
	}
	lock, ok := b.locks[id]
	if !ok {
		lock = &boardLock{}
		b.locks[id] = lock
	}
	lock.waiting++
	b.mu.Unlock()

	lock.mu.Lock()
	return lock
}

// release hands the board on and forgets it once nobody else wants it AND it
// owes nothing: an unpaid settlement is the retry, so its record outlives the
// press that created it.
//
// b.mu is taken while lock.mu is still held (that is what makes reading
// pending safe here). acquire takes them the other way round but RELEASES b.mu
// before it blocks on lock.mu, so the two orders cannot deadlock.
func (b *boardLocks) release(id string, lock *boardLock) {
	b.mu.Lock()
	lock.waiting--
	if lock.waiting == 0 && lock.pending == nil {
		delete(b.locks, id)
	}
	b.mu.Unlock()

	lock.mu.Unlock()
}

// settleRetryButtons is the one live button a board keeps while it owes a
// settlement — the only way back to the chips once the session is gone.
func settleRetryButtons(game, sessionID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "🔁 精算をやり直す",
				Style:    discordgo.DangerButton,
				CustomID: BuildCustomID(game, sessionID, casinoActionSettle),
			},
		}},
	}
}

// casinoBank is the casino.Store surface the two button games use. Taking it
// as an interface is the same seam as interactionResponder: a settlement that
// the Store REFUSES (a chip cap, a write that failed) is a state the games
// must handle, and it cannot be reached from the outside by playing the
// economy — the payouts that would breach a cap are not the ones a fixed deck
// deals. *casino.Store satisfies it; only the tests supply anything else.
type casinoBank interface {
	EnsureCasinoAccess(guildID, userID string, now time.Time) error
	// Every call that touches a stake names the BOARD it belongs to
	// (設計書 C-3b): OpenGame marks the chips with the board's ID, and the
	// rest are refused with casino.ErrEscrowMismatch unless the account is
	// holding the stake that board opened. The chips still move BEFORE the
	// board exists (C2-06), but the ID does not — the command layer mints it
	// with casino.NewSessionID first and passes the same one to
	// SessionManager.OpenWithID, so the open is a single marked write rather
	// than a gap some other caller could reach into.
	OpenGame(guildID, userID, game, sessionID string, bet int64, now time.Time) error
	AddToEscrow(guildID, userID, sessionID string, amount int64) error
	SettleGame(guildID, userID, sessionID string, payout int64) (casino.SettleResult, error)
	ViewAccount(guildID, userID string, now time.Time) (casino.AccountView, error)
	// The duel settles two accounts at once and withdraws through its own
	// route: AcceptDuel is the whole game in one transaction, and DeclineDuel
	// is the refund that cannot fail on the chip cap (設計書 C-3b §4.5). They
	// are on this interface rather than on a second one so that /duel keeps
	// the same seam — and the same test doubles — as the other two games.
	AcceptDuel(guildID, challengerID, opponentID, sessionID string, bet int64, challengerWins bool) (casino.DuelSettlement, error)
	DeclineDuel(guildID, challengerID, sessionID string) error
	// GameInProgress is the one READ on this interface, and only /duel needs
	// it: the one-game rule that OpenGame enforces covers the account being
	// debited, never the opponent a challenge is addressed to.
	GameInProgress(guildID, userID string) (bool, error)
}
