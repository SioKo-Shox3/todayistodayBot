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
	c := &BlackjackCommand{
		store:    casino.Default(),
		sessions: casino.DefaultSessions(),
		newGame:  newBlackjackGame,
	}
	Register(c)
	RegisterComponent(c) // 設計書 §4: ボタンは main.go ではなくゲーム自身が登録する
}

// Button actions, i.e. the <action> element of casino:blackjack:<id>:<action>.
const (
	blackjackActionHit    = "hit"
	blackjackActionStand  = "stand"
	blackjackActionDouble = "double"
)

const (
	// 設計書 §8's bet range, the same one /highlow takes. The two games state
	// it separately and a test pins them together: a table that quietly took
	// a different range than the one its wording promises is worse than two
	// constants.
	blackjackMinBet = 10
	blackjackMaxBet = 1000
	// blackjackBustAbove is the total above which a hand is bust. internal/casino
	// owns the rule; this copy exists only so the closing line can name WHICH
	// side busted, and blackjackHeadline is its only reader.
	blackjackBustAbove = 21
	// blackjackHiddenCard is the face-down card. The dealer shows one card
	// and one of these per card still down (設計書 §7).
	blackjackHiddenCard = "🂠"
)

// User-facing wording (設計書 §9).
const (
	blackjackBetRangeMessage     = "❌ ベットは10〜1,000チップです"
	blackjackInProgressMessage   = "❌ 進行中のゲームがあります(先に決着してください)"
	blackjackSessionOverMessage  = "⌛ この盤面はもう終了しています"
	blackjackDoubleOverMessage   = "❌ ダブルは最初の判断でのみ選べます"
	blackjackStartFailedMessage  = "❌ ブラックジャックの開始に失敗しました。"
	blackjackActionFailedMessage = "❌ 操作に失敗しました。"
)

// errBlackjackBadState means the session is carrying something that is not a
// blackjack hand. Unreachable while Prefix() and GameBlackjack agree, but the
// hand it points at cannot be played, so the press ends the session rather
// than leaving an unplayable board addressable (and its escrow settleable).
var errBlackjackBadState = errors.New("commands: session state is not a blackjack hand")

// errBlackjackUnknownAction is a custom_id whose action element this handler
// does not implement — an old message from a future/older build.
var errBlackjackUnknownAction = errors.New("commands: unknown blackjack action")

// BlackjackCommand implements /blackjack and the three buttons its hand
// carries. One value serves as both Command and ComponentHandler, exactly as
// HighLowCommand does.
type BlackjackCommand struct {
	store    casinoBank
	sessions *casino.SessionManager
	// newGame is the deal, injected so tests can hand the hand a fixed shoe
	// instead of chasing a seed.
	newGame func(bet int64) *casino.BlackjackGame
	// locks serializes the presses on ONE hand (and parks the settlement a
	// finished hand still owes). The zero value is ready to use.
	locks boardLocks
}

// newBlackjackGame is the production deal: one *rand.Rand per hand, never the
// math/rand top-level functions (internal/casino's reproducibility rule).
func newBlackjackGame(bet int64) *casino.BlackjackGame {
	return casino.NewBlackjack(bet, rand.New(rand.NewSource(time.Now().UnixNano())))
}

func (c *BlackjackCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "blackjack",
		Description: "ブラックジャックで遊びます（ベット: 10〜1,000チップ）",
		Contexts:    &guildOnlyContexts,
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "bet", Description: "ベット額（10〜1,000チップ）", Required: true, MinValue: floatPtr(blackjackMinBet), MaxValue: blackjackMaxBet},
		},
	}
}

func (c *BlackjackCommand) Prefix() string { return string(casino.GameBlackjack) }

// --- display (pure) --------------------------------------------------------

// blackjackBoard is a snapshot of a hand, taken while the session lock is
// held, so that every renderer below is a pure function of plain values.
// Nothing outside WithSession may read *casino.BlackjackGame: Get returns a
// session copy whose State still points at the SHARED hand (設計書 §5).
type blackjackBoard struct {
	Bet         int64
	TotalBet    int64
	Doubled     bool
	Player      []casino.Card
	PlayerValue int
	Dealer      []casino.Card
	DealerValue int
	CanDouble   bool
	Finished    bool
	Result      casino.BlackjackResult
}

