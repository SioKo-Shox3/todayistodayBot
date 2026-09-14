package commands

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

func init() {
	c := &HighLowCommand{
		store:    casino.Default(),
		sessions: casino.DefaultSessions(),
		newGame:  newHighLowGame,
	}
	Register(c)
	RegisterComponent(c) // 設計書 §4: ボタンは main.go ではなくゲーム自身が登録する
}

// Button actions, i.e. the <action> element of casino:highlow:<id>:<action>.
const (
	highLowActionHigh    = "high"
	highLowActionLow     = "low"
	highLowActionCashOut = "cashout"
)

const (
	highLowMinBet = 10
	highLowMaxBet = 1000
	// highLowCelebrationStreak is the streak from which a cash-out is worth
	// telling the channel about (slot.go's jackpot celebration, same shape:
	// a SEPARATE public message, never the board itself).
	highLowCelebrationStreak = 7
	// highLowStreakLimit and highLowPotCapMultiple mirror 設計書 §6's two
	// automatic cash-outs. internal/casino owns the rule and keeps its own
	// unexported copies; these exist only to word the closing message, and
	// highLowEndingOf is their only reader.
	highLowStreakLimit    = 10
	highLowPotCapMultiple = 100
)

// User-facing wording (設計書 §9). The board's own copy lives in the
// renderers below.
const (
	highLowBetRangeMessage      = "❌ ベットは10〜1,000チップです"
	highLowInProgressMessage    = "❌ 進行中のゲームがあります(先に決着してください)"
	highLowSessionOverMessage   = "⌛ この盤面はもう終了しています"
	highLowChoiceBlockedMessage = "❌ その選択肢は残りの山では当たりません"
	highLowStartFailedMessage   = "❌ ハイ&ローの開始に失敗しました。"
	highLowActionFailedMessage  = "❌ 操作に失敗しました。"
)

// errHighLowBadState means the session is carrying something that is not a
// high&low board. Unreachable while Prefix() and GameHighLow agree, but the
// board it points at cannot be played, so the press ends the session rather
// than leaving an unplayable board addressable (and its escrow settleable).
var errHighLowBadState = errors.New("commands: session state is not a high&low board")

// errHighLowUnknownAction is a custom_id whose action element this handler
// does not implement — an old message from a future/older build.
var errHighLowUnknownAction = errors.New("commands: unknown high&low action")

// HighLowCommand implements /highlow and the three buttons its board
// carries. One value serves as both Command and ComponentHandler: the slash
// command opens the session, the buttons drive it.
type HighLowCommand struct {
	store    casinoBank
	sessions *casino.SessionManager
	// newGame is the deal, injected so tests can hand the board a fixed deck
	// instead of chasing a seed.
	newGame func(bet int64) *casino.HighLowGame
	// locks serializes the presses on ONE board (and parks the settlement a
	// finished board still owes). The zero value is ready to use.
	locks boardLocks
}

// newHighLowGame is the production deal: one *rand.Rand per game, never the
// math/rand top-level functions (internal/casino's reproducibility rule).
// The generator is used only here, by the single goroutine handling this
// interaction, so it never needs the store's lock.
func newHighLowGame(bet int64) *casino.HighLowGame {
	return casino.NewHighLow(bet, rand.New(rand.NewSource(time.Now().UnixNano())))
}

func (c *HighLowCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "highlow",
		Description: "ハイ&ローで遊びます（ベット: 10〜1,000チップ）",
		Contexts:    &guildOnlyContexts,
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "bet", Description: "ベット額（10〜1,000チップ）", Required: true, MinValue: floatPtr(highLowMinBet), MaxValue: highLowMaxBet},
		},
	}
}

func (c *HighLowCommand) Prefix() string { return string(casino.GameHighLow) }

// --- display (pure) --------------------------------------------------------

// highLowBoard is a snapshot of a board, taken while the session lock is
// held, so that every renderer below is a pure function of plain values.
// Nothing outside WithSession may read *casino.HighLowGame: Get returns a
// session copy whose State still points at the SHARED board (設計書 §5).
type highLowBoard struct {
	Bet     int64
	Pot     int64
	Streak  int
	Current casino.Card
	Odds    casino.HighLowOdds
}

