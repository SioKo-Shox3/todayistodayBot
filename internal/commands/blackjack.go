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

// blackjackResultLines is a finished hand with the dealer's hole card turned
// over, WITHOUT the money: both hands face up and the line that says who won.
// Split out because the closing edits that cannot name money (a refused
// payout) or word it themselves (the sweeper) need exactly this much.
func blackjackResultLines(board blackjackBoard) []string {
	return []string{
		fmt.Sprintf("あなたの手: **%s**(%d)", blackjackHand(board.Player), board.PlayerValue),
		fmt.Sprintf("ディーラー: **%s**(%d)", blackjackHand(board.Dealer), board.DealerValue),
		"",
		blackjackHeadline(board),
	}
}

// blackjackResultEmbed renders a finished hand with the dealer's hole card
// turned over. payout/balance are what Store.SettleGame actually paid.
func blackjackResultEmbed(board blackjackBoard, payout, balance int64) *discordgo.MessageEmbed {
	lines := append(blackjackResultLines(board), casinoPayoutLine(payout, balance))
	return &discordgo.MessageEmbed{
		Title:       "🂡 ブラックジャック — 結果",
		Description: strings.Join(lines, "\n"),
	}
}

// TimedOutEmbed draws the closing message of a hand the sweeper auto-stood
// (C2-12). AutoResolve plays the dealer out, so the hole card is already
// turned over in state — and this edit replaces the board that was hiding it,
// which makes this the only place the player can ever see what they lost to.
//
// state is the swept hand, owned by the sweeper's goroutine alone, so
// snapshotting it here needs no lock (the same licence DisabledComponents has).
func (c *BlackjackCommand) TimedOutEmbed(state any, settled casino.SettleResult) *discordgo.MessageEmbed {
	game, isBlackjack := state.(*casino.BlackjackGame)
	if !isBlackjack {
		return nil
	}
	board := snapshotBlackjack(game)
	return casinoTimedOutEmbed(append(blackjackResultLines(board), casinoPayoutLine(settled.Payout, settled.Chips)))
}

// blackjackPendingEmbed is the finished hand shown while its payout has not
// landed. It deliberately prints no 配当/残高 line: those two numbers are the
// ones Store.SettleGame refused to produce, and inventing them here is how a
// player ends up believing they were paid.
func blackjackPendingEmbed(board blackjackBoard) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🂡 ブラックジャック — 結果",
		Description: strings.Join(blackjackResultLines(board), "\n"),
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
	case errors.Is(err, casino.ErrRefundPending):
		// Not "the hand is over": it is standing, and the chips are on their
		// way back. Same wording as a settlement that has not landed yet.
		return casinoSettleFailedMessage
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

	// The hand's ID comes first, before the chips move, for the reason
	// /highlow states: it is what lets the stake name its own board from its
	// very first write (設計書 C-3b). Nothing is staked yet, so a failure
	// here costs only the refusal.
	sessionID, err := casino.NewSessionID()
	if err != nil {
		slog.Error("casino: minting a blackjack hand ID failed", "error", redactInteractionError(err))
		return respondVia(r, i.Interaction, messageResponse(translateBlackjackError(err)))
	}

	game := c.newGame(bet)
	board := snapshotBlackjack(game) // safe without the lock: nobody can reach this hand until Open publishes its ID

	// The HAND is taken FIRST and the chips move afterwards (C3B-P2), for the
	// reason /highlow states: the hand is memory only, so a refused open here
	// leaves nothing to give back, and the state "chips are staked but no
	// board holds them" stops being reachable.
	session, err := c.sessions.OpenWithID(sessionID, i.GuildID, userID, casino.GameBlackjack, game, casino.MessageRef{ChannelID: i.ChannelID})
	if err != nil {
		return respondVia(r, i.Interaction, messageResponse(translateBlackjackError(err)))
	}
	// The hand comes back HELD and stays held for the whole open (C3B-P3),
	// for the reason /highlow states: the sweep must not be able to take the
	// board between the registration and the stake, where the open would
	// then leave an escrow that no board names.
	defer c.sessions.Release(sessionID)

	// Store.OpenGame is still the persisted half of "one game per person",
	// and the half that survives a restart. ⏫, every press and the sweeper
	// name sessionID, and nothing else can settle these chips.
	if err := c.store.OpenGame(i.GuildID, userID, string(casino.GameBlackjack), sessionID, bet, now); err != nil {
		// Nothing was staked, so the hand can go — asked of the account
		// rather than assumed here (C3B-P4).
		closeBoardIfSettled(c.store, c.sessions, i.GuildID, userID, sessionID)
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
		c.withdrawUndeliveredHand(session.ID, i.GuildID, userID, bet)
		return err
	}

	c.rememberHandMessage(r, i.Interaction, session.ID)
	return nil
}