func snapshotBlackjack(game *casino.BlackjackGame) blackjackBoard {
	return blackjackBoard{
		Bet:         game.Bet(),
		TotalBet:    game.TotalBet(),
		Doubled:     game.Doubled(),
		Player:      game.Player(),
		PlayerValue: game.PlayerValue(),
		Dealer:      game.Dealer(),
		DealerValue: game.DealerValue(),
		CanDouble:   game.CanDouble(),
		Finished:    game.State() == casino.BlackjackFinished,
		Result:      game.Result(),
	}
}

// blackjackHand renders a hand face up: "A♠ K♦".
func blackjackHand(cards []casino.Card) string {
	labels := make([]string, 0, len(cards))
	for _, card := range cards {
		labels = append(labels, card.String())
	}
	return strings.Join(labels, " ")
}

// blackjackDealerUpcard renders the dealer's hand as the player may see it
// while the hand is live: the first card, and one 🂠 for every card still
// down. It never shows the hole card's value — the whole game is the bet on
// what that card is.
func blackjackDealerUpcard(cards []casino.Card) string {
	if len(cards) == 0 {
		return blackjackHiddenCard
	}
	shown := []string{cards[0].String()}
	for range cards[1:] {
		shown = append(shown, blackjackHiddenCard)
	}
	return strings.Join(shown, " ")
}

// blackjackBoardEmbed renders a live hand: the player's cards and their
// value, the dealer's upcard and nothing else.
func blackjackBoardEmbed(board blackjackBoard, userID string) *discordgo.MessageEmbed {
	lines := []string{
		fmt.Sprintf("あなたの手: **%s**(%d)", blackjackHand(board.Player), board.PlayerValue),
		fmt.Sprintf("ディーラー: %s", blackjackDealerUpcard(board.Dealer)),
		"",
		blackjackStakeLine(board),
	}
	return &discordgo.MessageEmbed{
		Title:       "🂡 ブラックジャック",
		Description: strings.Join(lines, "\n"),
		Footer:      &discordgo.MessageEmbedFooter{Text: blackjackOwnerFooter(userID)},
	}
}

// blackjackStakeLine says what is on the table, and says so twice once the
// hand is doubled: the player agreed to one number and is now playing for
// another.
func blackjackStakeLine(board blackjackBoard) string {
	if board.Doubled {
		return fmt.Sprintf("ベット: %d枚(ダブルで %d枚)", board.Bet, board.TotalBet)
	}
	return fmt.Sprintf("ベット: %d枚", board.Bet)
}

// blackjackOwnerFooter names the owner in the hand itself, so a bystander can
// see whose buttons these are before pressing one and getting the ephemeral
// refusal.
func blackjackOwnerFooter(userID string) string {
	return fmt.Sprintf("プレイヤー: %s / 3分操作がないと自動スタンド", userID)
}

// blackjackHeadline is the one line that says how the hand ended. A bust is
// named on the side that busted rather than reported as a bare loss: the
// busted total is still in the hand and the player will look for it.
func blackjackHeadline(board blackjackBoard) string {
	switch board.Result {
	case casino.BlackjackNatural:
		return "🃏 ブラックジャック!(3:2)"
	case casino.BlackjackPlayerWin:
		if board.DealerValue > blackjackBustAbove {
			return "🎉 ディーラーがバースト! あなたの勝ち!"
		}
		return "🎉 あなたの勝ち!"
	case casino.BlackjackDealerWin:
		if board.PlayerValue > blackjackBustAbove {
			return "💥 バースト! ディーラーの勝ち"
		}
		return "😢 ディーラーの勝ち"
	case casino.BlackjackPush:
		return "🤝 プッシュ(引き分け)"
	default:
		return "…まだ決着していません"
	}
}

// blackjackResultEmbed renders a finished hand with the dealer's hole card
// turned over. payout/balance are what Store.SettleGame actually paid.
func blackjackResultEmbed(board blackjackBoard, payout, balance int64) *discordgo.MessageEmbed {
	lines := []string{
		fmt.Sprintf("あなたの手: **%s**(%d)", blackjackHand(board.Player), board.PlayerValue),
		fmt.Sprintf("ディーラー: **%s**(%d)", blackjackHand(board.Dealer), board.DealerValue),
		"",
		blackjackHeadline(board),
		fmt.Sprintf("配当: %d枚 / 残高: %d枚", payout, balance),
	}
	return &discordgo.MessageEmbed{
		Title:       "🂡 ブラックジャック — 結果",
		Description: strings.Join(lines, "\n"),
	}
}

