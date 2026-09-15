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
	c := &DuelCommand{
		store:    casino.Default(),
		sessions: casino.DefaultSessions(),
		flip:     tossDuelCoin,
	}
	Register(c)
	RegisterComponent(c) // 設計書 §4: ボタンは main.go ではなくゲーム自身が登録する
}

// Button actions, i.e. the <action> element of casino:duel:<id>:<action>.
const (
	duelActionAccept  = "accept"
	duelActionDecline = "decline"
)

const (
	duelMinBet = 10
	duelMaxBet = 1000
	// duelOpponentOption is the name of the user option. English like every
	// other identifier in this package; the Japanese is in its description.
	duelOpponentOption = "opponent"
	duelBetOption      = "bet"
)

// User-facing wording (設計書 C-3b §4.5 / §6).
const (
	duelBetRangeMessage   = "❌ ベットは10〜1,000チップです"
	duelInProgressMessage = "❌ 進行中のゲームがあります(先に決着してください)"
	// duelOpponentInProgressFormat is the same refusal seen from the other
	// side: the challenger is free, the person they aimed at is not. It names
	// them because "進行中のゲームがあります" alone reads as an accusation
	// against the presser, who has done nothing wrong and cannot settle
	// somebody else's hand — so the "先に決着してください" of the message
	// above is dropped here rather than aimed at the wrong player.
	duelOpponentInProgressFormat = "❌ <@%s> は進行中のゲームがあります"
	duelSelfMessage              = "❌ 自分自身とは対戦できません"
	duelBotMessage               = "❌ Bot とは対戦できません"
	duelNoTargetMessage          = "❌ 対戦相手を指定してください"
	duelNotYoursMessage          = "❌ この挑戦はあなた宛てではありません"
	duelChallengeOverMessage     = "⌛ この挑戦はもう終了しています"
	duelStartFailedMessage       = "❌ 挑戦の開始に失敗しました。"
	duelActionFailedMessage      = "❌ 操作に失敗しました。"
)

// Titles of the three states a challenge message can end in.
const (
	duelChallengeTitle = "⚔️ 決闘の申し込み"
	duelResultTitle    = "⚔️ 決闘 — 結果"
	duelDeclinedTitle  = "🚫 挑戦は断られました"
	// duelTimedOutTitle replaces the sweeper's own casinoTimeoutTitle: an
	// expired challenge is not 自動決着 but 取り下げ — nothing was played and
	// the stake went back (設計書 §4.5).
	duelTimedOutTitle = "⌛ 時間切れ — 挑戦は取り下げられました"
)

// errDuelBadState means the session is carrying something that is not a duel
// board. Unreachable while Prefix() and GameDuel agree; it ends the session
// rather than leaving an unanswerable challenge holding an escrow.
var errDuelBadState = errors.New("commands: session state is not a duel board")

// errDuelNotOpponent carries "somebody else pressed this" out of WithSession.
// It is an error rather than a flag because only an error return keeps the
// manager from refreshing the challenge's deadline; the wording the presser
// sees is requireDuelOpponent's, not this value's.
var errDuelNotOpponent = errors.New("commands: this duel challenge is addressed to somebody else")

// DuelCommand implements /duel and the two buttons its challenge carries.
// One value serves as both Command and ComponentHandler, like the other two
// button games.
type DuelCommand struct {
	store    casinoBank
	sessions *casino.SessionManager
	// flip is the coin toss, injected so tests get a decided duel instead of
	// a seed hunt. true means the challenger wins.
	flip func() bool
	// locks serializes the presses on ONE challenge: ⚔️ and 🚫 are pressed by
	// the same person on the same message, and without this a decline can
	// refund while an acceptance is still paying.
	locks boardLocks
}

// tossDuelCoin is the production toss: one *rand.Rand per challenge, never
// the math/rand top-level functions (internal/casino's reproducibility rule).
// The generator is used only here, by the goroutine handling this press, so
// it never needs the store's lock.
func tossDuelCoin() bool {
	return casino.FlipDuel(rand.New(rand.NewSource(time.Now().UnixNano())))
}

