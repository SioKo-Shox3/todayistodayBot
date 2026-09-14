package commands

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

// --- fixtures --------------------------------------------------------------

// zeroRng is a deterministic randSource: every shuffle it drives produces
// the same deck, so a test can deal the SAME deck twice — once into the game
// under test, once into a plain Deck it reads the draw order from.
type zeroRng struct{}

func (zeroRng) Float64() float64 { return 0 }
func (zeroRng) Intn(int) int     { return 0 }

// highLowDealOrder returns the cards of a zeroRng deck in the order they are
// drawn: [0] is the game's opening card, [1] the card the first guess turns
// over, and so on.
func highLowDealOrder(t *testing.T) []casino.Card {
	t.Helper()
	deck := casino.NewDeck(1, zeroRng{})
	order := make([]casino.Card, 0, deck.Len())
	for {
		card, ok := deck.Draw()
		if !ok {
			return order
		}
		order = append(order, card)
	}
}

// highLowRequireDeal pins the cards the fixed deck deals, in draw order.
// The zeroRng deck opens 2♠, A♣, K♣, Q♣, … and the button tests below are
// written around exactly that: a change to NewDeck must fail here, loudly,
// instead of quietly turning a "losing guess" test into a winning one.
func highLowRequireDeal(t *testing.T, want ...casino.Card) {
	t.Helper()
	order := highLowDealOrder(t)
	for idx, card := range want {
		if order[idx] != card {
			t.Fatalf("the fixed deck deals %s at position %d, want %s — these fixtures assume the zeroRng deck", order[idx], idx, card)
		}
	}
}

// Cards of the fixed deal, named so the fixtures read like the game.
var (
	highLowCard2Spade = casino.Card{Rank: 2, Suit: casino.SuitSpade}
	highLowCardAClub  = casino.Card{Rank: casino.MaxRank, Suit: casino.SuitClub}
	highLowCardKClub  = casino.Card{Rank: 13, Suit: casino.SuitClub}
	highLowCardQClub  = casino.Card{Rank: 12, Suit: casino.SuitClub}
)

// press drives one button of a live board as its owner.
func (c *HighLowCommand) press(t *testing.T, r *fakeHighLowResponder, sessionID, action string) *discordgo.InteractionResponse {
	t.Helper()
	if err := c.handleComponent(r, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, action), "u1"), sessionID, action); err != nil {
		t.Fatalf("press %q: %v", action, err)
	}
	return r.last(t)
}

// fakeHighLowResponder records what the handler sent, in order, and lets a
// test run an assertion at the exact moment of each call — which is how the
// settlement/edit ordering is pinned.
type fakeHighLowResponder struct {
	responses  []*discordgo.InteractionResponse
	sends      []string
	followups  []*discordgo.WebhookParams
	message    *discordgo.Message
	respondErr error
	lookupErr  error
	onRespond  func(*discordgo.InteractionResponse)
}

func (f *fakeHighLowResponder) InteractionRespond(_ *discordgo.Interaction, resp *discordgo.InteractionResponse, _ ...discordgo.RequestOption) error {
	if f.onRespond != nil {
		f.onRespond(resp)
	}
	f.responses = append(f.responses, resp)
	return f.respondErr
}

func (f *fakeHighLowResponder) InteractionResponse(_ *discordgo.Interaction, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	if f.message == nil {
		return &discordgo.Message{ID: "m1", ChannelID: "c1"}, nil
	}
	return f.message, nil
}

func (f *fakeHighLowResponder) ChannelMessageSend(_ string, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.sends = append(f.sends, content)
	return &discordgo.Message{ID: "m2"}, nil
}

func (f *fakeHighLowResponder) FollowupMessageCreate(_ *discordgo.Interaction, _ bool, data *discordgo.WebhookParams, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.followups = append(f.followups, data)
	return &discordgo.Message{ID: "m3"}, nil
}

func (f *fakeHighLowResponder) last(t *testing.T) *discordgo.InteractionResponse {
	t.Helper()
	if len(f.responses) == 0 {
		t.Fatal("the handler sent no response")
	}
	return f.responses[len(f.responses)-1]
}

// newHighLowCommandForTest wires a command onto this test's own store and
// its own session manager — never casino.Default()/DefaultSessions(), which
// point at the production file and at the process-wide board registry.
func newHighLowCommandForTest(t *testing.T) *HighLowCommand {
	t.Helper()
	return &HighLowCommand{
		store:    casino.New(filepath.Join(t.TempDir(), "casino.json")),
		sessions: casino.NewSessionManager(time.Now, casino.DefaultSessionTTL),
		newGame:  func(bet int64) *casino.HighLowGame { return casino.NewHighLow(bet, zeroRng{}) },
	}
}

func highLowSlashInteraction(bet int64) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID:    "1",
		Token: "TEST_TOKEN",
		Type:  discordgo.InteractionApplicationCommand,
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "highlow",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "bet", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(bet)},
			},
		},
		GuildID:   "g1",
		ChannelID: "c1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: "u1"}},
	}}
}

func highLowButtonInteraction(customID, userID string) *discordgo.InteractionCreate {
	i := componentInteraction(customID)
	i.ChannelID = "c1"
	i.Member = &discordgo.Member{User: &discordgo.User{ID: userID}}
	return i
}