// blackjackPendingEmbed is the finished hand shown while its payout has not
// landed. It deliberately prints no 配当/残高 line: those two numbers are the
// ones Store.SettleGame refused to produce, and inventing them here is how a
// player ends up believing they were paid.
func blackjackPendingEmbed(board blackjackBoard) *discordgo.MessageEmbed {
	lines := []string{
		fmt.Sprintf("あなたの手: **%s**(%d)", blackjackHand(board.Player), board.PlayerValue),
		fmt.Sprintf("ディーラー: **%s**(%d)", blackjackHand(board.Dealer), board.DealerValue),
		"",
		blackjackHeadline(board),
	}
	return &discordgo.MessageEmbed{
		Title:       "🂡 ブラックジャック — 結果",
		Description: strings.Join(lines, "\n"),
	}
}

// blackjackButtons builds the three buttons. ⏫ ダブル is offered only on the
// first decision (CanDouble) AND only when the account can actually cover the
// second stake — a button whose only possible answer is「チップが足りません」
// is worse than no button. disableAll switches off the whole row for a
// finished hand.
func blackjackButtons(sessionID string, board blackjackBoard, affordsDouble, disableAll bool) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "🃏 ヒット",
				Style:    discordgo.PrimaryButton,
				CustomID: BuildCustomID(string(casino.GameBlackjack), sessionID, blackjackActionHit),
				Disabled: disableAll,
			},
			discordgo.Button{
				Label:    "✋ スタンド",
				Style:    discordgo.SuccessButton,
				CustomID: BuildCustomID(string(casino.GameBlackjack), sessionID, blackjackActionStand),
				Disabled: disableAll,
			},
			discordgo.Button{
				Label:    fmt.Sprintf("⏫ ダブル %d枚", board.Bet),
				Style:    discordgo.SecondaryButton,
				CustomID: BuildCustomID(string(casino.GameBlackjack), sessionID, blackjackActionDouble),
				Disabled: disableAll || !board.CanDouble || !affordsDouble,
			},
		}},
	}
}

// translateBlackjackError renders the errors /blackjack can refuse a bet with
// (設計書 §9). Anything else is logged (redacted) and reported generically: a
// store failure must not leak a path or a token into the channel.
func translateBlackjackError(err error) string {
	var insufficientChips *casino.ErrInsufficientChips
	switch {
	case errors.As(err, &insufficientChips):
		return fmt.Sprintf("❌ チップが足りません(現在: %d枚)", insufficientChips.Balance)
	case errors.Is(err, casino.ErrGameInProgress):
		return blackjackInProgressMessage
	case errors.Is(err, casino.ErrBetOutOfRange):
		return blackjackBetRangeMessage
	case errors.Is(err, casino.ErrChipCapExceeded):
		return "❌ チップ残高が上限に達しているため開始できません。"
	default:
		slog.Error("casino: starting blackjack failed", "error", redactInteractionError(err))
		return blackjackStartFailedMessage
	}
}

// translateBlackjackPressError renders the errors a BUTTON can fail with. A
// hand that is gone (settled, swept, lost to a restart) is the common case
// and gets the ⌛ wording rather than an error.
func translateBlackjackPressError(err error) string {
	var insufficientChips *casino.ErrInsufficientChips
	switch {
	case errors.As(err, &insufficientChips):
		return fmt.Sprintf("❌ チップが足りません(現在: %d枚)", insufficientChips.Balance)
	case errors.Is(err, casino.ErrSessionNotFound), errors.Is(err, casino.ErrNoGameInProgress), errors.Is(err, errBlackjackBadState):
		return blackjackSessionOverMessage
	case errors.Is(err, casino.ErrDoubleUnavailable):
		return blackjackDoubleOverMessage
	case errors.Is(err, errBlackjackUnknownAction):
		return unknownComponentMessage
	default:
		slog.Error("casino: blackjack button failed", "error", redactInteractionError(err))
		return blackjackActionFailedMessage
	}
}

// --- /blackjack ------------------------------------------------------------

func (c *BlackjackCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return c.handle(s, i)
}