func (c *DuelCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "duel",
		Description: "コイントスで1対1の勝負を挑みます（ベット: 10〜1,000チップ）",
		Contexts:    &guildOnlyContexts,
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionUser, Name: duelOpponentOption, Description: "挑戦する相手", Required: true},
			{Type: discordgo.ApplicationCommandOptionInteger, Name: duelBetOption, Description: "ベット額（10〜1,000チップ）", Required: true, MinValue: floatPtr(duelMinBet), MaxValue: duelMaxBet},
		},
	}
}

func (c *DuelCommand) Prefix() string { return string(casino.GameDuel) }

// --- reading the command's options (pure) ----------------------------------

// duelTarget reads the opponent out of the interaction: the ID from the
// option, the Bot flag from Discord's resolved objects.
//
// Neither value is taken through discordgo's UserValue: that helper panics on
// an option whose type or value shape is unexpected, and a slash command must
// not be able to take the process down (discordgo starts handler goroutines
// without recover). A missing Resolved block reports isBot=false — refusing a
// real player because Discord omitted a field is worse than letting a bot be
// challenged, which costs nothing: the bot never presses, and the challenge
// expires into a full refund three minutes later.
func duelTarget(data discordgo.ApplicationCommandInteractionData) (opponentID string, isBot bool) {
	for _, opt := range data.Options {
		if opt == nil || opt.Name != duelOpponentOption {
			continue
		}
		id, isString := opt.Value.(string)
		if !isString {
			return "", false
		}
		opponentID = id
	}
	if opponentID == "" || data.Resolved == nil {
		return opponentID, false
	}
	if user, resolved := data.Resolved.Users[opponentID]; resolved && user != nil {
		return opponentID, user.Bot
	}
	return opponentID, false
}

// duelBet reads the bet option, 0 when it is absent or not a number (which
// the range check then refuses).
func duelBet(data discordgo.ApplicationCommandInteractionData) int64 {
	for _, opt := range data.Options {
		if opt == nil || opt.Name != duelBetOption {
			continue
		}
		if value, isNumber := opt.Value.(float64); isNumber {
			return int64(value)
		}
	}
	return 0
}

// duelTargetRefusal names the opponents a duel cannot be sent to (設計書
// §4.5), or "" when the pair is playable. Split out from handle so the three
// refusals are testable without a store.
func duelTargetRefusal(challengerID, opponentID string, opponentIsBot bool) string {
	switch {
	case challengerID == "" || opponentID == "":
		return duelNoTargetMessage
	case challengerID == opponentID:
		return duelSelfMessage
	case opponentIsBot:
		return duelBotMessage
	default:
		return ""
	}
}

// requireDuelOpponent is requireSessionOwner for a challenge, whose owner is
// INVERTED: Session.UserID is the challenger, but the only person who may
// press ⚔️/🚫 is the opponent named on the board. The wording differs too
// (設計書 §6), which is why this does not simply call the shared helper.
func requireDuelOpponent(i *discordgo.InteractionCreate, opponentID string) string {
	presser := resolveUserID(i)
	// An empty ID means the interaction carried neither Member nor User, or
	// the board lost its receiver. Comparing "" == "" would hand the buttons
	// to anyone, so treat either empty side as a mismatch.
	if presser == "" || opponentID == "" || presser != opponentID {
		return duelNotYoursMessage
	}
	return ""
}

// --- display (pure) --------------------------------------------------------

// duelChallengeEmbed is the live challenge. board is a copy taken under the
// manager's lock, so every renderer here is a pure function of plain values.
func duelChallengeEmbed(board casino.DuelState) *discordgo.MessageEmbed {
	lines := []string{
		fmt.Sprintf("⚔️ <@%s> への挑戦(%d チップ)", board.OpponentID, board.Bet),
		fmt.Sprintf("挑戦者: <@%s>", board.ChallengerID),
		"",
		fmt.Sprintf("コイントスで決めます。勝者が %d枚 を総取りします。", 2*board.Bet),
	}
	return &discordgo.MessageEmbed{
		Title:       duelChallengeTitle,
		Description: strings.Join(lines, "\n"),
		Footer:      &discordgo.MessageEmbedFooter{Text: duelOpponentFooter(board.OpponentID)},
	}
}