// startHighLow runs /highlow and returns the live session's ID.
func startHighLow(t *testing.T, c *HighLowCommand, r *fakeHighLowResponder, bet int64) string {
	t.Helper()
	if err := c.handle(r, highLowSlashInteraction(bet)); err != nil {
		t.Fatalf("/highlow: %v", err)
	}
	_, sessionID, _, ok := ParseCustomID(highLowButtonsOf(t, r.last(t))[0].CustomID)
	if !ok {
		t.Fatal("the board's first button does not carry a parsable custom_id")
	}
	return sessionID
}

// highLowButtonsOf pulls the three buttons out of a response.
func highLowButtonsOf(t *testing.T, resp *discordgo.InteractionResponse) []discordgo.Button {
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
		t.Fatalf("board has %d buttons, want 3 (ハイ / ロー / キャッシュアウト)", len(buttons))
	}
	return buttons
}

func highLowBodyOf(t *testing.T, resp *discordgo.InteractionResponse) string {
	t.Helper()
	if resp.Data == nil || len(resp.Data.Embeds) != 1 {
		t.Fatal("response carries no single embed")
	}
	return resp.Data.Embeds[0].Description
}

// --- display (pure) --------------------------------------------------------

func TestHighLowChoiceLabelShowsChanceAndMultiplier(t *testing.T) {
	// 31 of 51 cards win: 60 %, and 95*51/31 = 156 (x100).
	if got, want := highLowChoiceLabel("⬆️ ハイ", 31, 51, casino.Multiplier(31, 51)), "⬆️ ハイ 60% ×1.56"; got != want {
		t.Errorf("label: got %q, want %q", got, want)
	}
	// A guess no card can win is marked, not priced.
	if got, want := highLowChoiceLabel("⬆️ ハイ", 0, 51, casino.Multiplier(0, 51)), "⬆️ ハイ ―"; got != want {
		t.Errorf("impossible guess label: got %q, want %q", got, want)
	}
	// Two decimals always: ×2 and ×2.05 must not look alike.
	if got, want := formatMultiplierX100(200), "×2.00"; got != want {
		t.Errorf("×2 rendered as %q, want %q", got, want)
	}
	if got, want := formatMultiplierX100(205), "×2.05"; got != want {
		t.Errorf("×2.05 rendered as %q, want %q", got, want)
	}
	// The percentage is truncated, never rounded up: 1 of 3 is 33 %.
	if got := highLowChancePercent(1, 3); got != 33 {
		t.Errorf("chance of 1/3: got %d%%, want 33%%", got)
	}
}

func TestHighLowBoardEmbedShowsCardPotStreakAndBothChoices(t *testing.T) {
	board := highLowBoard{
		Bet: 100, Pot: 155, Streak: 1,
		Current: casino.Card{Rank: 10, Suit: casino.SuitHeart},
		Odds:    casino.HighLowOdds{Remaining: 50, HighCards: 16, LowCards: 31, HighMultiplier: casino.Multiplier(16, 50), LowMultiplier: casino.Multiplier(31, 50)},
	}
	body := highLowBoardEmbed(board, "u1").Description
	for _, want := range []string{
		"現在のカード: **10♥**",
		"ポット: 155枚(ベット 100枚)",
		"連勝: 1",
		"⬆️ ハイ: 32%(16/50枚) ×2.96",
		"⬇️ ロー: 62%(31/50枚) ×1.53",
		"💰 キャッシュアウト: 155枚",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("board body is missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestHighLowButtonsDisableImpossibleGuessesAndFinishedBoards(t *testing.T) {
	// An ace is showing: nothing beats it, so ハイ cannot win.
	board := highLowBoard{
		Bet: 100, Pot: 100,
		Current: casino.Card{Rank: casino.MaxRank, Suit: casino.SuitSpade},
		Odds:    casino.HighLowOdds{Remaining: 51, HighCards: 0, LowCards: 48, HighMultiplier: 0, LowMultiplier: casino.Multiplier(48, 51)},
	}
	buttons := highLowButtonsOf(t, &discordgo.InteractionResponse{Data: &discordgo.InteractionResponseData{Components: highLowButtons("s1", board, false)}})
	if !buttons[0].Disabled {
		t.Error("ハイ is enabled although no remaining card is higher than an ace")
	}
	if buttons[1].Disabled || buttons[2].Disabled {
		t.Error("ロー or キャッシュアウト is disabled on a playable board")
	}

	// A finished board disables the whole row, including a guess that would
	// otherwise be live — that is what stops a second press reaching a
	// settled hand.
	finished := highLowButtonsOf(t, &discordgo.InteractionResponse{Data: &discordgo.InteractionResponseData{Components: highLowButtons("s1", board, true)}})
	for idx, button := range finished {
		if !button.Disabled {
			t.Errorf("button %d (%q) is still enabled on a finished board", idx, button.Label)
		}
	}
}

func TestHighLowResultEmbedNamesEveryEnding(t *testing.T) {
	base := highLowResult{
		Guessed: true, GuessHigh: true,
		Previous: casino.Card{Rank: 7, Suit: casino.SuitDiamond},
		Drawn:    casino.Card{Rank: 3, Suit: casino.SuitSpade},
		Bet:      100, Streak: 2, Balance: 4000,
	}
	for _, tc := range []struct {
		name   string
		result highLowResult
		want   []string
	}{
		{"loss", func() highLowResult { r := base; r.Ending, r.Payout = highLowEndLost, 0; return r }(),
			[]string{"⬆️ ハイ(7♦) → **3♠**", "😢 ハズレ(ベット100枚)", "配当: 0枚 / 残高: 4000枚"}},
		{"cash-out", func() highLowResult {
			r := base
			r.Ending, r.Guessed, r.Payout = highLowEndCashedOut, false, 155
			return r
		}(),
			[]string{"💰 キャッシュアウト(2連勝)", "配当: 155枚 / 残高: 4000枚"}},
		{"streak cap", func() highLowResult {
			r := base
			r.Ending, r.Streak, r.Payout = highLowEndStreakCap, 10, 9000
			return r
		}(),
			[]string{"🏁 10連勝で自動キャッシュアウト!"}},
		{"pot cap", func() highLowResult { r := base; r.Ending, r.Payout = highLowEndPotCap, 10000; return r }(),
			[]string{"🏁 ポットが上限(ベットの100倍)に達したため自動キャッシュアウト!"}},
	} {
		body := highLowResultEmbed(tc.result).Description
		for _, want := range tc.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s: result body is missing %q\n--- body ---\n%s", tc.name, want, body)
			}
		}
		// A cash-out turns no card over, so it must not claim one.
		if !tc.result.Guessed && strings.Contains(body, "→") {
			t.Errorf("%s: a cash-out body claims a drawn card\n%s", tc.name, body)
		}
	}
}