// handle takes the responder as an interface so the whole start path is
// testable; the production caller passes *discordgo.Session.
func (c *BlackjackCommand) handle(r interactionResponder, i *discordgo.InteractionCreate) error {
	if msg := requireGuildContext(i); msg != "" {
		return respondVia(r, i.Interaction, messageResponse(msg))
	}

	var bet int64
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "bet" {
			bet = opt.IntValue()
		}
	}
	if bet < blackjackMinBet || bet > blackjackMaxBet { // defense in depth — MinValue/MaxValue only bind the client
		return respondVia(r, i.Interaction, messageResponse(blackjackBetRangeMessage))
	}

	now, userID := time.Now(), resolveUserID(i)
	if err := c.store.EnsureCasinoAccess(i.GuildID, userID, now); err != nil { // §3.8
		slog.Error("casino: EnsureCasinoAccess failed", "command", "blackjack", "error", redactInteractionError(err))
		return respondVia(r, i.Interaction, messageResponse("❌ カジノの初期化に失敗しました。"))
	}

	// Stake BEFORE the hand (完了条件の順序), for the reason /highlow states:
	// Store.OpenGame is the persisted half of "one game per person", so it is
	// the half that refuses a second bet.
	if err := c.store.OpenGame(i.GuildID, userID, string(casino.GameBlackjack), bet, now); err != nil {
		return respondVia(r, i.Interaction, messageResponse(translateBlackjackError(err)))
	}

	game := c.newGame(bet)
	board := snapshotBlackjack(game) // safe without the lock: nobody can reach this hand until Open publishes its ID

	session, err := c.sessions.Open(i.GuildID, userID, casino.GameBlackjack, game, casino.MessageRef{ChannelID: i.ChannelID})
	if err != nil {
		c.refundUnplayableHand(i.GuildID, userID, bet) // the chips already moved: hand them straight back
		return respondVia(r, i.Interaction, messageResponse(translateBlackjackError(err)))
	}

	// A natural (either side, or both) settles at the deal: 設計書 §7 resolves
	// it before any button exists, so the hand is over before it is drawn.
	if board.Finished {
		return c.settleDealtHand(r, i, session.ID, board, game.Settle())
	}

	if err := respondVia(r, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{blackjackBoardEmbed(board, userID)},
			Components: blackjackButtons(session.ID, board, c.affordsDouble(board, i.GuildID, userID, now), false),
		},
	}); err != nil {
		// The hand never reached Discord, so there is nothing to press and
		// nothing will ever settle it.
		c.closeUndeliveredHand(session.ID, i.GuildID, userID, bet)
		return err
	}

	c.rememberHandMessage(r, i.Interaction, session.ID)
	return nil
}

// settleDealtHand closes and pays a hand that never took a press. The session
// is removed first, exactly as WithSession removes a finished board, so the
// settlement below is the only one this hand can ever get.
func (c *BlackjackCommand) settleDealtHand(r interactionResponder, i *discordgo.InteractionCreate, sessionID string, board blackjackBoard, payout int64) error {
	lock := c.locks.acquire(sessionID)
	defer c.locks.release(sessionID, lock)

	c.sessions.Close(sessionID)
	pending := &blackjackPending{GuildID: i.GuildID, UserID: resolveUserID(i), Payout: payout, Board: board}
	lock.pending = pending
	return c.settle(r, i, sessionID, lock, pending, discordgo.InteractionResponseChannelMessageWithSource)
}

// closeUndeliveredHand retires a hand whose board never reached the player,
// and gives the stake back only when Close says the session was still ours:
// Discord can create the message and still fail the call, so by the time the
// error lands the hand may have been played out and replaced, in which case
// the escrow on the account belongs to the game that came after it.
//
// It takes THIS BOARD'S LOCK FIRST, for the same reason every press does. ⏫
// moves its second stake outside the manager's lock (disk I/O never runs
// inside WithSession), so an unlocked close can land between that stake and
// the hand that would count it: the refund would return the original bet
// alone, the raise would sit in an escrow nothing can settle, and the press
// would then find its session gone. Under the lock the press is over before
// the decision — a double always finishes the hand, so the session is already
// gone and Close reports false, and a hit leaves an escrow that is still
// exactly the bet.
func (c *BlackjackCommand) closeUndeliveredHand(sessionID, guildID, userID string, bet int64) {
	lock := c.locks.acquire(sessionID)
	defer c.locks.release(sessionID, lock)

	if c.sessions.Close(sessionID) {
		c.refundUnplayableHand(guildID, userID, bet)
	}
}