// duelOpponentFooter names who may press, so a bystander sees whose buttons
// these are before earning the ephemeral refusal.
func duelOpponentFooter(opponentID string) string {
	return fmt.Sprintf("受けられるのは %s だけ / 3分で自動的に取り下げ", opponentID)
}

// duelCoinFace is how the toss is shown. 表 is the challenger's side — fixed
// so that the coin and the winner cannot disagree in the same message.
func duelCoinFace(challengerWins bool) string {
	if challengerWins {
		return "表"
	}
	return "裏"
}

// duelResultLines is the whole settled duel: the coin, the winner, and BOTH
// accounts' payout and balance (設計書 §4.5 の表示). The payouts come from
// the settlement rather than from 2 × bet — a winner at the chip cap takes
// less than the pot, and the message must not claim chips the balance beside
// it does not show.
func duelResultLines(board casino.DuelState, settlement casino.DuelSettlement) []string {
	return []string{
		fmt.Sprintf("🪙 コイン: **%s**", duelCoinFace(settlement.ChallengerWins)),
		fmt.Sprintf("🏆 勝者: <@%s>(ベット %d枚)", settlement.WinnerID, board.Bet),
		"",
		fmt.Sprintf("<@%s> %s", board.ChallengerID, casinoPayoutLine(settlement.ChallengerPayout, settlement.ChallengerChips)),
		fmt.Sprintf("<@%s> %s", board.OpponentID, casinoPayoutLine(settlement.OpponentPayout, settlement.OpponentChips)),
	}
}

func duelResultEmbed(board casino.DuelState, settlement casino.DuelSettlement) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       duelResultTitle,
		Description: strings.Join(duelResultLines(board, settlement), "\n"),
	}
}

// duelDeclinedEmbed replaces a challenge the opponent turned down. It names
// the refund because the challenger's stake WAS taken when they challenged —
// without the line, a player who sees their balance dip and recover has to
// work out why on their own.
func duelDeclinedEmbed(board casino.DuelState) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title: duelDeclinedTitle,
		Description: strings.Join([]string{
			fmt.Sprintf("<@%s> は <@%s> の挑戦を断りました。", board.OpponentID, board.ChallengerID),
			fmt.Sprintf("<@%s> へ %d枚 を返金しました。", board.ChallengerID, board.Bet),
		}, "\n"),
	}
}

// duelTimedOutEmbed is the closing message of an expired challenge. Same
// outcome as 🚫 — 設計書 §4.5 treats the three-minute sweep as a withdrawal,
// not as a game — with its own title, because nothing was decided.
func duelTimedOutEmbed(board casino.DuelState) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title: duelTimedOutTitle,
		Description: strings.Join([]string{
			fmt.Sprintf("<@%s> が応じないまま3分が過ぎたので、挑戦を取り下げました。", board.OpponentID),
			fmt.Sprintf("<@%s> へ %d枚 を返金しました。", board.ChallengerID, board.Bet),
		}, "\n"),
	}
}

// duelButtons builds the two buttons. disableAll switches off the whole row
// for a challenge that is over, which is what 設計書 §4 asks for (決着した
// メッセージのボタンは無効化して残す) and what stops a second press from
// reaching a settled challenge at all.
func duelButtons(sessionID string, disableAll bool) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "⚔️ 受ける",
				Style:    discordgo.SuccessButton,
				CustomID: BuildCustomID(string(casino.GameDuel), sessionID, duelActionAccept),
				Disabled: disableAll,
			},
			discordgo.Button{
				Label:    "🚫 断る",
				Style:    discordgo.DangerButton,
				CustomID: BuildCustomID(string(casino.GameDuel), sessionID, duelActionDecline),
				Disabled: disableAll,
			},
		}},
	}
}

// DisabledComponents redraws an expired challenge's buttons greyed out for
// the sweeper's closing edit. state is the board Sweep took out of the
// manager, so reading it here needs no lock.
func (c *DuelCommand) DisabledComponents(sessionID string, state any) []discordgo.MessageComponent {
	if _, isDuel := state.(*casino.DuelState); !isDuel {
		return nil
	}
	return duelButtons(sessionID, true)
}

