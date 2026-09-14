package commands

import (
	"errors"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

// --- fixtures --------------------------------------------------------------

// The fake responder only records what went out, so it is game-agnostic:
// blackjack drives the one highlow_test.go already defines rather than
// growing a second copy of it. flakyBank (a real store that can be told to
// refuse a settlement) is shared for the same reason.
type fakeCasinoResponder = fakeHighLowResponder

// blackjackRngFor is the deal driver for one seed. The same seed always deals
// the same shoe, which is what lets a test probe a deal and then play it.
func blackjackRngFor(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }

// blackjackSeedWhere returns the first seed whose 100-chip deal satisfies
// want. Searching beats a hand-built shoe: casino's shoe constructor that
// takes a fixed deck is unexported, and a hand-computed Intn script would be
// pinned to the exact shuffle loop rather than to the deal it wants.
//
// want receives a throwaway game and may play it.
func blackjackSeedWhere(t *testing.T, what string, want func(*casino.BlackjackGame) bool) int64 {
	t.Helper()
	for seed := int64(1); seed < 20000; seed++ {
		if want(casino.NewBlackjack(100, blackjackRngFor(seed))) {
			return seed
		}
	}
	t.Fatalf("no deal in the first 20,000 shoes is %s", what)
	return 0
}

// blackjackProbe rebuilds the hand a seed deals, so a test can compute what
// the real flow must pay instead of restating casino's payout rules.
func blackjackProbe(bet int64, seed int64) *casino.BlackjackGame {
	return casino.NewBlackjack(bet, blackjackRngFor(seed))
}

// newBlackjackCommandForTest wires a command onto this test's own store and
// its own session manager — never casino.Default()/DefaultSessions(), which
// point at the production file and at the process-wide board registry.
func newBlackjackCommandForTest(t *testing.T, seed int64) (*BlackjackCommand, *flakyBank) {
	t.Helper()
	bank := &flakyBank{Store: casino.New(filepath.Join(t.TempDir(), "casino.json"))}
	return &BlackjackCommand{
		store:    bank,
		sessions: casino.NewSessionManager(time.Now, casino.DefaultSessionTTL),
		newGame:  func(bet int64) *casino.BlackjackGame { return casino.NewBlackjack(bet, blackjackRngFor(seed)) },
	}, bank
}

func blackjackSlashInteraction(bet int64) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID:    "1",
		Token: "TEST_TOKEN",
		Type:  discordgo.InteractionApplicationCommand,
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "blackjack",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "bet", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(bet)},
			},
		},
		GuildID:   "g1",
		ChannelID: "c1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: "u1"}},
	}}
}

// startBlackjack runs /blackjack and returns the live session's ID.
func startBlackjack(t *testing.T, c *BlackjackCommand, r *fakeCasinoResponder, bet int64) string {
	t.Helper()
	if err := c.handle(r, blackjackSlashInteraction(bet)); err != nil {
		t.Fatalf("/blackjack: %v", err)
	}
	_, sessionID, _, ok := ParseCustomID(blackjackButtonsOf(t, r.last(t))[0].CustomID)
	if !ok {
		t.Fatal("the hand's first button does not carry a parsable custom_id")
	}
	return sessionID
}

// press drives one button of a live hand as its owner.
func (c *BlackjackCommand) press(t *testing.T, r *fakeCasinoResponder, sessionID, action string) *discordgo.InteractionResponse {
	t.Helper()
	if err := c.handleComponent(r, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, action), "u1"), sessionID, action); err != nil {
		t.Fatalf("press %q: %v", action, err)
	}
	return r.last(t)
}

// blackjackButtonsOf pulls the three buttons out of a response.
func blackjackButtonsOf(t *testing.T, resp *discordgo.InteractionResponse) []discordgo.Button {
	t.Helper()
	if resp.Data == nil || len(resp.Data.Components) != 1 {
		t.Fatalf("response carries %d component rows, want 1", len(resp.Data.Components))
	}
	row, ok := resp.Data.Components[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("component row is %T, want discordgo.ActionsRow", resp.Data.Components[0])
	}
	buttons := make([]discordgo.Button, 0, len(row.Components))
	for _, component := range row.Components {
		button, isButton := component.(discordgo.Button)
		if !isButton {
			t.Fatalf("row holds a %T, want discordgo.Button", component)
		}
		buttons = append(buttons, button)
	}
	if len(buttons) != 3 {
		t.Fatalf("hand has %d buttons, want 3 (ヒット / スタンド / ダブル)", len(buttons))
	}
	return buttons
}