func TestHighLowCelebrationOnlyForLongPaidStreaks(t *testing.T) {
	paid := highLowResult{Ending: highLowEndCashedOut, Payout: 5000, Streak: highLowCelebrationStreak}
	got := highLowCelebration("u1", paid)
	for _, want := range []string{"<@u1>", "7連勝", "5000枚"} {
		if !strings.Contains(got, want) {
			t.Errorf("celebration %q is missing %q", got, want)
		}
	}
	short := paid
	short.Streak = highLowCelebrationStreak - 1
	if msg := highLowCelebration("u1", short); msg != "" {
		t.Errorf("a %d-streak cash-out announced itself: %q", short.Streak, msg)
	}
	lost := paid
	lost.Ending, lost.Payout = highLowEndLost, 0
	if msg := highLowCelebration("u1", lost); msg != "" {
		t.Errorf("a losing board announced itself: %q", msg)
	}
}

// --- custom_id -------------------------------------------------------------

func TestHighLowCustomIDRoundTripForEveryAction(t *testing.T) {
	c := newHighLowCommandForTest(t)
	// A real session ID (32 hex characters), not a short stand-in: the ID
	// travels inside the 100-character custom_id budget.
	session, err := c.sessions.Open("g1", "u1", casino.GameHighLow, c.newGame(100), casino.MessageRef{ChannelID: "c1"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, action := range []string{highLowActionHigh, highLowActionLow, highLowActionCashOut} {
		id := BuildCustomID(c.Prefix(), session.ID, action)
		game, gotSession, gotAction, ok := ParseCustomID(id)
		if !ok {
			t.Fatalf("ParseCustomID(%q): ok=false", id)
		}
		if game != c.Prefix() || gotSession != session.ID || gotAction != action {
			t.Errorf("round trip of %q: got (%q, %q, %q), want (%q, %q, %q)", id, game, gotSession, gotAction, c.Prefix(), session.ID, action)
		}
	}
}

func TestHighLowPrefixMatchesTheSessionGameKind(t *testing.T) {
	c := newHighLowCommandForTest(t)
	if c.Prefix() != string(casino.GameHighLow) {
		t.Fatalf("Prefix() = %q, want %q — the button namespace and the session's GameKind must be the same string", c.Prefix(), casino.GameHighLow)
	}
}

// --- /highlow --------------------------------------------------------------

func TestHighLowCommandDefinitionIsGuildOnlyWithABetOption(t *testing.T) {
	def := newHighLowCommandForTest(t).Definition()
	if def.Contexts == nil || len(*def.Contexts) != 1 || (*def.Contexts)[0] != discordgo.InteractionContextGuild {
		t.Errorf("Contexts = %v, want guild-only", def.Contexts)
	}
	if len(def.Options) != 1 || def.Options[0].Name != "bet" || !def.Options[0].Required {
		t.Fatalf("options = %+v, want one required bet option", def.Options)
	}
	if def.Options[0].MinValue == nil || *def.Options[0].MinValue != highLowMinBet || def.Options[0].MaxValue != highLowMaxBet {
		t.Errorf("bet bounds = [%v, %v], want [%d, %d]", def.Options[0].MinValue, def.Options[0].MaxValue, highLowMinBet, highLowMaxBet)
	}
}

func TestHighLowCommandStartsABoardAndStakesTheBet(t *testing.T) {
	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	if err := c.store.EnsureCasinoAccess("g1", "u1", time.Now()); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	before, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}

	sessionID := startHighLow(t, c, r, 100)

	view, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Escrow != 100 {
		t.Errorf("escrow = %d, want the 100-chip stake", view.Account.Escrow)
	}
	if got, want := view.Account.Chips, before.Account.Chips-100; got != want {
		t.Errorf("chips = %d, want %d (the stake left Chips)", got, want)
	}
	if got, want := view.TotalAssets, before.TotalAssets; got != want {
		t.Errorf("total assets = %d, want %d — staking must not change what the player owns", got, want)
	}
	if _, live := c.sessions.Get(sessionID); !live {
		t.Fatal("no live session after /highlow")
	}
	// The board is public (no ephemeral flag) and carries the opening card.
	resp := r.last(t)
	if resp.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Error("the board was sent ephemerally; 設計書 §8 says the board is public")
	}
	opening := highLowDealOrder(t)[0]
	if body := highLowBodyOf(t, resp); !strings.Contains(body, opening.String()) {
		t.Errorf("board body does not show the opening card %s\n%s", opening, body)
	}
	// The message ID is remembered for the idle sweeper.
	session, _ := c.sessions.Get(sessionID)
	if session.Ref.MessageID != "m1" || session.Ref.ChannelID != "c1" {
		t.Errorf("session Ref = %+v, want the board's channel and message", session.Ref)
	}
}