func snapshotHighLow(game *casino.HighLowGame) highLowBoard {
	return highLowBoard{
		Bet:     game.Bet(),
		Pot:     game.Pot(),
		Streak:  game.Streak(),
		Current: game.Current(),
		Odds:    game.Odds(),
	}
}

// formatMultiplierX100 renders casino's x100 integer multipliers as ×1.55.
// The two decimals are always printed: ×2 and ×2.05 must not look alike in a
// button label the player bets on.
func formatMultiplierX100(x100 int64) string {
	return fmt.Sprintf("×%d.%02d", x100/100, x100%100)
}

// highLowChancePercent is the truncated win chance of a guess. Truncating
// (rather than rounding) keeps the displayed number from ever overstating
// the odds — the same direction as every other rounding in the casino.
func highLowChancePercent(winning, remaining int) int {
	if winning <= 0 || remaining <= 0 {
		return 0
	}
	return winning * 100 / remaining
}

// highLowChoiceLine is one guess's odds as a body line: the number the
// player is actually betting on, in text, because a button label alone is
// truncated on narrow clients.
func highLowChoiceLine(name string, winning, remaining int, multiplier int64) string {
	if multiplier == 0 {
		return fmt.Sprintf("%s: 選べません(残り%d枚に該当なし)", name, remaining)
	}
	return fmt.Sprintf("%s: %d%%(%d/%d枚) %s", name, highLowChancePercent(winning, remaining), winning, remaining, formatMultiplierX100(multiplier))
}

// highLowChoiceLabel is the same information compressed into a button label
// (設計書 §6: 確率と倍率が読めること) — "⬆️ ハイ 61% ×1.55".
func highLowChoiceLabel(name string, winning, remaining int, multiplier int64) string {
	if multiplier == 0 {
		return name + " ―"
	}
	return fmt.Sprintf("%s %d%% %s", name, highLowChancePercent(winning, remaining), formatMultiplierX100(multiplier))
}

// highLowBoardEmbed renders a playable board.
func highLowBoardEmbed(board highLowBoard, userID string) *discordgo.MessageEmbed {
	lines := []string{
		fmt.Sprintf("現在のカード: **%s**", board.Current),
		fmt.Sprintf("ポット: %d枚(ベット %d枚)", board.Pot, board.Bet),
		fmt.Sprintf("連勝: %d", board.Streak),
		"",
		highLowChoiceLine("⬆️ ハイ", board.Odds.HighCards, board.Odds.Remaining, board.Odds.HighMultiplier),
		highLowChoiceLine("⬇️ ロー", board.Odds.LowCards, board.Odds.Remaining, board.Odds.LowMultiplier),
		fmt.Sprintf("💰 キャッシュアウト: %d枚", board.Pot),
	}
	return &discordgo.MessageEmbed{
		Title:       "🃏 ハイ&ロー",
		Description: strings.Join(lines, "\n"),
		Footer:      &discordgo.MessageEmbedFooter{Text: highLowOwnerFooter(userID)},
	}
}

// highLowOwnerFooter names the owner in the board itself, so a bystander can
// see whose buttons these are before pressing one and getting the ephemeral
// refusal.
func highLowOwnerFooter(userID string) string {
	return fmt.Sprintf("プレイヤー: %s / 3分操作がないと自動キャッシュアウト", userID)
}

// highLowEnding says how a board finished, which is what the result embed's
// headline is made of.
type highLowEnding int

const (
	highLowEndLost highLowEnding = iota
	highLowEndCashedOut
	highLowEndStreakCap
	highLowEndPotCap
)

// highLowResult is everything the closing message shows. Like highLowBoard
// it is plain values, so the renderer is testable on its own.
type highLowResult struct {
	Ending    highLowEnding
	Guessed   bool // false for a cash-out, which turns no card over
	GuessHigh bool
	Previous  casino.Card
	Drawn     casino.Card
	Bet       int64
	Payout    int64
	Streak    int
	Balance   int64
}