func blackjackBodyOf(t *testing.T, resp *discordgo.InteractionResponse) string {
	t.Helper()
	if resp.Data == nil || len(resp.Data.Embeds) != 1 {
		t.Fatal("response carries no single embed")
	}
	return resp.Data.Embeds[0].Description
}

// Cards for the pure renderers, named so the fixtures read like a table.
var (
	blackjackAceSpade  = casino.Card{Rank: casino.MaxRank, Suit: casino.SuitSpade}
	blackjackKingHeart = casino.Card{Rank: 13, Suit: casino.SuitHeart}
	blackjackSixClub   = casino.Card{Rank: 6, Suit: casino.SuitClub}
	blackjackFiveClub  = casino.Card{Rank: 5, Suit: casino.SuitClub}
)

// --- display (pure) --------------------------------------------------------

func TestBlackjackDealerUpcardHidesEveryCardButTheFirst(t *testing.T) {
	cases := []struct {
		name  string
		cards []casino.Card
		want  string
	}{
		{"opening two", []casino.Card{blackjackKingHeart, blackjackAceSpade}, "K♥ " + blackjackHiddenCard},
		{"a third card down", []casino.Card{blackjackKingHeart, blackjackAceSpade, blackjackSixClub}, "K♥ " + blackjackHiddenCard + " " + blackjackHiddenCard},
		{"no cards yet", nil, blackjackHiddenCard},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blackjackDealerUpcard(tc.cards); got != tc.want {
				t.Errorf("upcard: got %q, want %q", got, tc.want)
			}
		})
	}
	// The hole card's own face must not appear anywhere in the live board.
	board := blackjackBoard{Bet: 100, TotalBet: 100, Player: []casino.Card{blackjackSixClub, blackjackFiveClub}, PlayerValue: 11, Dealer: []casino.Card{blackjackKingHeart, blackjackAceSpade}, DealerValue: 21}
	body := blackjackBoardEmbed(board, "u1").Description
	if strings.Contains(body, blackjackAceSpade.String()) {
		t.Errorf("the live board shows the hole card:\n%s", body)
	}
	if !strings.Contains(body, "あなたの手: **6♣ 5♣**(11)") || !strings.Contains(body, "ディーラー: K♥ "+blackjackHiddenCard) {
		t.Errorf("board body does not read as a hand:\n%s", body)
	}
	if !strings.Contains(body, "ベット: 100枚") {
		t.Errorf("board body does not state the stake:\n%s", body)
	}
}

func TestBlackjackStakeLineNamesBothNumbersOnceDoubled(t *testing.T) {
	plain := blackjackStakeLine(blackjackBoard{Bet: 100, TotalBet: 100})
	if plain != "ベット: 100枚" {
		t.Errorf("plain stake line: got %q", plain)
	}
	doubled := blackjackStakeLine(blackjackBoard{Bet: 100, TotalBet: 200, Doubled: true})
	if !strings.Contains(doubled, "100枚") || !strings.Contains(doubled, "200枚") {
		t.Errorf("a doubled stake line must name both numbers, got %q", doubled)
	}
}