// refundUnplayableHand returns a stake whose hand never became playable.
// Failing only means the chips stay in escrow until the next restart returns
// them (RefundStaleEscrows), so it is logged rather than shown to the player,
// who already has a refusal in front of them.
func (c *BlackjackCommand) refundUnplayableHand(guildID, userID string, bet int64) {
	if _, err := c.store.SettleGame(guildID, userID, bet); err != nil {
		slog.Error("casino: refunding an undelivered blackjack hand failed", "error", redactInteractionError(err))
	}
}

// affordsDouble reports whether the ⏫ button should be live. The store is
// only asked when the hand would allow a double at all, so a hit does not buy
// a needless read. A store that cannot be read disables the button: offering
// a double the account may not cover is the more expensive mistake.
func (c *BlackjackCommand) affordsDouble(board blackjackBoard, guildID, userID string, now time.Time) bool {
	if !board.CanDouble {
		return false
	}
	view, err := c.store.ViewAccount(guildID, userID, now)
	if err != nil {
		slog.Error("casino: reading the balance for a blackjack double failed", "error", redactInteractionError(err))
		return false
	}
	return view.Account.Chips >= board.Bet
}

// rememberHandMessage fills in the session's MessageRef once Discord has
// created the message, for the same reason /highlow does: the idle sweeper
// (C2-08) needs the message ID to edit the hand with the bot token after the
// interaction token expires. A failure only costs the sweeper its edit.
func (c *BlackjackCommand) rememberHandMessage(r interactionResponder, interaction *discordgo.Interaction, sessionID string) {
	msg, err := r.InteractionResponse(interaction)
	if err != nil {
		slog.Error("discord: InteractionResponse lookup failed for a blackjack hand", "error", redactInteractionError(err))
		return
	}
	if err := c.sessions.WithSession(sessionID, func(session *casino.Session) (bool, error) {
		session.Ref = casino.MessageRef{ChannelID: msg.ChannelID, MessageID: msg.ID}
		return false, nil
	}); err != nil {
		slog.Error("casino: recording the blackjack hand message failed", "error", redactInteractionError(err))
	}
}

// --- buttons ---------------------------------------------------------------

func (c *BlackjackCommand) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate, sessionID, action string) error {
	return c.handleComponent(s, i, sessionID, action)
}