// highLowResultLines is how the board ended, WITHOUT the money: the card that
// decided it and the headline that names the ending. Split out because two
// callers must not print the money — a settlement that was refused has no
// numbers yet, and the sweeper's closing edit words them itself.
func highLowResultLines(result highLowResult) []string {
	var lines []string
	if result.Guessed {
		guess := "⬇️ ロー"
		if result.GuessHigh {
			guess = "⬆️ ハイ"
		}
		lines = append(lines, fmt.Sprintf("%s(%s) → **%s**", guess, result.Previous, result.Drawn))
	}
	switch result.Ending {
	case highLowEndLost:
		lines = append(lines, fmt.Sprintf("😢 ハズレ(ベット%d枚)", result.Bet))
	case highLowEndStreakCap:
		lines = append(lines, fmt.Sprintf("🏁 %d連勝で自動キャッシュアウト!", result.Streak))
	case highLowEndPotCap:
		lines = append(lines, fmt.Sprintf("🏁 ポットが上限(ベットの%d倍)に達したため自動キャッシュアウト!", highLowPotCapMultiple))
	default:
		lines = append(lines, fmt.Sprintf("💰 キャッシュアウト(%d連勝)", result.Streak))
	}
	return lines
}

func highLowResultEmbed(result highLowResult) *discordgo.MessageEmbed {
	lines := append(highLowResultLines(result), casinoPayoutLine(result.Payout, result.Balance))
	return &discordgo.MessageEmbed{
		Title:       "🃏 ハイ&ロー — 結果",
		Description: strings.Join(lines, "\n"),
	}
}

// highLowPendingEmbed is the finished board shown while its payout has not
// landed. It prints no 配当/残高 line, for the same reason blackjack's does
// not: those are the two numbers Store.SettleGame refused to produce, and the
// zeroes a result embed would show in their place read as "you won nothing"
// to a player whose pot is still owed to them.
func highLowPendingEmbed(result highLowResult) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🃏 ハイ&ロー — 結果",
		Description: strings.Join(highLowResultLines(result), "\n"),
	}
}

// TimedOutEmbed draws the closing message of a board the sweeper auto-cashed
// out (C2-12). AutoResolve IS CashOut, so the ending is the same one the 💰
// button produces; the final card is spelled out because this edit replaces
// the board that was showing it.
//
// state is the swept board, owned by the sweeper's goroutine alone, so
// snapshotting it here needs no lock (the same licence DisabledComponents has).
func (c *HighLowCommand) TimedOutEmbed(state any, settled casino.SettleResult) *discordgo.MessageEmbed {
	game, isHighLow := state.(*casino.HighLowGame)
	if !isHighLow {
		return nil
	}
	board := snapshotHighLow(game)
	lines := append(
		[]string{fmt.Sprintf("最終カード: **%s**", board.Current)},
		highLowResultLines(highLowResult{Ending: highLowEndCashedOut, Bet: board.Bet, Streak: board.Streak})...,
	)
	return casinoTimedOutEmbed(append(lines, casinoPayoutLine(settled.Payout, settled.Chips)))
}