func TestBlackjackHeadlineNamesEveryEndingAndTheSideThatBusted(t *testing.T) {
	cases := []struct {
		name  string
		board blackjackBoard
		want  string
	}{
		{"natural", blackjackBoard{Result: casino.BlackjackNatural, PlayerValue: 21}, "ブラックジャック"},
		{"player wins on points", blackjackBoard{Result: casino.BlackjackPlayerWin, PlayerValue: 20, DealerValue: 18}, "あなたの勝ち"},
		{"dealer busts", blackjackBoard{Result: casino.BlackjackPlayerWin, PlayerValue: 20, DealerValue: 23}, "ディーラーがバースト"},
		{"player busts", blackjackBoard{Result: casino.BlackjackDealerWin, PlayerValue: 24, DealerValue: 18}, "バースト"},
		{"dealer wins on points", blackjackBoard{Result: casino.BlackjackDealerWin, PlayerValue: 18, DealerValue: 20}, "ディーラーの勝ち"},
		{"push", blackjackBoard{Result: casino.BlackjackPush, PlayerValue: 20, DealerValue: 20}, "プッシュ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blackjackHeadline(tc.board); !strings.Contains(got, tc.want) {
				t.Errorf("headline: got %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

func TestBlackjackResultEmbedTurnsTheHoleCardOverAndReportsTheMoney(t *testing.T) {
	board := blackjackBoard{
		Bet: 100, TotalBet: 100, Result: casino.BlackjackPlayerWin,
		Player: []casino.Card{blackjackKingHeart, blackjackAceSpade}, PlayerValue: 21,
		Dealer: []casino.Card{blackjackSixClub, blackjackFiveClub}, DealerValue: 11,
	}
	body := blackjackResultEmbed(board, 200, 1100).Description
	for _, want := range []string{"K♥ A♠", "6♣ 5♣", "配当: 200枚 / 残高: 1100枚"} {
		if !strings.Contains(body, want) {
			t.Errorf("result body is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, blackjackHiddenCard) {
		t.Errorf("a finished hand still hides a card:\n%s", body)
	}
}

// The unpaid-settlement message must not print a 配当/残高 line: those are the
// two numbers the store refused to produce, and inventing them is how a
// player ends up believing they were paid.
func TestBlackjackPendingEmbedReportsNoMoney(t *testing.T) {
	board := blackjackBoard{Result: casino.BlackjackPlayerWin, Player: []casino.Card{blackjackKingHeart, blackjackAceSpade}, PlayerValue: 21, Dealer: []casino.Card{blackjackSixClub, blackjackFiveClub}, DealerValue: 11}
	body := blackjackPendingEmbed(board).Description
	if strings.Contains(body, "配当") || strings.Contains(body, "残高") {
		t.Errorf("an unpaid hand reports money:\n%s", body)
	}
	if !strings.Contains(body, "あなたの勝ち") {
		t.Errorf("an unpaid hand does not say how it ended:\n%s", body)
	}
}

func TestBlackjackButtonsDisableDoubleUnlessTheHandAndTheBalanceAllowIt(t *testing.T) {
	cases := []struct {
		name         string
		board        blackjackBoard
		affords      bool
		disableAll   bool
		wantDouble   bool // want the ⏫ button DISABLED
		wantHitStand bool // want ヒット/スタンド DISABLED
	}{
		{"first decision, funded", blackjackBoard{Bet: 100, CanDouble: true}, true, false, false, false},
		{"first decision, broke", blackjackBoard{Bet: 100, CanDouble: true}, false, false, true, false},
		{"after a hit", blackjackBoard{Bet: 100, CanDouble: false}, true, false, true, false},
		{"finished hand", blackjackBoard{Bet: 100, CanDouble: true}, true, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := blackjackButtons("s1", tc.board, tc.affords, tc.disableAll)
			buttons := blackjackButtonsOf(t, &discordgo.InteractionResponse{Data: &discordgo.InteractionResponseData{Components: row}})
			if got := buttons[2].Disabled; got != tc.wantDouble {
				t.Errorf("⏫ ダブル disabled: got %v, want %v", got, tc.wantDouble)
			}
			if got := buttons[0].Disabled; got != tc.wantHitStand {
				t.Errorf("🃏 ヒット disabled: got %v, want %v", got, tc.wantHitStand)
			}
			if got := buttons[1].Disabled; got != tc.wantHitStand {
				t.Errorf("✋ スタンド disabled: got %v, want %v", got, tc.wantHitStand)
			}
			if want := "⏫ ダブル 100枚"; buttons[2].Label != want {
				t.Errorf("⏫ label: got %q, want %q", buttons[2].Label, want)
			}
		})
	}
}

func TestBlackjackCustomIDRoundTripForEveryAction(t *testing.T) {
	c := &BlackjackCommand{}
	for _, action := range []string{blackjackActionHit, blackjackActionStand, blackjackActionDouble, casinoActionSettle} {
		id := BuildCustomID(c.Prefix(), "0123456789abcdef0123456789abcdef", action)
		game, sessionID, got, ok := ParseCustomID(id)
		if !ok {
			t.Fatalf("ParseCustomID(%q) refused its own output", id)
		}
		if game != string(casino.GameBlackjack) || sessionID != "0123456789abcdef0123456789abcdef" || got != action {
			t.Errorf("round trip: got (%q, %q, %q), want (blackjack, …, %q)", game, sessionID, got, action)
		}
	}
}

func TestBlackjackCommandDefinitionIsGuildOnlyWithABetOption(t *testing.T) {
	def := (&BlackjackCommand{}).Definition()
	if def.Name != "blackjack" {
		t.Errorf("name: got %q, want %q", def.Name, "blackjack")
	}
	if def.Contexts == nil || len(*def.Contexts) != 1 || (*def.Contexts)[0] != discordgo.InteractionContextGuild {
		t.Error("/blackjack is not declared guild-only")
	}
	if len(def.Options) != 1 || def.Options[0].Name != "bet" || def.Options[0].MinValue == nil {
		t.Fatal("/blackjack does not take a bounded bet option")
	}
	if *def.Options[0].MinValue != blackjackMinBet || def.Options[0].MaxValue != blackjackMaxBet {
		t.Errorf("bet bounds: got [%v, %v], want [%d, %d]", *def.Options[0].MinValue, def.Options[0].MaxValue, blackjackMinBet, blackjackMaxBet)
	}
	// 設計書 §8 gives the tables ONE bet range. Two constants are fine; two
	// different ranges behind the same wording are not.
	if blackjackMinBet != highLowMinBet || blackjackMaxBet != highLowMaxBet {
		t.Errorf("blackjack bets [%d, %d] but highlow bets [%d, %d]", blackjackMinBet, blackjackMaxBet, highLowMinBet, highLowMaxBet)
	}
}

func TestBlackjackPrefixMatchesTheSessionGameKind(t *testing.T) {
	if got := (&BlackjackCommand{}).Prefix(); got != string(casino.GameBlackjack) {
		t.Errorf("prefix: got %q, want %q", got, string(casino.GameBlackjack))
	}
}

func TestBlackjackRefusesABetOutOfRange(t *testing.T) {
	c, bank := newBlackjackCommandForTest(t, 1)
	r := &fakeCasinoResponder{}
	if err := c.handle(r, blackjackSlashInteraction(blackjackMaxBet+1)); err != nil {
		t.Fatalf("/blackjack: %v", err)
	}
	if got := r.last(t).Data.Content; got != blackjackBetRangeMessage {
		t.Errorf("refusal: got %q, want %q", got, blackjackBetRangeMessage)
	}
	if bank.settles != 0 {
		t.Error("an out-of-range bet touched the account")
	}
}

// --- the deal --------------------------------------------------------------

func TestBlackjackCommandDealsAHandAndStakesTheBet(t *testing.T) {
	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}
	sessionID := startBlackjack(t, c, r, 100)

	if got := highLowEscrowOf(t, bank); got != 100 {
		t.Errorf("escrow after the deal: got %d, want 100", got)
	}
	if _, live := c.sessions.Get(sessionID); !live {
		t.Error("the deal left no live session")
	}
	probe := blackjackProbe(100, seed)
	body := blackjackBodyOf(t, r.last(t))
	if !strings.Contains(body, blackjackHand(probe.Player())) {
		t.Errorf("the board does not show the hand that was dealt (%s):\n%s", blackjackHand(probe.Player()), body)
	}
	buttons := blackjackButtonsOf(t, r.last(t))
	// 900 chips are left after the stake, so the second stake is affordable.
	if buttons[2].Disabled {
		t.Error("⏫ ダブル is disabled on the first decision of a funded account")
	}
}

// 残高不足なら disabled (完了条件): a 1,000-chip bet consumes the whole
// welcome bonus, so the second stake cannot be covered.
func TestBlackjackDisablesDoubleWhenTheStakeIsNotAffordable(t *testing.T) {
	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}
	startBlackjack(t, c, r, 1000)

	if got := highLowChipsOf(t, bank); got != 0 {
		t.Fatalf("chips after a 1,000 bet: got %d, want 0 — this test needs an account that cannot double", got)
	}
	if !blackjackButtonsOf(t, r.last(t))[2].Disabled {
		t.Error("⏫ ダブル is live although the account has no chips left to stake")
	}
}

// A natural is resolved by the deal itself (設計書 §7), so the hand is over
// before any button exists: it pays 3:2 on the spot and tells the channel.
func TestBlackjackSettlesANaturalAtTheDealAndCelebrates(t *testing.T) {
	seed := blackjackSeedWhere(t, "a player natural", func(g *casino.BlackjackGame) bool {
		return g.Result() == casino.BlackjackNatural
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}

	if err := c.handle(r, blackjackSlashInteraction(100)); err != nil {
		t.Fatalf("/blackjack: %v", err)
	}

	resp := r.last(t)
	if resp.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Errorf("a hand settled at the deal answers with type %v, want a new message", resp.Type)
	}
	want := blackjackProbe(100, seed).Settle() // 100 + floor(100*3/2) = 250
	if body := blackjackBodyOf(t, resp); !strings.Contains(body, "ブラックジャック!(3:2)") || !strings.Contains(body, "配当: 250枚") {
		t.Errorf("the natural does not read as a 3:2 win (payout %d):\n%s", want, body)
	}
	for _, button := range blackjackButtonsOf(t, resp) {
		if !button.Disabled {
			t.Errorf("button %q is live on a hand that is already over", button.Label)
		}
	}
	if got := highLowEscrowOf(t, bank); got != 0 {
		t.Errorf("escrow after the natural: got %d, want 0", got)
	}
	if got, want := highLowChipsOf(t, bank), int64(1000-100+250); got != want {
		t.Errorf("chips after the natural: got %d, want %d", got, want)
	}
	if len(r.sends) != 1 || !strings.Contains(r.sends[0], "ブラックジャック") {
		t.Errorf("the channel was told %v, want one blackjack celebration", r.sends)
	}
	if c.sessions.Len() != 0 {
		t.Error("a hand settled at the deal left a live session behind")
	}
}

// --- buttons ---------------------------------------------------------------

func TestBlackjackRefusesAnotherPlayersButton(t *testing.T) {
	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}
	sessionID := startBlackjack(t, c, r, 100)

	i := highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, blackjackActionStand), "someone-else")
	if err := c.handleComponent(r, i, sessionID, blackjackActionStand); err != nil {
		t.Fatalf("press: %v", err)
	}
	resp := r.last(t)
	if resp.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Error("the refusal was not ephemeral")
	}
	if !strings.Contains(resp.Data.Content, "あなたのゲームではありません") {
		t.Errorf("refusal: got %q", resp.Data.Content)
	}
	if bank.settles != 0 {
		t.Error("a stranger's press settled the hand")
	}
	if _, live := c.sessions.Get(sessionID); !live {
		t.Error("a stranger's press closed the hand")
	}
}