func (c *BlackjackCommand) handleComponent(r interactionResponder, i *discordgo.InteractionCreate, sessionID, action string) error {
	// One press at a time on THIS hand, from the move to the reply. Without
	// it two presses can apply in order and answer out of order, and the
	// slower answer repaints a finished hand as a playable one.
	lock := c.locks.acquire(sessionID)
	defer c.locks.release(sessionID, lock)

	// Chips the hand already owes outrank any new press: it is over, only the
	// payment is missing, so every button on it retries the payment.
	if pending, owed := lock.pending.(*blackjackPending); owed {
		if msg := requireSessionOwner(i, pending.UserID); msg != "" {
			return respondVia(r, i.Interaction, ephemeralResponse(msg))
		}
		return c.settle(r, i, sessionID, lock, pending, discordgo.InteractionResponseUpdateMessage)
	}

	// Hold, not Get: the board lock above serializes PRESSES, but the idle
	// sweeper does not take it. ⏫ stakes its second bet with AddToEscrow —
	// disk I/O, so it cannot run inside WithSession — and a sweep landing in
	// that gap would auto-resolve the 100-chip hand while 200 sit in escrow.
	// Hold marks the board busy under the manager's own lock, which is the
	// lock Sweep's deadline test takes (反復 1 の指摘 2).
	session, ok := c.sessions.Hold(sessionID)
	if !ok {
		return respondVia(r, i.Interaction, ephemeralResponse(blackjackSessionOverMessage))
	}
	defer c.sessions.Release(sessionID)

	if msg := requireSessionOwner(i, session.UserID); msg != "" {
		return respondVia(r, i.Interaction, ephemeralResponse(msg))
	}

	// The hand's last edit never reached Discord, so the message shows the
	// PREVIOUS cards while the game holds the drawn one. A stand or a ⏫ aimed
	// at that picture would be decided on a total the player never saw — and
	// ⏫ would stake a second bet on it — so this comes BEFORE stakeDouble:
	// repair the picture and take no move.
	if session.NeedsRedraw {
		return c.redrawStaleHand(r, i, sessionID, session)
	}

	// ⏫ stakes chips, and the stake is PERSISTED BEFORE the hand applies it
	// (C2-04 の順序). Reversed, a stale ⏫ would take the extra bet and then be
	// refused by the hand, leaving chips in escrow nothing accounts for.
	if action == blackjackActionDouble {
		if err := c.stakeDouble(session, sessionID); err != nil {
			return respondVia(r, i.Interaction, ephemeralResponse(translateBlackjackPressError(err)))
		}
	}

	var (
		board    blackjackBoard
		finished bool
		payout   int64
	)
	// Everything inside this closure runs under the manager's lock: no
	// Discord call, no disk I/O, no re-entry into the manager (危険地帯).
	err := c.sessions.WithSession(sessionID, func(live *casino.Session) (bool, error) {
		game, isBlackjack := live.State.(*casino.BlackjackGame)
		if !isBlackjack {
			return true, errBlackjackBadState
		}
		switch action {
		case blackjackActionHit:
			if _, hitErr := game.Hit(); hitErr != nil {
				return false, hitErr // a refused move leaves the hand playable
			}
		case blackjackActionStand:
			if standErr := game.Stand(); standErr != nil {
				return false, standErr
			}
		case blackjackActionDouble:
			if _, doubleErr := game.Double(); doubleErr != nil {
				return false, doubleErr
			}
		default:
			return false, errBlackjackUnknownAction
		}
		board = snapshotBlackjack(game)
		finished = board.Finished
		payout = game.Settle()
		return finished, nil
	})
	if err != nil {
		return respondVia(r, i.Interaction, ephemeralResponse(translateBlackjackPressError(err)))
	}

	if !finished {
		if err := respondVia(r, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Embeds:     []*discordgo.MessageEmbed{blackjackBoardEmbed(board, session.UserID)},
				Components: blackjackButtons(sessionID, board, c.affordsDouble(board, session.GuildID, session.UserID, time.Now()), false),
			},
		}); err != nil {
			// The card was drawn but the message did not change: the hand in
			// the channel is now a card behind the game. Flag it so the next
			// press repairs the picture instead of standing on a dead total.
			c.sessions.SetNeedsRedraw(sessionID, true)
			return err
		}
		return nil
	}

	// The hand is gone from the manager, so what it owes now lives here.
	// Recording it BEFORE the store call is the whole point: a refused payout
	// must leave something for the next press to retry.
	pending := &blackjackPending{GuildID: session.GuildID, UserID: session.UserID, Payout: payout, Board: board}
	lock.pending = pending
	return c.settle(r, i, sessionID, lock, pending, discordgo.InteractionResponseUpdateMessage)
}

// stakeDouble runs the double in the order C2-04 fixed: ask the hand whether
// the double is legal (under the manager's lock), move the chips (outside it —
// disk I/O never runs inside WithSession), and only then let the caller apply
// the draw. The board lock is what makes the gap between the question and the
// stake safe: no other press on this hand can run inside it.
func (c *BlackjackCommand) stakeDouble(session casino.Session, sessionID string) error {
	var extra int64
	if err := c.sessions.WithSession(sessionID, func(live *casino.Session) (bool, error) {
		game, isBlackjack := live.State.(*casino.BlackjackGame)
		if !isBlackjack {
			return true, errBlackjackBadState
		}
		if !game.CanDouble() {
			return false, casino.ErrDoubleUnavailable
		}
		extra = game.Bet()
		return false, nil
	}); err != nil {
		return err
	}
	return c.store.AddToEscrow(session.GuildID, session.UserID, extra)
}

// DisabledComponents redraws a swept hand's buttons greyed out, for the
// sweeper's closing edit (設計書 §4: 決着したメッセージのボタンは無効化して残す).
// state is the board Sweep took out of the manager, so reading it here needs
// no lock. affordsDouble is false on purpose: every button is disabled
// anyway, and asking the store would put disk I/O on the sweep path.
func (c *BlackjackCommand) DisabledComponents(sessionID string, state any) []discordgo.MessageComponent {
	game, isBlackjack := state.(*casino.BlackjackGame)
	if !isBlackjack {
		return nil
	}
	return blackjackButtons(sessionID, snapshotBlackjack(game), false, true)
}