// highLowButtons builds the three buttons. A guess no remaining card can win
// is disabled (設計書 §6) — pressing it would only earn an ephemeral
// refusal — and disableAll switches off the whole row for a finished board,
// which is what stops a second press from reaching a settled hand at all.
func highLowButtons(sessionID string, board highLowBoard, disableAll bool) []discordgo.MessageComponent {
	// The cash-out label names its amount only while the button can still be
	// pressed: live, it is an OFFER — 「押せばこれだけ取れる」 — and the pot is
	// exactly what pressing it pays. On a finished board there is no offer
	// left, and the figure would be the pot rather than what the settlement
	// paid: SettleGame credits only what fits under MaxChips and drops the
	// rest (設計書 §2), so a frozen 「キャッシュアウト 200枚」 can sit next to
	// the body's 「配当: 100枚」. The body is the one that counted the chips,
	// so the button stops competing with it. This covers the sweeper's closing
	// edit too (DisabledComponents), which never sees the settlement at all.
	cashOutLabel := "💰 キャッシュアウト"
	if !disableAll {
		cashOutLabel = fmt.Sprintf("%s %d枚", cashOutLabel, board.Pot)
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    highLowChoiceLabel("⬆️ ハイ", board.Odds.HighCards, board.Odds.Remaining, board.Odds.HighMultiplier),
				Style:    discordgo.PrimaryButton,
				CustomID: BuildCustomID(string(casino.GameHighLow), sessionID, highLowActionHigh),
				Disabled: disableAll || board.Odds.HighMultiplier == 0,
			},
			discordgo.Button{
				Label:    highLowChoiceLabel("⬇️ ロー", board.Odds.LowCards, board.Odds.Remaining, board.Odds.LowMultiplier),
				Style:    discordgo.PrimaryButton,
				CustomID: BuildCustomID(string(casino.GameHighLow), sessionID, highLowActionLow),
				Disabled: disableAll || board.Odds.LowMultiplier == 0,
			},
			discordgo.Button{
				Label:    cashOutLabel,
				Style:    discordgo.SuccessButton,
				CustomID: BuildCustomID(string(casino.GameHighLow), sessionID, highLowActionCashOut),
				Disabled: disableAll,
			},
		}},
	}
}

// DisabledComponents redraws a swept board's buttons greyed out, for the
// sweeper's closing edit (設計書 §4: 決着したメッセージのボタンは無効化して残す).
// state is the board Sweep took out of the manager, so reading it here needs
// no lock.
func (c *HighLowCommand) DisabledComponents(sessionID string, state any) []discordgo.MessageComponent {
	game, isHighLow := state.(*casino.HighLowGame)
	if !isHighLow {
		return nil
	}
	return highLowButtons(sessionID, snapshotHighLow(game), true)
}

// translateHighLowError renders the errors /highlow can refuse a bet with
// (設計書 §9). Anything else is logged (redacted) and reported generically:
// a store failure must not leak a path or a token into the channel.
func translateHighLowError(err error) string {
	var insufficientChips *casino.ErrInsufficientChips
	switch {
	case errors.As(err, &insufficientChips):
		return fmt.Sprintf("❌ チップが足りません(現在: %d枚)", insufficientChips.Balance)
	case errors.Is(err, casino.ErrGameInProgress):
		return highLowInProgressMessage
	case errors.Is(err, casino.ErrBetOutOfRange):
		return highLowBetRangeMessage
	case errors.Is(err, casino.ErrChipCapExceeded):
		return "❌ チップ残高が上限に達しているため開始できません。"
	default:
		slog.Error("casino: starting high&low failed", "error", redactInteractionError(err))
		return highLowStartFailedMessage
	}
}

// translateHighLowPressError renders the errors a BUTTON can fail with. A
// board that is gone (settled, swept, lost to a restart) is the common case
// and gets the ⌛ wording rather than an error.
func translateHighLowPressError(err error) string {
	switch {
	case errors.Is(err, casino.ErrSessionNotFound), errors.Is(err, casino.ErrNoGameInProgress), errors.Is(err, errHighLowBadState):
		return highLowSessionOverMessage
	case errors.Is(err, casino.ErrChoiceUnavailable):
		return highLowChoiceBlockedMessage
	case errors.Is(err, errHighLowUnknownAction):
		return unknownComponentMessage
	default:
		slog.Error("casino: high&low button failed", "error", redactInteractionError(err))
		return highLowActionFailedMessage
	}
}

// --- /highlow --------------------------------------------------------------

func (c *HighLowCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return c.handle(s, i)
}