func TestBlackjackHitLeavesALivePlayableHand(t *testing.T) {
	seed := blackjackSeedWhere(t, "a hand that survives one hit", func(g *casino.BlackjackGame) bool {
		if g.State() != casino.BlackjackPlaying {
			return false
		}
		if _, err := g.Hit(); err != nil {
			return false
		}
		return g.State() == casino.BlackjackPlaying
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}
	sessionID := startBlackjack(t, c, r, 100)

	resp := c.press(t, r, sessionID, blackjackActionHit)
	if resp.Type != discordgo.InteractionResponseUpdateMessage {
		t.Errorf("a press answers with type %v, want an edit of the board", resp.Type)
	}
	probe := blackjackProbe(100, seed)
	if _, err := probe.Hit(); err != nil {
		t.Fatalf("probe hit: %v", err)
	}
	if body := blackjackBodyOf(t, resp); !strings.Contains(body, blackjackHand(probe.Player())) {
		t.Errorf("the board does not show the card that was drawn (%s):\n%s", blackjackHand(probe.Player()), body)
	}
	if got := highLowEscrowOf(t, bank); got != 100 {
		t.Errorf("escrow after a hit: got %d, want the stake still held", got)
	}
	if bank.settles != 0 {
		t.Error("an undecided hand was settled")
	}
	// The hand has three cards now, so the double is gone whatever the balance.
	if !blackjackButtonsOf(t, resp)[2].Disabled {
		t.Error("⏫ ダブル survived the first decision")
	}
}

// The chips must be PERSISTED before the hand applies the double (C2-04 の
// 順序). Reversed, a stake the hand refuses would sit in escrow with nothing
// accounting for it.
func TestBlackjackDoubleDoesNotApplyWhenTheStakeCannotBeTaken(t *testing.T) {
	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}
	sessionID := startBlackjack(t, c, r, 1000) // the whole welcome bonus

	i := highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, blackjackActionDouble), "u1")
	if err := c.handleComponent(r, i, sessionID, blackjackActionDouble); err != nil {
		t.Fatalf("press: %v", err)
	}
	if got := r.last(t).Data.Content; !strings.Contains(got, "チップが足りません") {
		t.Errorf("refusal: got %q, want a "+"チップが足りません"+" line", got)
	}
	if got := highLowEscrowOf(t, bank); got != 1000 {
		t.Errorf("escrow after the refused double: got %d, want the original stake (1000)", got)
	}
	// The hand never saw the double, so it is still on its first decision.
	resp := c.press(t, r, sessionID, blackjackActionHit)
	if body := blackjackBodyOf(t, resp); strings.Contains(body, "ダブルで") {
		t.Errorf("the hand applied a double whose stake was refused:\n%s", body)
	}
}