// settleDealtHand pays a hand that never took a press — a natural, settled at
// the deal — and retires it once the chips have landed.
//
// The hand used to be removed FIRST, which was the last of the five stranded
// stakes (反復 1 の指摘 1): when the payout was refused AND the reply that
// carries the 🔁 button never reached Discord, the stake sat in escrow with no
// board, no button and no sweep, until the next restart. Settling first and
// closing through the one door (C3B-P4) removes the trade — a refused payout
// leaves the hand standing for the idle sweep, and a payout that lands takes
// the hand with it.
//
// Nothing can settle this hand twice in the meantime. The caller holds the
// board for the whole open (Release is deferred in handle), so the sweep skips
// it, and this path holds the board lock, so no press can be inside settle at
// the same time.
func (c *BlackjackCommand) settleDealtHand(r interactionResponder, i *discordgo.InteractionCreate, sessionID string, board blackjackBoard, payout int64) error {
	lock := c.locks.acquire(sessionID)
	defer c.locks.release(sessionID, lock)

	userID := resolveUserID(i)
	pending := &blackjackPending{GuildID: i.GuildID, UserID: userID, Payout: payout, Board: board}
	lock.pending = pending
	// What this hand owes is already decided — the deal decided it — so the
	// sweep must pay exactly that rather than ask the hand again. The mark is
	// what keeps a hand that is standing only because its payout failed from
	// being re-derived by AutoResolve.
	c.sessions.MarkRefundPending(sessionID, payout)

	err := c.settle(r, i, sessionID, lock, pending, discordgo.InteractionResponseChannelMessageWithSource)
	closeBoardIfSettled(c.store, c.sessions, i.GuildID, userID, sessionID)
	return err
}

// withdrawUndeliveredHand retires a hand whose board never reached the
// player, and gives the stake back only when the hand was still ours: Discord
// can create the message and still fail the call, so by the time the error
// lands the hand may have been played out and replaced, in which case the
// escrow on the account belongs to the game that came after it. A Hold that
// fails is that answer.
//
// It takes THIS BOARD'S LOCK FIRST, for the same reason every press does. ⏫
// moves its second stake outside the manager's lock (disk I/O never runs
// inside WithSession), so an unlocked withdrawal can land between that stake
// and the hand that would count it: the refund would return the original bet
// alone, the raise would sit in an escrow nothing can settle, and the press
// would then find its session gone. Under the lock the press is over before
// the decision — a double always finishes the hand, so the session is already
// gone and Hold reports false, and a hit leaves an escrow that is still
// exactly the bet.
//
// The refund goes BEFORE the hand is retired, the order /highlow and /duel
// keep, so the chips are never in escrow with no board naming them — and a
// refund that FAILED leaves the hand standing, exactly like those two
// (C3B-P3). Retiring it there used to be justified by "the sweep would stand
// this hand", which was the wrong half of the trade: it did avoid playing a
// hand the player never saw, but it did so by throwing away the only thing
// that could still return the chips before the next restart. The mark below
// is what removes the trade — the hand is never played out AND the stake is
// returned by the next pass.
func (c *BlackjackCommand) withdrawUndeliveredHand(sessionID, guildID, userID string, bet int64) {
	lock := c.locks.acquire(sessionID)
	defer c.locks.release(sessionID, lock)

	if _, held := c.sessions.Hold(sessionID); !held {
		return
	}
	defer c.sessions.Release(sessionID)

	if err := c.refundUnplayableHand(guildID, userID, sessionID, bet); err != nil {
		// The chips never moved, so the hand may not be retired: a board that
		// is gone has no press and no sweep left to give them back, and the
		// stake would sit in escrow — blocking every new game — until the
		// next restart. The mark makes the idle sweep hand back exactly this
		// stake instead of standing the hand: the cards were never seen, so
		// there is nothing to decide, only chips to return.
		c.sessions.MarkRefundPending(sessionID, bet)
		return
	}
	closeBoardIfSettled(c.store, c.sessions, guildID, userID, sessionID)
}

// refundUnplayableHand returns a stake whose hand never became playable and
// reports whether the chips moved. A failure is logged rather than shown to
// the player, who already has a refusal in front of them, and the caller
// keeps the board so the idle sweep can retry the refund.
//
// escrowID is the mark the stake carries at this point of the open — see
// /highlow's withdrawUndeliveredBoard — so the refund can only ever hand back
// the chips this hand staked.
func (c *BlackjackCommand) refundUnplayableHand(guildID, userID, escrowID string, bet int64) error {
	if _, err := c.store.SettleGame(guildID, userID, escrowID, bet); err != nil {
		slog.Error("casino: refunding an undelivered blackjack hand failed, the sweeper retries it", "error", redactInteractionError(err))
		return err
	}
	return nil
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
		err := c.settle(r, i, sessionID, lock, pending, discordgo.InteractionResponseUpdateMessage)
		// A hand that ended at the deal is still standing while its payout is
		// unpaid (settleDealtHand), so the retry that finally pays it is what
		// retires it. Every other retry finds the hand already gone, and the
		// door answers that with nothing.
		closeBoardIfSettled(c.store, c.sessions, pending.GuildID, pending.UserID, sessionID)
		return err
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

	// This hand is only standing because the refund (or the payout a natural
	// decided at the deal) was refused (C3B-P5). What it owes is already
	// fixed, so a hit, a stand or a ⏫ here would move a hand whose number can
	// no longer change — and ⏫ would stake a second bet the fixed amount will
	// never return. The wording is the one an unpaid settlement already uses.
	if session.RefundPending() {
		return respondVia(r, i.Interaction, ephemeralResponse(casinoSettleFailedMessage))
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
		// Asked of the LIVE hand, not of the copy the press is holding: this
		// is the only place in the package that adds chips to an escrow that
		// already exists, so the refusal sits against the write itself rather
		// than only against the press that reached it (C3B-P5).
		if live.RefundPending() {
			return false, casino.ErrRefundPending
		}
		if !game.CanDouble() {
			return false, casino.ErrDoubleUnavailable
		}
		extra = game.Bet()
		return false, nil
	}); err != nil {
		return err
	}
	return c.store.AddToEscrow(session.GuildID, session.UserID, sessionID, extra)
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
	settled, err := c.store.SettleGame(pending.GuildID, pending.UserID, sessionID, pending.Payout)
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