// handle takes the responder as an interface so the whole start path is
// testable; the production caller passes *discordgo.Session.
func (c *HighLowCommand) handle(r interactionResponder, i *discordgo.InteractionCreate) error {
	if msg := requireGuildContext(i); msg != "" {
		return respondVia(r, i.Interaction, messageResponse(msg))
	}

	var bet int64
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "bet" {
			bet = opt.IntValue()
		}
	}
	if bet < highLowMinBet || bet > highLowMaxBet { // defense in depth — MinValue/MaxValue only bind the client
		return respondVia(r, i.Interaction, messageResponse(highLowBetRangeMessage))
	}

	now, userID := time.Now(), resolveUserID(i)
	if err := c.store.EnsureCasinoAccess(i.GuildID, userID, now); err != nil { // §3.8
		slog.Error("casino: EnsureCasinoAccess failed", "command", "highlow", "error", redactInteractionError(err))
		return respondVia(r, i.Interaction, messageResponse("❌ カジノの初期化に失敗しました。"))
	}

	// Stake BEFORE the board (C2-06 の完了条件: EnsureCasinoAccess → OpenGame
	// → Open). Store.OpenGame is the PERSISTED half of "one game per person",
	// and it is the half that survives a restart, so it is the one that must
	// refuse a second bet. The in-memory manager can then only fail on an
	// exhausted crypto/rand, and that failure refunds.
	if err := c.store.OpenGame(i.GuildID, userID, string(casino.GameHighLow), bet, now); err != nil {
		return respondVia(r, i.Interaction, messageResponse(translateHighLowError(err)))
	}

	game := c.newGame(bet)
	board := snapshotHighLow(game) // safe without the lock: nobody can reach this board until Open publishes its ID

	session, err := c.sessions.Open(i.GuildID, userID, casino.GameHighLow, game, casino.MessageRef{ChannelID: i.ChannelID})
	if err != nil {
		c.refundUnplayableBoard(i.GuildID, userID, bet) // the chips already moved: hand them straight back
		return respondVia(r, i.Interaction, messageResponse(translateHighLowError(err)))
	}

	if err := respondVia(r, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{highLowBoardEmbed(board, userID)},
			Components: highLowButtons(session.ID, board, false),
		},
	}); err != nil {
		// The board never reached Discord, so there is nothing to press and
		// nothing will ever settle it: give the stake back now instead of
		// leaving the account stuck "in a game" until the next restart.
		//
		// ONLY if Close says the session was still ours. Discord can create
		// the message and still fail this call, and the board can then be
		// played and settled while the error is in flight — at which point
		// the escrow on the account belongs to whatever game came next, and
		// refunding "our" bet would pay it out of somebody else's stake.
		if c.sessions.Close(session.ID) {
			c.refundUnplayableBoard(i.GuildID, userID, bet)
		}
		return err
	}

	c.rememberBoardMessage(r, i.Interaction, session.ID)
	return nil
}

// refundUnplayableBoard returns a stake whose board never became playable.
// Failing only means the chips stay in escrow until the next restart returns
// them (RefundStaleEscrows), so it is logged rather than shown to the player,
// who already has a refusal in front of them.
func (c *HighLowCommand) refundUnplayableBoard(guildID, userID string, bet int64) {
	if _, err := c.store.SettleGame(guildID, userID, bet); err != nil {
		slog.Error("casino: refunding an undelivered high&low board failed", "error", redactInteractionError(err))
	}
}

// rememberBoardMessage fills in the session's MessageRef once Discord has
// created the message. The ID cannot be known at Open time (the message is
// the reply to this very interaction), and the idle sweeper (C2-08) needs it
// to edit the board with the bot token after the interaction token expires.
// Writing Ref here is the one exception to "immutable after Open", and it
// happens under the manager's lock like every other field write.
// A failure only costs the sweeper its edit, so it is logged, not surfaced.
func (c *HighLowCommand) rememberBoardMessage(r interactionResponder, interaction *discordgo.Interaction, sessionID string) {
	msg, err := r.InteractionResponse(interaction)
	if err != nil {
		slog.Error("discord: InteractionResponse lookup failed for a high&low board", "error", redactInteractionError(err))
		return
	}
	if err := c.sessions.WithSession(sessionID, func(session *casino.Session) (bool, error) {
		session.Ref = casino.MessageRef{ChannelID: msg.ChannelID, MessageID: msg.ID}
		return false, nil
	}); err != nil {
		slog.Error("casino: recording the high&low board message failed", "error", redactInteractionError(err))
	}
}

// --- buttons ---------------------------------------------------------------

func (c *HighLowCommand) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate, sessionID, action string) error {
	return c.handleComponent(s, i, sessionID, action)
}