func TestHighLowCommandRefusesASecondBoard(t *testing.T) {
	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	startHighLow(t, c, r, 100)

	if err := c.handle(r, highLowSlashInteraction(100)); err != nil {
		t.Fatalf("second /highlow: %v", err)
	}
	if got := r.last(t).Data.Content; got != highLowInProgressMessage {
		t.Errorf("second board answered %q, want %q", got, highLowInProgressMessage)
	}
	view, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Escrow != 100 {
		t.Errorf("escrow = %d, want the FIRST stake only — the refused board must not stake anything", view.Account.Escrow)
	}
}

func TestHighLowCommandRefusesABetOutOfRange(t *testing.T) {
	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	if err := c.handle(r, highLowSlashInteraction(highLowMaxBet+1)); err != nil {
		t.Fatalf("/highlow: %v", err)
	}
	if got := r.last(t).Data.Content; got != highLowBetRangeMessage {
		t.Errorf("out-of-range bet answered %q, want %q", got, highLowBetRangeMessage)
	}
	if c.sessions.Len() != 0 {
		t.Errorf("a refused bet left %d sessions open", c.sessions.Len())
	}
}

func TestHighLowCommandRefundsWhenTheBoardNeverReachesDiscord(t *testing.T) {
	c := newHighLowCommandForTest(t)
	if err := c.store.EnsureCasinoAccess("g1", "u1", time.Now()); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	before, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}

	r := &fakeHighLowResponder{respondErr: errors.New("https://discord.com/api/v9/interactions/1/SECRET_TOKEN/callback: 500")}
	err = c.handle(r, highLowSlashInteraction(100))
	if err == nil {
		t.Fatal("a failed board delivery reported success")
	}
	if strings.Contains(err.Error(), "SECRET_TOKEN") {
		t.Errorf("the returned error leaks the interaction token: %v", err)
	}
	view, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Escrow != 0 {
		t.Errorf("escrow = %d after an undelivered board, want 0 — nothing can ever settle it", view.Account.Escrow)
	}
	if view.Account.Chips != before.Account.Chips {
		t.Errorf("chips = %d, want the pre-bet %d (the stake must come back)", view.Account.Chips, before.Account.Chips)
	}
	if c.sessions.Len() != 0 {
		t.Errorf("an undelivered board left %d sessions open", c.sessions.Len())
	}
}

// --- buttons ---------------------------------------------------------------

func TestHighLowComponentRefusesAnotherPlayersButton(t *testing.T) {
	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)

	if err := c.handleComponent(r, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, highLowActionCashOut), "someone-else"), sessionID, highLowActionCashOut); err != nil {
		t.Fatalf("HandleComponent: %v", err)
	}
	resp := r.last(t)
	if resp.Data.Content != notSessionOwnerMessage {
		t.Errorf("answered %q, want %q", resp.Data.Content, notSessionOwnerMessage)
	}
	if resp.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Error("the refusal was public; 設計書 §9 says ephemeral")
	}
	if resp.Type == discordgo.InteractionResponseUpdateMessage {
		t.Error("a stranger's press edited the board")
	}
	if _, live := c.sessions.Get(sessionID); !live {
		t.Error("a stranger's press closed the owner's board")
	}
	view, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Escrow != 100 {
		t.Errorf("escrow = %d, want the stake untouched by a stranger's press", view.Account.Escrow)
	}
}

func TestHighLowComponentTellsAPressOnASettledBoardItIsOver(t *testing.T) {
	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)

	// First press cashes out and settles the hand.
	if err := c.handleComponent(r, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, highLowActionCashOut), "u1"), sessionID, highLowActionCashOut); err != nil {
		t.Fatalf("first press: %v", err)
	}
	settledChips := func() int64 {
		view, err := c.store.ViewAccount("g1", "u1", time.Now())
		if err != nil {
			t.Fatalf("ViewAccount: %v", err)
		}
		return view.Account.Chips
	}
	after := settledChips()

	// Second press on the same (now unknown) session.
	if err := c.handleComponent(r, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, highLowActionCashOut), "u1"), sessionID, highLowActionCashOut); err != nil {
		t.Fatalf("second press: %v", err)
	}
	resp := r.last(t)
	if resp.Data.Content != highLowSessionOverMessage {
		t.Errorf("second press answered %q, want %q", resp.Data.Content, highLowSessionOverMessage)
	}
	if resp.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Error("the ⌛ notice was public, not ephemeral")
	}
	if got := settledChips(); got != after {
		t.Errorf("chips moved from %d to %d on the second press — the hand paid twice", after, got)
	}
}