// TimedOutEmbed draws the body of that same edit. Like DisabledComponents it
// reads a board the sweeper owns alone. The SettleResult is ignored: the
// refund is the stake the board itself is carrying, and there is no payout to
// report.
func (c *DuelCommand) TimedOutEmbed(state any, _ casino.SettleResult) *discordgo.MessageEmbed {
	board, isDuel := state.(*casino.DuelState)
	if !isDuel {
		return nil
	}
	return duelTimedOutEmbed(*board)
}

// SettleTimedOutBoard closes an expired challenge the way 🚫 does: the
// challenger's stake goes back and the opponent — who never staked anything —
// is not touched (設計書 §4.5). It is also why a duel does not implement
// casino.AutoResolver: a withdrawal is not a payout, and routing it through
// Store.SettleGame would cap the refund at MaxChips and could destroy part of
// a stake it is supposed to return whole (see casino.DeclineDuel).
//
// The returned SettleResult carries the refund for the sweeper's fallback
// line only; the ⌛ embed above is drawn from the board itself.
func (c *DuelCommand) SettleTimedOutBoard(session *casino.Session) (casino.SettleResult, error) {
	board, isDuel := session.State.(*casino.DuelState)
	if !isDuel {
		return casino.SettleResult{}, errDuelBadState
	}
	if err := c.store.DeclineDuel(session.GuildID, board.ChallengerID, session.ID); err != nil {
		return casino.SettleResult{}, err
	}
	board.Stage = casino.DuelSettled
	// Owed equals Payout because DeclineDuel returns the stake whole: the
	// withdrawal never goes near the chip cap, so there is nothing to drop.
	return casino.SettleResult{Payout: board.Bet, Owed: board.Bet}, nil
}

// --- error wording ---------------------------------------------------------

// translateDuelError renders the errors /duel can refuse a challenge with.
// Anything else is logged (redacted) and reported generically: a store
// failure must not leak a path or a token into the channel.
func translateDuelError(err error) string {
	var insufficientChips *casino.ErrInsufficientChips
	switch {
	case errors.As(err, &insufficientChips):
		return fmt.Sprintf("❌ チップが足りません(現在: %d枚)", insufficientChips.Balance)
	case errors.Is(err, casino.ErrGameInProgress):
		return duelInProgressMessage
	case errors.Is(err, casino.ErrBetOutOfRange):
		return duelBetRangeMessage
	case errors.Is(err, casino.ErrChipCapExceeded):
		return "❌ チップ残高が上限に達しているため開始できません。"
	default:
		slog.Error("casino: starting a duel failed", "error", redactInteractionError(err))
		return duelStartFailedMessage
	}
}

// translateDuelPressError renders the errors a BUTTON can fail with. A
// challenge that is gone (settled, swept, lost to a restart) is the common
// case and gets the ⌛ wording rather than an error.
func translateDuelPressError(err error) string {
	switch {
	case errors.Is(err, casino.ErrSessionNotFound), errors.Is(err, casino.ErrNoGameInProgress),
		errors.Is(err, casino.ErrDuelStakeMismatch), errors.Is(err, errDuelBadState):
		return duelChallengeOverMessage
	default:
		slog.Error("casino: a duel button failed", "error", redactInteractionError(err))
		return duelActionFailedMessage
	}
}

// translateDuelAcceptError renders the refusals of ⚔️. The first two leave
// the challenge standing (see duelAcceptDropsTheChallenge) and are answered
// privately, so the board in the channel is untouched.
func translateDuelAcceptError(err error) string {
	var insufficientChips *casino.ErrInsufficientChips
	switch {
	case errors.As(err, &insufficientChips):
		return fmt.Sprintf("❌ チップが足りません(現在: %d枚)", insufficientChips.Balance)
	case errors.Is(err, casino.ErrGameInProgress):
		return duelInProgressMessage
	default:
		return translateDuelPressError(err)
	}
}

