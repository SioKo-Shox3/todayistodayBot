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
	highLowSettleFailedMessage  = "❌ 精算に失敗しました。もう一度お試しください。"
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
	store    *casino.Store
	sessions *casino.SessionManager
	// newGame is the deal, injected so tests can hand the board a fixed deck
	// instead of chasing a seed.
	newGame func(bet int64) *casino.HighLowGame
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

func highLowResultEmbed(result highLowResult) *discordgo.MessageEmbed {
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
	lines = append(lines, fmt.Sprintf("配当: %d枚 / 残高: %d枚", result.Payout, result.Balance))
	return &discordgo.MessageEmbed{
		Title:       "🃏 ハイ&ロー — 結果",
		Description: strings.Join(lines, "\n"),
	}
}

// highLowButtons builds the three buttons. A guess no remaining card can win
// is disabled (設計書 §6) — pressing it would only earn an ephemeral
// refusal — and disableAll switches off the whole row for a finished board,
// which is what stops a second press from reaching a settled hand at all.
func highLowButtons(sessionID string, board highLowBoard, disableAll bool) []discordgo.MessageComponent {
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
				Label:    fmt.Sprintf("💰 キャッシュアウト %d枚", board.Pot),
				Style:    discordgo.SuccessButton,
				CustomID: BuildCustomID(string(casino.GameHighLow), sessionID, highLowActionCashOut),
				Disabled: disableAll,
			},
		}},
	}
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

	game := c.newGame(bet)
	board := snapshotHighLow(game) // safe without the lock: nobody can reach this board until Open publishes its ID

	// Session BEFORE escrow (設計書 §5 / C2-05 の決定): refusing a second game
	// after the chips moved would need a refund path, while closing a session
	// that never got its stake costs nothing.
	session, err := c.sessions.Open(i.GuildID, userID, casino.GameHighLow, game, casino.MessageRef{ChannelID: i.ChannelID})
	if err != nil {
		return respondVia(r, i.Interaction, messageResponse(translateHighLowError(err)))
	}
	if err := c.store.OpenGame(i.GuildID, userID, string(casino.GameHighLow), bet, now); err != nil {
		c.sessions.Close(session.ID) // no chips moved yet: nothing to refund
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
		c.sessions.Close(session.ID)
		if _, refundErr := c.store.SettleGame(i.GuildID, userID, bet); refundErr != nil {
			slog.Error("casino: refunding an undelivered high&low board failed", "error", redactInteractionError(refundErr))
		}
		return err
	}

	c.rememberBoardMessage(r, i.Interaction, session.ID)
	return nil
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
	session, ok := c.sessions.Get(sessionID)
	if !ok {
		return respondVia(r, i.Interaction, ephemeralResponse(highLowSessionOverMessage))
	}
	if msg := requireSessionOwner(i, session.UserID); msg != "" {
		return respondVia(r, i.Interaction, ephemeralResponse(msg))
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
		return respondVia(r, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Embeds:     []*discordgo.MessageEmbed{highLowBoardEmbed(board, session.UserID)},
				Components: highLowButtons(sessionID, board, false),
			},
		})
	}

	// Settle FIRST, edit second: the chips are the part that must survive a
	// crash, and the session is already gone (WithSession removed it), so a
	// failed edit cannot be retried into a second payout.
	settled, err := c.store.SettleGame(session.GuildID, session.UserID, payout)
	if err != nil {
		slog.Error("casino: SettleGame failed for high&low", "error", redactInteractionError(err))
		return respondVia(r, i.Interaction, ephemeralResponse(highLowSettleFailedMessage))
	}

	result := highLowResult{
		Ending:  highLowEndingOf(step, guessed, board),
		Guessed: guessed, GuessHigh: action == highLowActionHigh,
		Previous: step.Previous, Drawn: step.Card,
		Bet: board.Bet, Payout: settled.Payout, Streak: board.Streak, Balance: settled.Chips,
	}
	if err := respondVia(r, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{highLowResultEmbed(result)},
			Components: highLowButtons(sessionID, board, true),
		},
	}); err != nil {
		return err
	}

	if celebration := highLowCelebration(session.UserID, result); celebration != "" {
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