func TestHighLowComponentCashOutPaysThePotAndDisablesTheButtons(t *testing.T) {
	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	if err := c.store.EnsureCasinoAccess("g1", "u1", time.Now()); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	before, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	sessionID := startHighLow(t, c, r, 100)

	if err := c.handleComponent(r, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, highLowActionCashOut), "u1"), sessionID, highLowActionCashOut); err != nil {
		t.Fatalf("cash-out: %v", err)
	}
	resp := r.last(t)
	if resp.Type != discordgo.InteractionResponseUpdateMessage {
		t.Errorf("cash-out responded with type %v, want an edit of the board message", resp.Type)
	}
	for idx, button := range highLowButtonsOf(t, resp) {
		if !button.Disabled {
			t.Errorf("button %d (%q) is still pressable after the cash-out", idx, button.Label)
		}
	}
	// An untouched board's cash-out is a full refund (設計書 §6).
	view, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Escrow != 0 {
		t.Errorf("escrow = %d after settling, want 0", view.Account.Escrow)
	}
	if view.Account.Chips != before.Account.Chips {
		t.Errorf("chips = %d, want the full refund back to %d", view.Account.Chips, before.Account.Chips)
	}
	if body := highLowBodyOf(t, resp); !strings.Contains(body, "配当: 100枚") {
		t.Errorf("result body does not report the 100-chip payout\n%s", body)
	}
	if len(r.sends) != 0 {
		t.Errorf("a 0-streak cash-out posted a celebration: %q", r.sends)
	}
}

// TestHighLowComponentSettlesBeforeEditingTheMessage pins the order the
// escrow discipline depends on: chips are the part that must survive a
// crash, so they are persisted BEFORE the message the player sees changes.
// The fake responder reads the store at the exact moment of the edit.
func TestHighLowComponentSettlesBeforeEditingTheMessage(t *testing.T) {
	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)

	var escrowAtEditTime int64 = -1
	var editSeen bool
	r.onRespond = func(resp *discordgo.InteractionResponse) {
		if resp.Type != discordgo.InteractionResponseUpdateMessage {
			return
		}
		editSeen = true
		view, err := c.store.ViewAccount("g1", "u1", time.Now())
		if err != nil {
			t.Errorf("ViewAccount during the edit: %v", err)
			return
		}
		escrowAtEditTime = view.Account.Escrow
	}

	if err := c.handleComponent(r, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, highLowActionCashOut), "u1"), sessionID, highLowActionCashOut); err != nil {
		t.Fatalf("cash-out: %v", err)
	}
	if !editSeen {
		t.Fatal("the board was never edited")
	}
	if escrowAtEditTime != 0 {
		t.Errorf("escrow was still %d when the message was edited — the settlement must be persisted first", escrowAtEditTime)
	}
}

func TestHighLowComponentGuessAdvancesOrEndsTheBoard(t *testing.T) {
	highLowRequireDeal(t, highLowCard2Spade, highLowCardAClub)

	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)

	// 2♠ then A♣: ハイ wins, the board stays alive and the pot grows.
	resp := c.press(t, r, sessionID, highLowActionHigh)
	if resp.Type != discordgo.InteractionResponseUpdateMessage {
		t.Fatalf("guess responded with type %v, want an edit", resp.Type)
	}
	body := highLowBodyOf(t, resp)
	for _, want := range []string{"現在のカード: **" + highLowCardAClub.String() + "**", "連勝: 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("board body is missing %q after a winning guess\n%s", want, body)
		}
	}
	if _, live := c.sessions.Get(sessionID); !live {
		t.Fatal("a winning guess closed the board")
	}
	buttons := highLowButtonsOf(t, resp)
	if !buttons[0].Disabled {
		t.Error("ハイ is pressable with an ace showing — no card can beat it")
	}
	if buttons[1].Disabled || buttons[2].Disabled {
		t.Error("ロー or キャッシュアウト is disabled on a board that is still in play")
	}
	view, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Escrow != 100 {
		t.Errorf("escrow = %d mid-hand, want the stake still held", view.Account.Escrow)
	}
	if view.Account.Chips != 0 && strings.Contains(body, "配当") {
		t.Error("a mid-hand board is showing a payout line")
	}
}

func TestHighLowComponentLosingGuessSettlesAtZero(t *testing.T) {
	highLowRequireDeal(t, highLowCard2Spade, highLowCardAClub, highLowCardKClub, highLowCardQClub)

	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	if err := c.store.EnsureCasinoAccess("g1", "u1", time.Now()); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	before, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	sessionID := startHighLow(t, c, r, 100)

	c.press(t, r, sessionID, highLowActionHigh)         // 2♠ → A♣: win
	c.press(t, r, sessionID, highLowActionLow)          // A♣ → K♣: win
	resp := c.press(t, r, sessionID, highLowActionHigh) // K♣ → Q♣: lost

	body := highLowBodyOf(t, resp)
	for _, want := range []string{"😢 ハズレ", "配当: 0枚", highLowCardQClub.String()} {
		if !strings.Contains(body, want) {
			t.Errorf("a lost board is missing %q\n%s", want, body)
		}
	}
	for idx, button := range highLowButtonsOf(t, resp) {
		if !button.Disabled {
			t.Errorf("button %d (%q) is still pressable after the loss", idx, button.Label)
		}
	}
	if _, live := c.sessions.Get(sessionID); live {
		t.Error("a lost board is still addressable")
	}
	view, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Escrow != 0 {
		t.Errorf("escrow = %d after a loss, want 0", view.Account.Escrow)
	}
	if got, want := view.Account.Chips, before.Account.Chips-100; got != want {
		t.Errorf("chips = %d, want %d (the stake is lost, nothing more)", got, want)
	}
}

func TestHighLowComponentRejectsAGuessNoCardCanWin(t *testing.T) {
	highLowRequireDeal(t, highLowCard2Spade, highLowCardAClub)

	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)
	c.press(t, r, sessionID, highLowActionHigh) // 2♠ → A♣: an ace is now showing

	// The button is disabled in the message, but a stale client can still
	// send the press, so the handler must refuse it too.
	resp := c.press(t, r, sessionID, highLowActionHigh)
	if resp.Data.Content != highLowChoiceBlockedMessage {
		t.Errorf("answered %q, want %q", resp.Data.Content, highLowChoiceBlockedMessage)
	}
	if resp.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Error("the refusal was public, not ephemeral")
	}
	// A refused move leaves the board playable (設計書 §5).
	if _, live := c.sessions.Get(sessionID); !live {
		t.Error("a refused guess closed the board")
	}
	view, err := c.store.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Escrow != 100 {
		t.Errorf("escrow = %d after a refused guess, want the stake still held", view.Account.Escrow)
	}
}