// duelAcceptDropsTheChallenge reports whether a refused acceptance leaves no
// stake for this challenge to release. Only ErrNoGameInProgress says that:
// the challenger's escrow is empty, or it now belongs to another game, and
// either way this board can neither settle nor refund anything.
//
// Every other refusal keeps the challenge standing, and the default side of
// the test is the important one. An I/O failure inside Store.Update writes
// nothing, so the stake is still in escrow; closing the session on it would
// take away the 🚫 button AND hide the board from the sweeper, stranding the
// chips until the next restart. An opponent who cannot cover the bet — or who
// is in a game of their own — has simply not answered yet, and 設計書 §4.5
// keeps the board alive for them (nobody else can take it anyway).
func duelAcceptDropsTheChallenge(err error) bool {
	return errors.Is(err, casino.ErrNoGameInProgress)
}

// --- /duel -----------------------------------------------------------------

func (c *DuelCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return c.handle(s, i)
}

// handle takes the responder as an interface so the whole challenge path is
// testable; the production caller passes *discordgo.Session.
func (c *DuelCommand) handle(r interactionResponder, i *discordgo.InteractionCreate) error {
	if msg := requireGuildContext(i); msg != "" {
		return respondVia(r, i.Interaction, messageResponse(msg))
	}

	data := i.ApplicationCommandData()
	bet := duelBet(data)
	if bet < duelMinBet || bet > duelMaxBet { // defense in depth — MinValue/MaxValue only bind the client
		return respondVia(r, i.Interaction, messageResponse(duelBetRangeMessage))
	}

	challengerID := resolveUserID(i)
	opponentID, opponentIsBot := duelTarget(data)
	if msg := duelTargetRefusal(challengerID, opponentID, opponentIsBot); msg != "" {
		return respondVia(r, i.Interaction, messageResponse(msg))
	}

	// 設計書 §4.5 refuses a duel if EITHER side is mid-game, and only one of
	// the two is checked by staking: OpenGame below sees the challenger's own
	// escrow, never the opponent's. Without this read the challenge goes up,
	// the challenger's chips go into escrow, and the opponent's ⚔️ is refused
	// by AcceptDuel — leaving the stake locked for the full three minutes over
	// a game that could never have started. The acceptance keeps its own
	// check, which is the one that actually guarantees the rule: the opponent
	// can open a game in the three minutes this read cannot see into.
	//
	// An opponent with no account at all is not in a game, and the read does
	// not open one for them (casino.GameInProgress).
	opponentBusy, err := c.store.GameInProgress(i.GuildID, opponentID)
	if err != nil {
		slog.Error("casino: reading the opponent's escrow failed", "command", "duel", "error", redactInteractionError(err))
		return respondVia(r, i.Interaction, messageResponse(duelStartFailedMessage))
	}
	if opponentBusy {
		return respondVia(r, i.Interaction, messageResponse(fmt.Sprintf(duelOpponentInProgressFormat, opponentID)))
	}

	now := time.Now()
	if err := c.store.EnsureCasinoAccess(i.GuildID, challengerID, now); err != nil { // §3.8
		slog.Error("casino: EnsureCasinoAccess failed", "command", "duel", "error", redactInteractionError(err))
		return respondVia(r, i.Interaction, messageResponse("❌ カジノの初期化に失敗しました。"))
	}

	// Stake the challenger BEFORE the board, the order C2-06 settled: the
	// persisted half of "one game per person" is the half that survives a
	// restart, so it is the one that must refuse a second bet.
	// The stake is marked casino.EscrowOpening until the challenge board
	// exists (設計書 C-3b): two challenges of the same size are otherwise
	// indistinguishable, so an unmarked stake is one another acceptance could
	// spend.
	if err := c.store.OpenGame(i.GuildID, challengerID, string(casino.GameDuel), casino.EscrowOpening, bet, now); err != nil {
		return respondVia(r, i.Interaction, messageResponse(translateDuelError(err)))
	}

	state := &casino.DuelState{ChallengerID: challengerID, OpponentID: opponentID, Bet: bet, Stage: casino.DuelPending}
	board := *state // safe without the lock: nobody can reach this board until Open publishes its ID

	session, err := c.sessions.Open(i.GuildID, challengerID, casino.GameDuel, state, casino.MessageRef{ChannelID: i.ChannelID})
	if err != nil {
		c.refundUndeliveredChallenge(i.GuildID, challengerID, casino.EscrowOpening)
		return respondVia(r, i.Interaction, messageResponse(translateDuelError(err)))
	}

	// The challenge owns the stake from here: 🎲 and 🚫, the sweeper and this
	// function's own refund all name this board, and an acceptance that names
	// a challenge already withdrawn is refused instead of paid out of it.
	if err := c.store.BindEscrowSession(i.GuildID, challengerID, session.ID); err != nil {
		if c.sessions.Close(session.ID) {
			c.refundUndeliveredChallenge(i.GuildID, challengerID, casino.EscrowOpening)
		}
		return respondVia(r, i.Interaction, messageResponse(translateDuelError(err)))
	}

	if err := respondVia(r, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{duelChallengeEmbed(board)},
			Components: duelButtons(session.ID, false),
		},
	}); err != nil {
		c.withdrawUndeliveredChallenge(i.GuildID, challengerID, session.ID)
		return err
	}

	c.rememberChallengeMessage(r, i.Interaction, session.ID)
	return nil
}