// redrawStaleHand repaints a hand whose last edit was lost and answers the
// presser privately. It takes NO game action and stakes NO chips: the press
// paid for the repair, and the player decides again on a total that is true.
//
// The flag comes down only after the redraw has landed — a second lost edit
// leaves the hand exactly as stale as it was, and the press after it must get
// the same treatment. The caller holds both the board lock and a Hold.
func (c *BlackjackCommand) redrawStaleHand(r interactionResponder, i *discordgo.InteractionCreate, sessionID string, session casino.Session) error {
	var board blackjackBoard
	if err := c.sessions.WithSession(sessionID, func(live *casino.Session) (bool, error) {
		game, isBlackjack := live.State.(*casino.BlackjackGame)
		if !isBlackjack {
			return true, errBlackjackBadState
		}
		board = snapshotBlackjack(game)
		return false, nil
	}); err != nil {
		return respondVia(r, i.Interaction, ephemeralResponse(translateBlackjackPressError(err)))
	}

	if err := respondVia(r, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{blackjackBoardEmbed(board, session.UserID)},
			Components: blackjackButtons(sessionID, board, c.affordsDouble(board, session.GuildID, session.UserID, time.Now()), false),
		},
	}); err != nil {
		return err
	}
	c.sessions.SetNeedsRedraw(sessionID, false)
	notifyRedrawn(r, i)
	return nil
}

// blackjackPending is a finished hand's unpaid settlement: everything needed
// to pay it and to draw the closing message.
type blackjackPending struct {
	GuildID string
	UserID  string
	Payout  int64
	Board   blackjackBoard
}

// settle persists the payout and shows the hand, in that order: the chips are
// the part that must survive a crash, and a failed edit must never be retried
// into a second payout.
//
// It is the only writer of lock.pending — cleared the moment the chips land,
// kept (under a 🔁 button) when they do not. responseType is
// ChannelMessageWithSource for a hand that ended at the deal, where no message
// exists yet, and UpdateMessage for every press. The caller holds lock.
func (c *BlackjackCommand) settle(r interactionResponder, i *discordgo.InteractionCreate, sessionID string, lock *boardLock, pending *blackjackPending, responseType discordgo.InteractionResponseType) error {
	settled, err := c.store.SettleGame(pending.GuildID, pending.UserID, pending.Payout)
	if err != nil {
		slog.Error("casino: SettleGame failed for blackjack", "error", redactInteractionError(err))
		return respondVia(r, i.Interaction, &discordgo.InteractionResponse{
			Type: responseType,
			Data: &discordgo.InteractionResponseData{
				Embeds:     []*discordgo.MessageEmbed{blackjackPendingEmbed(pending.Board)},
				Content:    casinoSettleFailedMessage,
				Components: settleRetryButtons(string(casino.GameBlackjack), sessionID),
			},
		})
	}
	lock.pending = nil

	if err := respondVia(r, i.Interaction, &discordgo.InteractionResponse{
		Type: responseType,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{blackjackResultEmbed(pending.Board, settled.Payout, settled.Chips)},
			Components: blackjackButtons(sessionID, pending.Board, false, true),
		},
	}); err != nil {
		return err
	}

	if celebration := blackjackCelebration(pending.UserID, pending.Board, settled.Payout); celebration != "" {
		if _, sendErr := r.ChannelMessageSend(i.ChannelID, celebration); sendErr != nil {
			slog.Error("discord: ChannelMessageSend failed for a blackjack celebration", "error", redactInteractionError(sendErr))
		}
	}
	return nil
}

// blackjackCelebration is the public message a natural earns, or "" when the
// hand is not worth telling the channel about. A separate message like
// slot.go's jackpot — the hand itself stays the hand.
func blackjackCelebration(userID string, board blackjackBoard, payout int64) string {
	if board.Result != casino.BlackjackNatural || payout <= 0 {
		return ""
	}
	return fmt.Sprintf("🎉🎉🎉 <@%s> がブラックジャック! %d枚 を持ち帰りました!! 🎉🎉🎉", userID, payout)
}