func TestHighLowComponentRejectsAnUnknownAction(t *testing.T) {
	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)

	if err := c.handleComponent(r, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, "surrender"), "u1"), sessionID, "surrender"); err != nil {
		t.Fatalf("unknown action: %v", err)
	}
	if got := r.last(t).Data.Content; got != unknownComponentMessage {
		t.Errorf("answered %q, want %q", got, unknownComponentMessage)
	}
	if _, live := c.sessions.Get(sessionID); !live {
		t.Error("an unknown action closed the board")
	}
}

// --- 評価者の指摘(反復 6)-------------------------------------------------

// flakyBank is a real store that can be told to refuse SettleGame, and that
// counts the settlements it was asked for. Refusing is not reachable from the
// outside — a fixed deck deals no payout big enough to breach the chip cap —
// but "the payout is decided and the chips did not move" is exactly the state
// the retry exists for.
type flakyBank struct {
	*casino.Store
	settleErr error
	settles   int
	// afterAddToEscrow runs once, immediately after a stake has landed on
	// disk. That is the gap 反復 1 の指摘 2 names — AddToEscrow is disk I/O, so
	// it cannot run inside WithSession — and this is how a test gets to act
	// inside it (drive a sweep) instead of hoping to hit it by timing.
	afterAddToEscrow func()
}

func (b *flakyBank) SettleGame(guildID, userID string, payout int64) (casino.SettleResult, error) {
	b.settles++
	if b.settleErr != nil {
		return casino.SettleResult{}, b.settleErr
	}
	return b.Store.SettleGame(guildID, userID, payout)
}

func (b *flakyBank) AddToEscrow(guildID, userID string, amount int64) error {
	if err := b.Store.AddToEscrow(guildID, userID, amount); err != nil {
		return err
	}
	if hook := b.afterAddToEscrow; hook != nil {
		b.afterAddToEscrow = nil // once — the hook must not re-enter its own stake
		hook()
	}
	return nil
}

// newHighLowCommandOnBank is newHighLowCommandForTest with a store the test
// can make fail.
func newHighLowCommandOnBank(t *testing.T) (*HighLowCommand, *flakyBank) {
	t.Helper()
	bank := &flakyBank{Store: casino.New(filepath.Join(t.TempDir(), "casino.json"))}
	return &HighLowCommand{
		store:    bank,
		sessions: casino.NewSessionManager(time.Now, casino.DefaultSessionTTL),
		newGame:  func(bet int64) *casino.HighLowGame { return casino.NewHighLow(bet, zeroRng{}) },
	}, bank
}

// highLowEscrowOf reports what the account has staked right now.
func highLowEscrowOf(t *testing.T, bank *flakyBank) int64 {
	t.Helper()
	view, err := bank.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	return view.Account.Escrow
}

func highLowChipsOf(t *testing.T, bank *flakyBank) int64 {
	t.Helper()
	view, err := bank.ViewAccount("g1", "u1", time.Now())
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	return view.Account.Chips
}

// assertNoAutomaticSettlementPromise fixes the WORDING of a refused payout, not
// only the field it lands in. A settled board has already left the manager
// (WithSession done=true), so the sweeper never looks at it again: the sole
// automatic settlement left is RefundStaleEscrows at the next startup, and that
// returns the escrow rather than paying the pot. A message that told the player
// to wait would therefore promise something that never arrives — the 🔁 retry
// is theirs to press. See §9 of the C-2 design.
func assertNoAutomaticSettlementPromise(t *testing.T, content string) {
	t.Helper()
	for _, promise := range []string{"自動", "次回", "お待ち"} {
		if strings.Contains(content, promise) {
			t.Errorf("a refused payout says %q, which promises a settlement (%q) that never comes", content, promise)
		}
	}
	if !strings.Contains(content, "もう一度") {
		t.Errorf("a refused payout says %q, which never asks for the manual retry", content)
	}
}

// A refused payout must leave something to press. The board is gone from the
// manager, so without a record the stake would sit in escrow until the next
// restart and every later press would answer the ⌛ "already finished" line.
func TestHighLowRetriesOnlyTheSettlementAfterARefusedPayout(t *testing.T) {
	c, bank := newHighLowCommandOnBank(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)
	before := highLowChipsOf(t, bank)

	bank.settleErr = errors.New("the store refused this payout")
	resp := c.press(t, r, sessionID, highLowActionCashOut)

	if resp.Data.Content != casinoSettleFailedMessage {
		t.Errorf("a refused payout says %q, want %q", resp.Data.Content, casinoSettleFailedMessage)
	}
	assertNoAutomaticSettlementPromise(t, resp.Data.Content)
	retry := highLowRetryButtonOf(t, resp)
	if _, _, action, ok := ParseCustomID(retry.CustomID); !ok || action != casinoActionSettle {
		t.Fatalf("the button under a refused payout is %q, want the %q action", retry.CustomID, casinoActionSettle)
	}
	if retry.Disabled {
		t.Error("the retry button is disabled, so the stake can never be recovered")
	}
	if got := highLowEscrowOf(t, bank); got != 100 {
		t.Errorf("escrow after a refused payout: got %d, want the stake (100) still held", got)
	}

	// The press that follows pays the SAME payout: the board is not replayed.
	bank.settleErr = nil
	resp = c.press(t, r, sessionID, casinoActionSettle)
	if bank.settles != 2 {
		t.Errorf("SettleGame was called %d times, want 2 (one refusal, one retry)", bank.settles)
	}
	if got, want := highLowChipsOf(t, bank), before+100; got != want {
		t.Errorf("chips after the retry: got %d, want the stake back (%d)", got, want)
	}
	if got := highLowEscrowOf(t, bank); got != 0 {
		t.Errorf("escrow after the retry: got %d, want 0", got)
	}
	if body := highLowBodyOf(t, resp); !strings.Contains(body, "配当: 100枚") {
		t.Errorf("the retry's result does not report the payout: %q", body)
	}
	for _, button := range highLowButtonsOf(t, resp) {
		if !button.Disabled {
			t.Errorf("button %q is still live after the board was paid", button.Label)
		}
	}
}