// withdrawUndeliveredChallenge takes back a challenge whose board reached the
// manager but never reached Discord: there is nothing to press, so nothing
// will ever settle it, and the stake would otherwise sit "in a game" until
// the next restart.
//
// It runs where ⚔️ and 🚫 run — inside the SAME per-board lock, under the
// manager's own Hold — because it is a third presser of a board those two can
// be on right now: Discord can create the message and still fail the call
// that sent it. A Hold that fails means the challenge is already somebody
// else's (accepted, declined, swept), and this path must not hand back a
// stake that has already been settled.
//
// The refund goes FIRST and the board is closed only once it has landed.
// Closing first hides the board from the sweeper, so a refund that failed
// after it would strand the stake with no button and no retry left; keeping
// the board is what makes the next sweep (three minutes on) the retry, the
// same order the sweeper itself keeps for a settlement (C2-10).
func (c *DuelCommand) withdrawUndeliveredChallenge(guildID, challengerID, sessionID string) {
	lock := c.locks.acquire(sessionID)
	defer c.locks.release(sessionID, lock)

	if _, held := c.sessions.Hold(sessionID); !held {
		return
	}
	defer c.sessions.Release(sessionID)

	if err := c.store.DeclineDuel(guildID, challengerID, sessionID); err != nil {
		slog.Error("casino: refunding an undelivered duel challenge failed, the sweeper retries it", "error", redactInteractionError(err))
		return
	}
	c.sessions.Close(sessionID)
}

// refundUndeliveredChallenge returns a stake whose challenge never became
// pressable. It goes through DeclineDuel rather than SettleGame for the
// reason SettleTimedOutBoard gives, and that call's EscrowGame check is what
// stops it from refunding some other game's stake if one has opened since —
// and escrowID, the mark the stake carries at this point of the open, is what
// stops it when that other game is another DUEL (設計書 C-3b).
func (c *DuelCommand) refundUndeliveredChallenge(guildID, challengerID, escrowID string) {
	if err := c.store.DeclineDuel(guildID, challengerID, escrowID); err != nil {
		slog.Error("casino: refunding an undelivered duel challenge failed", "error", redactInteractionError(err))
	}
}

// rememberChallengeMessage fills in the session's MessageRef once Discord has
// created the message — the ID cannot be known at Open time, and the idle
// sweeper needs it to edit the challenge with the bot token after the
// interaction token expires. A failure only costs the sweeper its edit.
func (c *DuelCommand) rememberChallengeMessage(r interactionResponder, interaction *discordgo.Interaction, sessionID string) {
	msg, err := r.InteractionResponse(interaction)
	if err != nil {
		slog.Error("discord: InteractionResponse lookup failed for a duel challenge", "error", redactInteractionError(err))
		return
	}
	if err := c.sessions.WithSession(sessionID, func(session *casino.Session) (bool, error) {
		session.Ref = casino.MessageRef{ChannelID: msg.ChannelID, MessageID: msg.ID}
		return false, nil
	}); err != nil {
		slog.Error("casino: recording the duel challenge message failed", "error", redactInteractionError(err))
	}
}

// --- buttons ---------------------------------------------------------------

func (c *DuelCommand) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate, sessionID, action string) error {
	return c.handleComponent(s, i, sessionID, action)
}