func TestBlackjackDoubleStakesASecondBetAndPaysOnTheTotal(t *testing.T) {
	seed := blackjackSeedWhere(t, "a double the player wins", func(g *casino.BlackjackGame) bool {
		if g.State() != casino.BlackjackPlaying || !g.CanDouble() {
			return false
		}
		if _, err := g.Double(); err != nil {
			return false
		}
		return g.Result() == casino.BlackjackPlayerWin
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}
	sessionID := startBlackjack(t, c, r, 100)

	probe := blackjackProbe(100, seed)
	if _, err := probe.Double(); err != nil {
		t.Fatalf("probe double: %v", err)
	}
	wantPayout := probe.Settle() // 2 x the TOTAL bet, i.e. 400 on a 100 stake

	resp := c.press(t, r, sessionID, blackjackActionDouble)
	if body := blackjackBodyOf(t, resp); !strings.Contains(body, "配当: 400枚") {
		t.Errorf("a doubled win pays %d, and the message does not say so:\n%s", wantPayout, body)
	}
	if got := highLowEscrowOf(t, bank); got != 0 {
		t.Errorf("escrow after the doubled hand: got %d, want 0", got)
	}
	// 1000 - 100 (stake) - 100 (second stake) + 400 (payout).
	if got, want := highLowChipsOf(t, bank), int64(1200); got != want {
		t.Errorf("chips after the doubled win: got %d, want %d", got, want)
	}
}

// 精算が編集より先: the chips are the part that must survive a crash, so the
// escrow is already released by the time the message goes out.
func TestBlackjackSettlesBeforeEditingTheMessage(t *testing.T) {
	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}
	sessionID := startBlackjack(t, c, r, 100)

	var escrowAtEdit int64 = -1
	r.onRespond = func(*discordgo.InteractionResponse) {
		escrowAtEdit = highLowEscrowOf(t, bank)
	}
	c.press(t, r, sessionID, blackjackActionStand)

	if escrowAtEdit != 0 {
		t.Errorf("escrow when the closing message went out: got %d, want 0 (settle first, edit second)", escrowAtEdit)
	}
	if bank.settles != 1 {
		t.Errorf("SettleGame was called %d times, want 1", bank.settles)
	}
}