func (c *HighLowCommand) handleComponent(r interactionResponder, i *discordgo.InteractionCreate, sessionID, action string) error {
	// One press at a time on THIS board, from the move to the reply. Without
	// it two presses can apply in order and answer out of order, and the
	// slower answer repaints a finished board as a playable one.
	lock := c.locks.acquire(sessionID)
	defer c.locks.release(sessionID, lock)

	// Chips the board already owes outrank any new press: the hand is over,
	// only the payment is missing, so every button on it retries the payment.
	if pending, owed := lock.pending.(*highLowPending); owed {
		if msg := requireSessionOwner(i, pending.UserID); msg != "" {
			return respondVia(r, i.Interaction, ephemeralResponse(msg))
		}
		return c.settle(r, i, sessionID, lock, pending)
	}

	// Hold, not Get: the board lock above serializes PRESSES, but the idle
	// sweeper does not take it, and LastActionAt is only refreshed when
	// WithSession returns. Holding the board under the manager's own lock —
	// the lock Sweep's deadline test takes — keeps a sweep from settling a
	// board this press is still working on (反復 1 の指摘 2).
	session, ok := c.sessions.Hold(sessionID)
	if !ok {
		return respondVia(r, i.Interaction, ephemeralResponse(highLowSessionOverMessage))
	}
	defer c.sessions.Release(sessionID)

	if msg := requireSessionOwner(i, session.UserID); msg != "" {
		return respondVia(r, i.Interaction, ephemeralResponse(msg))
	}

	// The board's last edit never reached Discord, so the message shows the
	// PREVIOUS card while the game holds the next one. A guess made against
	// that picture would be decided by odds the player never saw: repair the
	// picture and take no move.
	if session.NeedsRedraw {
		return c.redrawStaleBoard(r, i, sessionID, session.UserID)
	}

	var (
		board    highLowBoard
		step     casino.StepResult
		guessed  bool
		finished bool
		payout   int64
	)
	// Everything inside this closure runs under the manager's lock: no
	// Discord call, no disk I/O, no re-entry into the manager (危険地帯).
	err := c.sessions.WithSession(sessionID, func(live *casino.Session) (bool, error) {
		game, isHighLow := live.State.(*casino.HighLowGame)
		if !isHighLow {
			return true, errHighLowBadState
		}
		switch action {
		case highLowActionCashOut:
			payout = game.CashOut()
			board = snapshotHighLow(game)
			finished = true
			return true, nil
		case highLowActionHigh, highLowActionLow:
			result, guessErr := game.Guess(action == highLowActionHigh)
			if guessErr != nil {
				return false, guessErr // a refused move leaves the board playable
			}
			step, guessed = result, true
			board = snapshotHighLow(game)
			finished, payout = result.Finished, result.Payout
			return result.Finished, nil
		default:
			return false, errHighLowUnknownAction
		}
	})
	if err != nil {
		return respondVia(r, i.Interaction, ephemeralResponse(translateHighLowPressError(err)))
	}

	if !finished {
		if err := respondVia(r, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Embeds:     []*discordgo.MessageEmbed{highLowBoardEmbed(board, session.UserID)},
				Components: highLowButtons(sessionID, board, false),
			},
		}); err != nil {
			// The card turned over but the message did not: the board in the
			// channel is now a card behind the game. Flag it so the next
			// press repairs the picture instead of guessing on a dead one.
			c.sessions.SetNeedsRedraw(sessionID, true)
			return err
		}
		return nil
	}

	// The board is gone from the manager, so what it owes now lives here.
	// Recording it BEFORE the store call is the whole point: a refused
	// payout must leave something for the next press to retry.
	pending := &highLowPending{
		GuildID: session.GuildID, UserID: session.UserID, Payout: payout, Board: board,
		Result: highLowResult{
			Ending:  highLowEndingOf(step, guessed, board),
			Guessed: guessed, GuessHigh: action == highLowActionHigh,
			Previous: step.Previous, Drawn: step.Card,
			Bet: board.Bet, Streak: board.Streak,
		},
	}
	lock.pending = pending
	return c.settle(r, i, sessionID, lock, pending)
}