func (c *DuelCommand) handleComponent(r interactionResponder, i *discordgo.InteractionCreate, sessionID, action string) error {
	// One press at a time on THIS challenge, from the settlement to the
	// reply. ⚔️ and 🚫 are two buttons on one message, and without this a
	// decline can refund while an acceptance is still paying.
	lock := c.locks.acquire(sessionID)
	defer c.locks.release(sessionID, lock)

	// Hold, not Get: the board lock above serializes PRESSES, but the idle
	// sweeper does not take it. Holding the challenge under the manager's own
	// lock — the lock Sweep's deadline test takes — keeps a sweep from
	// withdrawing a challenge this press is still settling.
	session, held := c.sessions.Hold(sessionID)
	if !held {
		return respondVia(r, i.Interaction, ephemeralResponse(duelChallengeOverMessage))
	}
	defer c.sessions.Release(sessionID)

	var board casino.DuelState
	var refusal string
	if err := c.sessions.WithSession(sessionID, func(live *casino.Session) (bool, error) {
		state, isDuel := live.State.(*casino.DuelState)
		if !isDuel {
			return true, errDuelBadState
		}
		if state.Stage != casino.DuelPending {
			return false, casino.ErrNoGameInProgress
		}
		// The owner of a challenge is the RECEIVER, not Session.UserID. The
		// test runs INSIDE the closure, and a refusal leaves through the
		// error return, because WithSession refreshes LastActionAt on every
		// call that does not fail: a bystander pressing either button would
		// otherwise push the 3-minute deadline back indefinitely and hold off
		// the refund the challenger is waiting for.
		if msg := requireDuelOpponent(i, state.OpponentID); msg != "" {
			refusal = msg
			return false, errDuelNotOpponent
		}
		board = *state
		return false, nil
	}); err != nil {
		if errors.Is(err, errDuelNotOpponent) {
			return respondVia(r, i.Interaction, ephemeralResponse(refusal))
		}
		return respondVia(r, i.Interaction, ephemeralResponse(translateDuelPressError(err)))
	}

	switch action {
	case duelActionAccept:
		return c.accept(r, i, sessionID, session.GuildID, board)
	case duelActionDecline:
		return c.decline(r, i, sessionID, session.GuildID, board)
	default:
		return respondVia(r, i.Interaction, ephemeralResponse(unknownComponentMessage))
	}
}

// accept stakes the opponent, tosses the coin and settles both sides in one
// store transaction, THEN shows the result: chips before pixels, the order
// every board in this package keeps.
func (c *DuelCommand) accept(r interactionResponder, i *discordgo.InteractionCreate, sessionID, guildID string, board casino.DuelState) error {
	settlement, err := c.store.AcceptDuel(guildID, board.ChallengerID, board.OpponentID, sessionID, board.Bet, c.flip())
	if err != nil {
		if duelAcceptDropsTheChallenge(err) {
			// The stake behind this challenge is gone, so the board can never
			// settle: drop it rather than leave a button that only ever fails.
			c.sessions.Close(sessionID)
		}
		return respondVia(r, i.Interaction, ephemeralResponse(translateDuelAcceptError(err)))
	}

	// The challenge is spent. Closing it BEFORE the edit is what stops the
	// sweeper from later "withdrawing" a duel that has already paid.
	c.sessions.Close(sessionID)
	return respondVia(r, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{duelResultEmbed(board, settlement)},
			Components: duelButtons(sessionID, true),
		},
	})
}

// decline returns the challenger's stake and closes the challenge. The
// opponent staked nothing, so nothing of theirs moves.
func (c *DuelCommand) decline(r interactionResponder, i *discordgo.InteractionCreate, sessionID, guildID string, board casino.DuelState) error {
	if err := c.store.DeclineDuel(guildID, board.ChallengerID, sessionID); err != nil {
		// Nothing was refunded, so the challenge stays exactly as it was: a
		// board whose stake is still in escrow must keep a button that can
		// release it (and the sweeper will, three minutes on).
		return respondVia(r, i.Interaction, ephemeralResponse(translateDuelPressError(err)))
	}

	c.sessions.Close(sessionID)
	return respondVia(r, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{duelDeclinedEmbed(board)},
			Components: duelButtons(sessionID, true),
		},
	})
}