// highLowRetryButtonOf pulls the single 🔁 button off a refused settlement.
func highLowRetryButtonOf(t *testing.T, resp *discordgo.InteractionResponse) discordgo.Button {
	t.Helper()
	if resp.Data == nil || len(resp.Data.Components) != 1 {
		t.Fatal("the refusal carries no component row")
	}
	row, ok := resp.Data.Components[0].(discordgo.ActionsRow)
	if !ok || len(row.Components) != 1 {
		t.Fatalf("the refusal carries %T, want one row holding one button", resp.Data.Components[0])
	}
	button, ok := row.Components[0].(discordgo.Button)
	if !ok {
		t.Fatalf("the refusal's component is %T, want discordgo.Button", row.Components[0])
	}
	return button
}

// Discord can create the message and still fail this call. By the time the
// error arrives the board may have been played, settled and replaced by the
// next game — whose stake is what sits in escrow now. Refunding then would
// pay the new game's chips out for the old game's bet.
func TestHighLowDoesNotRefundAStakeItNoLongerOwns(t *testing.T) {
	c, bank := newHighLowCommandOnBank(t)
	r := &fakeHighLowResponder{respondErr: errors.New("the reply never arrived")}
	r.onRespond = func(resp *discordgo.InteractionResponse) {
		// Stand in for "somebody settled this board while the reply was in
		// flight": the session is no longer ours to close.
		if _, sessionID, _, ok := ParseCustomID(highLowButtonsOf(t, resp)[0].CustomID); ok {
			c.sessions.Close(sessionID)
		}
	}

	if err := c.handle(r, highLowSlashInteraction(100)); err == nil {
		t.Fatal("/highlow reported success although the reply failed")
	}
	if bank.settles != 0 {
		t.Errorf("SettleGame was called %d times, want 0: the escrow belongs to whoever closed the session", bank.settles)
	}
	if got := highLowEscrowOf(t, bank); got != 100 {
		t.Errorf("escrow: got %d, want the stake (100) left for its real owner", got)
	}
}

// Two presses on one board must not interleave their replies: a reply that
// overtakes a later one repaints a finished board as a playable one.
func TestHighLowSerializesThePressesOfOneBoard(t *testing.T) {
	c, _ := newHighLowCommandOnBank(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)

	second := make(chan struct{})
	presses := 0
	r.onRespond = func(*discordgo.InteractionResponse) {
		presses++
		if presses != 1 { // onRespond was hung on AFTER the board itself went out
			return
		}
		go func() {
			defer close(second)
			_ = c.handleComponent(&fakeHighLowResponder{}, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, highLowActionCashOut), "u1"), sessionID, highLowActionCashOut)
		}()
		select {
		case <-second:
			t.Error("a second press ran the board while the first press was still replying")
		case <-time.After(50 * time.Millisecond):
		}
	}

	c.press(t, r, sessionID, highLowActionHigh)
	<-second // the second press may only finish once the first has let go
}

// 完了条件の順序: EnsureCasinoAccess → Store.OpenGame → DefaultSessions().Open.
// The persisted half of "one game per person" is the half that survives a
// restart, so it takes the stake first — and a board that cannot be opened
// after that gives it straight back.
func TestHighLowStakesBeforeTheBoardAndRefundsWhenTheBoardCannotOpen(t *testing.T) {
	c, bank := newHighLowCommandOnBank(t)
	r := &fakeHighLowResponder{}
	before := highLowChipsOf(t, bank)

	// A board this user already has: sessions.Open will refuse the new one.
	if _, err := c.sessions.Open("g1", "u1", casino.GameHighLow, casino.NewHighLow(10, zeroRng{}), casino.MessageRef{}); err != nil {
		t.Fatalf("seeding a live board: %v", err)
	}

	if err := c.handle(r, highLowSlashInteraction(100)); err != nil {
		t.Fatalf("/highlow: %v", err)
	}
	if got, want := r.last(t).Data.Content, highLowInProgressMessage; got != want {
		t.Errorf("refusal: got %q, want %q", got, want)
	}
	if bank.settles != 1 {
		t.Fatalf("SettleGame was called %d times, want 1: the stake moved before the board was opened and must come back", bank.settles)
	}
	if got := highLowEscrowOf(t, bank); got != 0 {
		t.Errorf("escrow after the refusal: got %d, want 0", got)
	}
	if got := highLowChipsOf(t, bank); got != before {
		t.Errorf("chips after the refusal: got %d, want %d", got, before)
	}
}