func TestBlackjackSecondPressOnASettledHandIsToldItIsOver(t *testing.T) {
	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}
	sessionID := startBlackjack(t, c, r, 100)

	c.press(t, r, sessionID, blackjackActionStand)
	chips := highLowChipsOf(t, bank)

	resp := c.press(t, r, sessionID, blackjackActionHit)
	if got := resp.Data.Content; got != blackjackSessionOverMessage {
		t.Errorf("the second press was told %q, want %q", got, blackjackSessionOverMessage)
	}
	if resp.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Error("the ⌛ reply was not ephemeral")
	}
	if bank.settles != 1 {
		t.Errorf("SettleGame was called %d times, want 1: a finished hand pays once", bank.settles)
	}
	if got := highLowChipsOf(t, bank); got != chips {
		t.Errorf("the second press moved chips: got %d, want %d", got, chips)
	}
}

func TestBlackjackRejectsAnUnknownAction(t *testing.T) {
	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	c, _ := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}
	sessionID := startBlackjack(t, c, r, 100)

	i := highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, "surrender"), "u1")
	if err := c.handleComponent(r, i, sessionID, "surrender"); err != nil {
		t.Fatalf("press: %v", err)
	}
	if got := r.last(t).Data.Content; got != unknownComponentMessage {
		t.Errorf("unknown action: got %q, want %q", got, unknownComponentMessage)
	}
	if _, live := c.sessions.Get(sessionID); !live {
		t.Error("an unknown action closed the hand")
	}
}