// redrawStaleBoard repaints a board whose last edit was lost and answers the
// presser privately. It takes NO game action: the press paid for the repair,
// and the player chooses again from a picture that is true.
//
// The flag comes down only after the redraw has landed — a second lost edit
// leaves the board exactly as stale as it was, and the press after it must
// get the same treatment. The caller holds both the board lock and a Hold.
func (c *HighLowCommand) redrawStaleBoard(r interactionResponder, i *discordgo.InteractionCreate, sessionID, userID string) error {
	var board highLowBoard
	if err := c.sessions.WithSession(sessionID, func(live *casino.Session) (bool, error) {
		game, isHighLow := live.State.(*casino.HighLowGame)
		if !isHighLow {
			return true, errHighLowBadState
		}
		board = snapshotHighLow(game)
		return false, nil
	}); err != nil {
		return respondVia(r, i.Interaction, ephemeralResponse(translateHighLowPressError(err)))
	}

	if err := respondVia(r, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{highLowBoardEmbed(board, userID)},
			Components: highLowButtons(sessionID, board, false),
		},
	}); err != nil {
		return err
	}
	c.sessions.SetNeedsRedraw(sessionID, false)
	notifyRedrawn(r, i)
	return nil
}

// highLowPending is a finished board's unpaid settlement: everything needed
// to pay it and to draw the closing message, minus the two numbers only
// Store.SettleGame can supply (Payout and Balance).
type highLowPending struct {
	GuildID string
	UserID  string
	Payout  int64
	Board   highLowBoard
	Result  highLowResult
}

// settle persists the payout and shows the result, in that order: the chips
// are the part that must survive a crash, and a failed edit must never be
// retried into a second payout.
//
// It is the only writer of lock.pending — cleared the moment the chips land,
// kept (under a 🔁 button) when they do not. The caller holds lock.
func (c *HighLowCommand) settle(r interactionResponder, i *discordgo.InteractionCreate, sessionID string, lock *boardLock, pending *highLowPending) error {
	settled, err := c.store.SettleGame(pending.GuildID, pending.UserID, pending.Payout)
	if err != nil {
		slog.Error("casino: SettleGame failed for high&low", "error", redactInteractionError(err))
		return respondVia(r, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Embeds:     []*discordgo.MessageEmbed{highLowPendingEmbed(pending.Result)},
				Content:    casinoSettleFailedMessage,
				Components: settleRetryButtons(string(casino.GameHighLow), sessionID),
			},
		})
	}
	lock.pending = nil

	result := pending.Result
	result.Payout, result.Balance = settled.Payout, settled.Chips
	if err := respondVia(r, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{highLowResultEmbed(result)},
			Components: highLowButtons(sessionID, pending.Board, true),
		},
	}); err != nil {
		return err
	}

	if celebration := highLowCelebration(pending.UserID, result); celebration != "" {
		if _, sendErr := r.ChannelMessageSend(i.ChannelID, celebration); sendErr != nil {
			slog.Error("discord: ChannelMessageSend failed for a high&low celebration", "error", redactInteractionError(sendErr))
		}
	}
	return nil
}

// highLowCelebration is the public message a long streak earns, or "" when
// the result is not worth telling the channel about. A separate message like
// slot.go's jackpot — the board itself stays the board.
func highLowCelebration(userID string, result highLowResult) string {
	if result.Payout <= 0 || result.Streak < highLowCelebrationStreak {
		return ""
	}
	return fmt.Sprintf("🎉🎉🎉 <@%s> がハイ&ローで %d連勝! %d枚 を持ち帰りました!! 🎉🎉🎉", userID, result.Streak, result.Payout)
}

// highLowEndingOf classifies a finished board. A cash-out the player asked
// for and one the rules forced differ only in the headline, but the player
// deserves to know which one happened.
func highLowEndingOf(step casino.StepResult, guessed bool, board highLowBoard) highLowEnding {
	switch {
	case !guessed:
		return highLowEndCashedOut
	case !step.Won:
		return highLowEndLost
	case board.Streak >= highLowStreakLimit:
		return highLowEndStreakCap
	default:
		return highLowEndPotCap
	}
}