func TestHighLowRedrawsInsteadOfGuessingAfterALostEdit(t *testing.T) {
	highLowRequireDeal(t, highLowCard2Spade, highLowCardAClub, highLowCardKClub)

	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)

	// The guess lands on the board but its edit never reaches Discord: the
	// message still shows 2♠ while the game holds A♣.
	r.respondErr = errors.New("the edit never reached Discord")
	if err := c.handleComponent(r, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, highLowActionHigh), "u1"), sessionID, highLowActionHigh); err == nil {
		t.Fatal("a failed edit was reported as a successful press")
	}
	session, live := c.sessions.Get(sessionID)
	if !live {
		t.Fatal("a failed edit closed the board")
	}
	if !session.NeedsRedraw {
		t.Fatal("the board is not marked for redraw, so the next press would guess against a card the player cannot see")
	}

	// The next press repairs the picture and turns NO card over.
	r.respondErr = nil
	resp := c.press(t, r, sessionID, highLowActionLow)
	if resp.Type != discordgo.InteractionResponseUpdateMessage {
		t.Fatalf("the redraw responded with type %v, want an edit", resp.Type)
	}
	body := highLowBodyOf(t, resp)
	for _, want := range []string{"現在のカード: **" + highLowCardAClub.String() + "**", "連勝: 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("the redraw does not show the CURRENT board (missing %q)\n%s", want, body)
		}
	}
	if strings.Contains(body, "配当") {
		t.Errorf("the redraw settled the board instead of repairing it\n%s", body)
	}
	if len(r.followups) != 1 {
		t.Fatalf("the redraw sent %d private notices, want 1", len(r.followups))
	}
	if r.followups[0].Content != casinoRedrawnMessage {
		t.Errorf("the notice says %q, want %q", r.followups[0].Content, casinoRedrawnMessage)
	}
	if r.followups[0].Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Error("the redraw notice was public, not ephemeral")
	}
	if session, _ := c.sessions.Get(sessionID); session.NeedsRedraw {
		t.Fatal("the flag survived a redraw that landed, so every later press would only redraw")
	}

	// And the press AFTER it plays a card, from the picture the player saw.
	resp = c.press(t, r, sessionID, highLowActionLow) // A♣ → K♣: win
	body = highLowBodyOf(t, resp)
	for _, want := range []string{"現在のカード: **" + highLowCardKClub.String() + "**", "連勝: 2"} {
		if !strings.Contains(body, want) {
			t.Errorf("the press after the redraw did not play its guess (missing %q)\n%s", want, body)
		}
	}
	if len(r.followups) != 1 {
		t.Errorf("a normal press sent %d private notices, want none beyond the redraw's", len(r.followups)-1)
	}
}

func TestHighLowKeepsTheRedrawFlagWhenTheRedrawItselfFails(t *testing.T) {
	highLowRequireDeal(t, highLowCard2Spade, highLowCardAClub)

	c := newHighLowCommandForTest(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)

	r.respondErr = errors.New("the edit never reached Discord")
	if err := c.handleComponent(r, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, highLowActionHigh), "u1"), sessionID, highLowActionHigh); err == nil {
		t.Fatal("a failed edit was reported as a successful press")
	}

	// The repair fails too, so the message in the channel is still stale.
	if err := c.handleComponent(r, highLowButtonInteraction(BuildCustomID(c.Prefix(), sessionID, highLowActionLow), "u1"), sessionID, highLowActionLow); err == nil {
		t.Fatal("a failed redraw was reported as a successful press")
	}
	session, live := c.sessions.Get(sessionID)
	if !live {
		t.Fatal("a failed redraw closed the board")
	}
	if !session.NeedsRedraw {
		t.Error("the flag came down on a redraw that never landed: the next press would guess against the stale card")
	}
	if len(r.followups) != 0 {
		t.Errorf("a redraw that failed still told the player it had updated the board (%d notices)", len(r.followups))
	}

	// The card is still A♣: neither press turned one over.
	r.respondErr = nil
	if body := highLowBodyOf(t, c.press(t, r, sessionID, highLowActionLow)); !strings.Contains(body, "連勝: 1") {
		t.Errorf("the board moved on while its picture was stale\n%s", body)
	}
}

// C2-12: a refused settlement produced no payout and no balance, so the
// closing message must not print a pair of zeroes in their place — a player
// whose pot is still owed to them would read「配当: 0枚」as having lost it.
// The hand itself still shows (blackjack's refusal has said this much since
// C2-07).
func TestHighLowNamesNoAmountsWhileTheSettlementIsRefused(t *testing.T) {
	c, bank := newHighLowCommandOnBank(t)
	r := &fakeHighLowResponder{}
	sessionID := startHighLow(t, c, r, 100)

	bank.settleErr = errors.New("the store refused this payout")
	body := highLowBodyOf(t, c.press(t, r, sessionID, highLowActionCashOut))

	for _, forbidden := range []string{"配当:", "残高:"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the refusal prints %q, but the store produced no such number:\n%s", forbidden, body)
		}
	}
	if !strings.Contains(body, "💰 キャッシュアウト(0連勝)") {
		t.Errorf("the refusal lost the result of the board it is holding:\n%s", body)
	}

	// And the retry that succeeds names both numbers: the silence above is
	// about the refusal, not about high&low.
	bank.settleErr = nil
	body = highLowBodyOf(t, c.press(t, r, sessionID, casinoActionSettle))
	if want := casinoPayoutLine(100, 1000); !strings.Contains(body, want) {
		t.Errorf("the settled retry does not name the money: want %q in\n%s", want, body)
	}
}