// --- the settlement a hand still owes --------------------------------------

func TestBlackjackRetriesOnlyTheSettlementAfterARefusedPayout(t *testing.T) {
	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}
	sessionID := startBlackjack(t, c, r, 100)

	bank.settleErr = errors.New("the store refused this payout")
	resp := c.press(t, r, sessionID, blackjackActionStand)

	if resp.Data.Content != casinoSettleFailedMessage {
		t.Errorf("a refused payout says %q, want %q", resp.Data.Content, casinoSettleFailedMessage)
	}
	retry := highLowRetryButtonOf(t, resp)
	if _, _, action, ok := ParseCustomID(retry.CustomID); !ok || action != casinoActionSettle {
		t.Fatalf("the button under a refused payout is %q, want the %q action", retry.CustomID, casinoActionSettle)
	}
	if got := highLowEscrowOf(t, bank); got != 100 {
		t.Errorf("escrow after a refused payout: got %d, want the stake still held", got)
	}

	probe := blackjackProbe(100, seed)
	if err := probe.Stand(); err != nil {
		t.Fatalf("probe stand: %v", err)
	}

	bank.settleErr = nil
	resp = c.press(t, r, sessionID, casinoActionSettle)
	if bank.settles != 2 {
		t.Errorf("SettleGame was called %d times, want 2 (one refusal, one retry)", bank.settles)
	}
	if got := highLowEscrowOf(t, bank); got != 0 {
		t.Errorf("escrow after the retry: got %d, want 0", got)
	}
	// The SAME hand is paid: the retry never deals or draws again.
	if body := blackjackBodyOf(t, resp); !strings.Contains(body, blackjackHand(probe.Player())) || !strings.Contains(body, blackjackHand(probe.Dealer())) {
		t.Errorf("the retry paid a different hand than the one that finished:\n%s", body)
	}
	if got, want := highLowChipsOf(t, bank), int64(900)+probe.Settle(); got != want {
		t.Errorf("chips after the retry: got %d, want %d", got, want)
	}
}

// 完了条件の順序: EnsureCasinoAccess → Store.OpenGame → DefaultSessions().Open,
// and a hand that cannot be opened after the stake moved gives it back.
func TestBlackjackStakesBeforeTheHandAndRefundsWhenTheHandCannotOpen(t *testing.T) {
	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{}

	if _, err := c.sessions.Open("g1", "u1", casino.GameBlackjack, casino.NewBlackjack(10, zeroRng{}), casino.MessageRef{}); err != nil {
		t.Fatalf("seeding a live hand: %v", err)
	}

	if err := c.handle(r, blackjackSlashInteraction(100)); err != nil {
		t.Fatalf("/blackjack: %v", err)
	}
	if got, want := r.last(t).Data.Content, blackjackInProgressMessage; got != want {
		t.Errorf("refusal: got %q, want %q", got, want)
	}
	if bank.settles != 1 {
		t.Fatalf("SettleGame was called %d times, want 1: the stake moved before the hand was opened and must come back", bank.settles)
	}
	if got := highLowEscrowOf(t, bank); got != 0 {
		t.Errorf("escrow after the refusal: got %d, want 0", got)
	}
	if got := highLowChipsOf(t, bank); got != 1000 {
		t.Errorf("chips after the refusal: got %d, want the welcome bonus back (1000)", got)
	}
}

// Discord can create the message and still fail this call. By the time the
// error arrives the hand may have been played, settled and replaced by the
// next game — whose stake is what sits in escrow now.
func TestBlackjackDoesNotRefundAStakeItNoLongerOwns(t *testing.T) {
	seed := blackjackSeedWhere(t, "a playable hand", func(g *casino.BlackjackGame) bool {
		return g.State() == casino.BlackjackPlaying
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	r := &fakeCasinoResponder{respondErr: errors.New("the reply never arrived")}
	r.onRespond = func(resp *discordgo.InteractionResponse) {
		if _, sessionID, _, ok := ParseCustomID(blackjackButtonsOf(t, resp)[0].CustomID); ok {
			c.sessions.Close(sessionID)
		}
	}

	if err := c.handle(r, blackjackSlashInteraction(100)); err == nil {
		t.Fatal("/blackjack reported success although the reply failed")
	}
	if bank.settles != 0 {
		t.Errorf("SettleGame was called %d times, want 0: the escrow belongs to whoever closed the session", bank.settles)
	}
	if got := highLowEscrowOf(t, bank); got != 100 {
		t.Errorf("escrow: got %d, want the stake (100) left for its real owner", got)
	}
}

// --- 評価者の指摘(反復 7)-------------------------------------------------

// stakeWatchingBank lets a test stop the world at the one moment ⏫ is
// half-applied: the chips have moved into escrow and the hand has not yet
// counted them. Nothing else can observe that gap — it lives between two
// calls in stakeDouble.
type stakeWatchingBank struct {
	*flakyBank
	onAddToEscrow func()
}

func (b *stakeWatchingBank) AddToEscrow(guildID, userID string, amount int64) error {
	if err := b.flakyBank.AddToEscrow(guildID, userID, amount); err != nil {
		return err
	}
	if b.onAddToEscrow != nil {
		b.onAddToEscrow()
	}
	return nil
}

// A failed start reply must not retire a hand somebody is pressing. The order
// below is the one that loses a raise: the double's chips are in escrow, the
// hand has not applied them, and the start path decides the hand is dead.
// Closing it there refunds the original bet only, and the ⏫ that paid the
// second one finds no session to charge it to.
func TestBlackjackDoesNotCloseAHandWhileADoubleIsBeingStaked(t *testing.T) {
	seed := blackjackSeedWhere(t, "a hand that may double", func(g *casino.BlackjackGame) bool {
		return g.CanDouble()
	})
	c, bank := newBlackjackCommandForTest(t, seed)
	watching := &stakeWatchingBank{flakyBank: bank}
	c.store = watching

	// What the doubled hand is worth, computed from the same shoe.
	probe := blackjackProbe(100, seed)
	if _, err := probe.Double(); err != nil {
		t.Fatalf("probing the double: %v", err)
	}
	wantPayout := probe.Settle()

	var (
		boardSent = make(chan string, 1)
		staked    = make(chan struct{})
		closed    = make(chan struct{})
		pressed   = make(chan error, 1)
	)

	watching.onAddToEscrow = func() {
		close(staked)
		// Give the start path every chance to close the session here. It
		// can only take it by ignoring this board's lock; holding the lock
		// it waits for the press, and this wait ends on the timeout.
		select {
		case <-closed:
		case <-time.After(500 * time.Millisecond):
		}
	}

	starter := &fakeCasinoResponder{respondErr: errors.New("the reply never arrived")}
	starter.onRespond = func(resp *discordgo.InteractionResponse) {
		_, sessionID, _, ok := ParseCustomID(blackjackButtonsOf(t, resp)[0].CustomID)
		if !ok {
			t.Fatal("the hand's first button does not carry a parsable custom_id")
		}
		boardSent <- sessionID
		<-staked // the reply fails only once the raise is in escrow
	}

	go func() {
		sessionID := <-boardSent
		presser := &fakeCasinoResponder{}
		pressed <- c.handleComponent(presser, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, blackjackActionDouble), "u1"), sessionID, blackjackActionDouble)
	}()

	err := c.handle(starter, blackjackSlashInteraction(100))
	close(closed)
	if err == nil {
		t.Fatal("/blackjack reported success although the reply failed")
	}
	if pressErr := <-pressed; pressErr != nil {
		t.Fatalf("⏫ ダブル: %v", pressErr)
	}

	if got := highLowEscrowOf(t, bank); got != 0 {
		t.Errorf("escrow: got %d, want 0 — the doubled hand settled, nothing stays staked", got)
	}
	if got, want := highLowChipsOf(t, bank), 1000-2*int64(100)+wantPayout; got != want {
		t.Errorf("chips: got %d, want %d (1000 - ダブルで 200 + 配当 %d)", got, want, wantPayout)
	}
	if bank.settles != 1 {
		t.Errorf("SettleGame was called %d times, want 1: the hand pays once and the start path refunds nothing it no longer owns", bank.settles)
	}
}
