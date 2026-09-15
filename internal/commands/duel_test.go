package commands

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

// --- fixtures --------------------------------------------------------------

const (
	duelChallenger = "u1"
	duelOpponent   = "u2"
)

// newDuelCommandForTest wires a command onto this test's own store and its
// own session manager — never casino.Default()/DefaultSessions(), which point
// at the production file and at the process-wide board registry. The coin is
// decided rather than tossed, so the assertions name one outcome.
func newDuelCommandForTest(t *testing.T, challengerWins bool) (*DuelCommand, *flakyBank) {
	t.Helper()
	bank := &flakyBank{Store: casino.New(filepath.Join(t.TempDir(), "casino.json"))}
	return &DuelCommand{
		store:    bank,
		sessions: casino.NewSessionManager(time.Now, casino.DefaultSessionTTL),
		flip:     func() bool { return challengerWins },
	}, bank
}

func duelSlashInteraction(opponentID string, opponentIsBot bool, bet int64) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID:    "1",
		Token: "TEST_TOKEN",
		Type:  discordgo.InteractionApplicationCommand,
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "duel",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: duelOpponentOption, Type: discordgo.ApplicationCommandOptionUser, Value: opponentID},
				{Name: duelBetOption, Type: discordgo.ApplicationCommandOptionInteger, Value: float64(bet)},
			},
			Resolved: &discordgo.ApplicationCommandInteractionDataResolved{
				Users: map[string]*discordgo.User{opponentID: {ID: opponentID, Bot: opponentIsBot}},
			},
		},
		GuildID:   "g1",
		ChannelID: "c1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: duelChallenger}},
	}}
}

func duelButtonInteraction(customID, userID string) *discordgo.InteractionCreate {
	i := componentInteraction(customID)
	i.ChannelID = "c1"
	i.GuildID = "g1"
	i.Member = &discordgo.Member{User: &discordgo.User{ID: userID}}
	return i
}

// startDuel runs /duel against duelOpponent and returns the live session's ID.
func startDuel(t *testing.T, c *DuelCommand, r *fakeCasinoResponder, bet int64) string {
	t.Helper()
	if err := c.handle(r, duelSlashInteraction(duelOpponent, false, bet)); err != nil {
		t.Fatalf("/duel: %v", err)
	}
	_, sessionID, _, ok := ParseCustomID(duelButtonsOf(t, r.last(t))[0].CustomID)
	if !ok {
		t.Fatal("the challenge's first button does not carry a parsable custom_id")
	}
	return sessionID
}

// duelPress drives one button of a live challenge as userID.
func duelPress(t *testing.T, c *DuelCommand, r *fakeCasinoResponder, sessionID, action, userID string) *discordgo.InteractionResponse {
	t.Helper()
	customID := BuildCustomID(c.Prefix(), sessionID, action)
	if err := c.handleComponent(r, duelButtonInteraction(customID, userID), sessionID, action); err != nil {
		t.Fatalf("press %q as %s: %v", action, userID, err)
	}
	return r.last(t)
}

// duelButtonsOf pulls the two buttons out of a response.
func duelButtonsOf(t *testing.T, resp *discordgo.InteractionResponse) []discordgo.Button {
	t.Helper()
	if resp.Data == nil || len(resp.Data.Components) != 1 {
		t.Fatalf("response carries %d component rows, want 1", len(resp.Data.Components))
	}
	row, isRow := resp.Data.Components[0].(discordgo.ActionsRow)
	if !isRow {
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
	if len(buttons) != 2 {
		t.Fatalf("challenge has %d buttons, want 2 (受ける / 断る)", len(buttons))
	}
	return buttons
}

func duelBodyOf(t *testing.T, resp *discordgo.InteractionResponse) string {
	t.Helper()
	if resp.Data == nil || len(resp.Data.Embeds) != 1 {
		t.Fatal("response carries no single embed")
	}
	return resp.Data.Embeds[0].Description
}

// duelHoldings reads one account's balance back through the store.
func duelHoldings(t *testing.T, bank *flakyBank, userID string) (chips, escrow int64) {
	t.Helper()
	view, err := bank.ViewAccount("g1", userID, time.Now())
	if err != nil {
		t.Fatalf("ViewAccount(%s): %v", userID, err)
	}
	return view.Account.Chips, view.Account.Escrow
}

// duelDrainChips leaves userID holding exactly `keep` chips, by staking the
// rest on a game that pays nothing. Seeding through the store's own paths
// (rather than writing the file) keeps the fixture honest about what a real
// account can look like.
func duelDrainChips(t *testing.T, bank *flakyBank, userID string, keep int64) {
	t.Helper()
	chips, _ := duelHoldings(t, bank, userID)
	loss := chips - keep
	if loss <= 0 {
		t.Fatalf("%s already holds %d chips, cannot drain down to %d", userID, chips, keep)
	}
	if err := bank.OpenGame("g1", userID, string(casino.GameHighLow), testEscrowSession, loss, time.Now()); err != nil {
		t.Fatalf("draining %s: OpenGame: %v", userID, err)
	}
	if _, err := bank.SettleGame("g1", userID, testEscrowSession, 0); err != nil {
		t.Fatalf("draining %s: SettleGame: %v", userID, err)
	}
}

// --- reading the options (pure) --------------------------------------------

// 設計書 §4.5: 自分自身と Bot は断る。The empty case is not decorative — an
// option Discord shaped unexpectedly must refuse the duel, not open one
// against nobody and strand the stake.
func TestDuelTargetRefusalNamesWhoCannotBeChallenged(t *testing.T) {
	tests := []struct {
		name          string
		challengerID  string
		opponentID    string
		opponentIsBot bool
		want          string
	}{
		{name: "別の人間なら通す", challengerID: "u1", opponentID: "u2", want: ""},
		{name: "自分自身", challengerID: "u1", opponentID: "u1", want: duelSelfMessage},
		{name: "Bot", challengerID: "u1", opponentID: "bot", opponentIsBot: true, want: duelBotMessage},
		{name: "相手が取れなかった", challengerID: "u1", opponentID: "", want: duelNoTargetMessage},
		{name: "押した人が取れなかった", challengerID: "", opponentID: "u2", want: duelNoTargetMessage},
		// Both empty must NOT read as "自分自身": the refusal has to say the
		// target is missing, and "" == "" would otherwise make it look like a
		// self-challenge.
		{name: "どちらも空", challengerID: "", opponentID: "", want: duelNoTargetMessage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := duelTargetRefusal(tc.challengerID, tc.opponentID, tc.opponentIsBot); got != tc.want {
				t.Fatalf("duelTargetRefusal = %q, want %q", got, tc.want)
			}
		})
	}
}

// The Bot flag comes from Discord's resolved objects, not from the option
// value, so it is read separately from the ID.
func TestDuelTargetReadsTheIDFromTheOptionAndTheBotFlagFromResolved(t *testing.T) {
	data := duelSlashInteraction("bot-1", true, 100).ApplicationCommandData()
	id, isBot := duelTarget(data)
	if id != "bot-1" || !isBot {
		t.Fatalf("duelTarget = (%q, %v), want (\"bot-1\", true)", id, isBot)
	}

	// No Resolved block: the ID still comes through and the flag defaults to
	// false, because refusing a real player over a missing field is worse
	// than a challenge a bot will simply never answer.
	data.Resolved = nil
	if id, isBot := duelTarget(data); id != "bot-1" || isBot {
		t.Fatalf("without Resolved: duelTarget = (%q, %v), want (\"bot-1\", false)", id, isBot)
	}
}

// A non-string user option and a non-number bet are both shapes discordgo
// would panic on through UserValue/IntValue. They must refuse the command.
func TestDuelOptionsOfAnUnexpectedShapeRefuseRatherThanPanic(t *testing.T) {
	i := duelSlashInteraction(duelOpponent, false, 100)
	data := i.ApplicationCommandData()
	data.Options[0].Value = 42
	data.Options[1].Value = "とても大きい"

	if id, isBot := duelTarget(data); id != "" || isBot {
		t.Fatalf("duelTarget = (%q, %v), want (\"\", false)", id, isBot)
	}
	if bet := duelBet(data); bet != 0 {
		t.Fatalf("duelBet = %d, want 0", bet)
	}
}

// --- the owner check (pure) ------------------------------------------------

// 設計書 §4.5: 押せるのは受け手だけ。The challenger is refused as firmly as a
// stranger — they own the session, but not these buttons.
func TestRequireDuelOpponentAcceptsOnlyTheReceiver(t *testing.T) {
	tests := []struct {
		name       string
		presser    string
		opponentID string
		want       string
	}{
		{name: "受け手", presser: "u2", opponentID: "u2", want: ""},
		{name: "挑戦者", presser: "u1", opponentID: "u2", want: duelNotYoursMessage},
		{name: "無関係の人", presser: "u9", opponentID: "u2", want: duelNotYoursMessage},
		{name: "押した人が空", presser: "", opponentID: "u2", want: duelNotYoursMessage},
		// Both empty must not pass: "" == "" would hand the buttons to anyone.
		{name: "どちらも空", presser: "", opponentID: "", want: duelNotYoursMessage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := duelButtonInteraction(BuildCustomID(string(casino.GameDuel), "s1", duelActionAccept), tc.presser)
			if tc.presser == "" {
				i.Member = nil
			}
			if got := requireDuelOpponent(i, tc.opponentID); got != tc.want {
				t.Fatalf("requireDuelOpponent = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- display (pure) --------------------------------------------------------

func TestDuelChallengeEmbedNamesBothSidesTheBetAndThePot(t *testing.T) {
	board := casino.DuelState{ChallengerID: "u1", OpponentID: "u2", Bet: 150, Stage: casino.DuelPending}
	embed := duelChallengeEmbed(board)

	if embed.Title != duelChallengeTitle {
		t.Errorf("title: got %q, want %q", embed.Title, duelChallengeTitle)
	}
	for _, want := range []string{"<@u2> への挑戦(150 チップ)", "挑戦者: <@u1>", "300枚 を総取り"} {
		if !strings.Contains(embed.Description, want) {
			t.Errorf("challenge body is missing %q: %q", want, embed.Description)
		}
	}
	if embed.Footer == nil || !strings.Contains(embed.Footer.Text, "u2") {
		t.Error("the footer does not say who may press the buttons")
	}
}

// The result prints what the settlement ACTUALLY credited, never a recomputed
// 2 × bet: a winner at the chip cap takes less than the pot, and a message
// claiming chips the balance beside it does not show is worse than a small
// number.
func TestDuelResultLinesUseTheSettlementsOwnNumbers(t *testing.T) {
	board := casino.DuelState{ChallengerID: "u1", OpponentID: "u2", Bet: 100}
	settlement := casino.DuelSettlement{
		ChallengerWins: true, WinnerID: "u1",
		ChallengerPayout: 30, OpponentPayout: 0, // capped: the pot was 200
		ChallengerChips: casino.MaxChips, OpponentChips: 900,
	}
	body := strings.Join(duelResultLines(board, settlement), "\n")

	for _, want := range []string{
		"🪙 コイン: **表**",
		"🏆 勝者: <@u1>",
		"<@u1> " + casinoPayoutLine(30, casino.MaxChips),
		"<@u2> " + casinoPayoutLine(0, 900),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("result body is missing %q: %q", want, body)
		}
	}
	if strings.Contains(body, "200枚") {
		t.Errorf("the result names the uncapped pot instead of what landed: %q", body)
	}
}

// The coin and the winner are drawn from the same bool, so they cannot
// disagree inside one message.
func TestDuelCoinFaceFollowsTheWinner(t *testing.T) {
	if got := duelCoinFace(true); got != "表" {
		t.Errorf("duelCoinFace(true) = %q, want 表", got)
	}
	if got := duelCoinFace(false); got != "裏" {
		t.Errorf("duelCoinFace(false) = %q, want 裏", got)
	}
}

// Both closing messages name the refund: the stake WAS taken when the
// challenge was sent, so a player who sees their balance dip and recover
// needs the reason in the message.
func TestDuelDeclinedAndTimedOutEmbedsNameTheRefund(t *testing.T) {
	board := casino.DuelState{ChallengerID: "u1", OpponentID: "u2", Bet: 250}

	declined := duelDeclinedEmbed(board)
	if declined.Title != duelDeclinedTitle {
		t.Errorf("declined title: got %q, want %q", declined.Title, duelDeclinedTitle)
	}
	if !strings.Contains(declined.Description, "<@u1> へ 250枚 を返金しました。") {
		t.Errorf("declined body does not name the refund: %q", declined.Description)
	}

	timedOut := duelTimedOutEmbed(board)
	if timedOut.Title != duelTimedOutTitle {
		t.Errorf("timed-out title: got %q, want %q", timedOut.Title, duelTimedOutTitle)
	}
	if !strings.Contains(timedOut.Description, "<@u1> へ 250枚 を返金しました。") {
		t.Errorf("timed-out body does not name the refund: %q", timedOut.Description)
	}
}

func TestDuelButtonsCarryTheSessionAndAreDisabledTogether(t *testing.T) {
	live := duelButtons("s1", false)
	row, isRow := live[0].(discordgo.ActionsRow)
	if !isRow {
		t.Fatalf("component row is %T, want discordgo.ActionsRow", live[0])
	}
	wantActions := []string{duelActionAccept, duelActionDecline}
	for idx, component := range row.Components {
		button := component.(discordgo.Button)
		game, sessionID, action, ok := ParseCustomID(button.CustomID)
		if !ok || game != string(casino.GameDuel) || sessionID != "s1" || action != wantActions[idx] {
			t.Errorf("button %d custom_id = %q, want casino:duel:s1:%s", idx, button.CustomID, wantActions[idx])
		}
		if button.Disabled {
			t.Errorf("button %d is disabled on a live challenge", idx)
		}
	}

	for idx, component := range duelButtons("s1", true)[0].(discordgo.ActionsRow).Components {
		if !component.(discordgo.Button).Disabled {
			t.Errorf("button %d is still live on a settled challenge", idx)
		}
	}
}

// --- /duel -----------------------------------------------------------------

func TestDuelStakesTheChallengerAndPublishesTheChallenge(t *testing.T) {
	c, bank := newDuelCommandForTest(t, true)
	r := &fakeCasinoResponder{}
	sessionID := startDuel(t, c, r, 100)

	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 900 || escrow != 100 {
		t.Errorf("challenger: %d chips / %d escrow, want 900 / 100", chips, escrow)
	}
	// The opponent stakes nothing until they accept.
	if chips, escrow := duelHoldings(t, bank, duelOpponent); chips != 1000 || escrow != 0 {
		t.Errorf("opponent: %d chips / %d escrow, want 1000 / 0", chips, escrow)
	}

	resp := r.last(t)
	if resp.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Errorf("response type = %v, want a public message", resp.Type)
	}
	if resp.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Error("the challenge is ephemeral; the opponent has to be able to see it")
	}
	if !strings.Contains(duelBodyOf(t, resp), "<@u2> への挑戦(100 チップ)") {
		t.Errorf("the challenge does not name the opponent: %q", duelBodyOf(t, resp))
	}
	if sessionID == "" || c.sessions.Len() != 1 {
		t.Fatalf("%d challenges are live, want exactly 1", c.sessions.Len())
	}
}

// A refused target must not move a single chip: the refusal happens before
// the stake, so there is nothing to give back.
func TestDuelRefusesSelfAndBotsWithoutStaking(t *testing.T) {
	tests := []struct {
		name       string
		opponentID string
		isBot      bool
		want       string
	}{
		{name: "自分自身", opponentID: duelChallenger, want: duelSelfMessage},
		{name: "Bot", opponentID: "bot-1", isBot: true, want: duelBotMessage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, bank := newDuelCommandForTest(t, true)
			r := &fakeCasinoResponder{}

			if err := c.handle(r, duelSlashInteraction(tc.opponentID, tc.isBot, 100)); err != nil {
				t.Fatalf("/duel: %v", err)
			}

			if got := r.last(t).Data.Content; got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
			if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 1000 || escrow != 0 {
				t.Errorf("challenger: %d chips / %d escrow, want 1000 / 0 — a refused duel must not stake", chips, escrow)
			}
			if c.sessions.Len() != 0 {
				t.Errorf("%d challenges are live after a refusal, want 0", c.sessions.Len())
			}
		})
	}
}

// 設計書 §4.5 refuses a duel when EITHER side is mid-game, and C3B-15 is the
// half that was missing: the opponent's escrow is not seen by OpenGame, which
// only ever looks at the account it debits. Without the check the challenge
// goes up and the challenger's chips are locked for three minutes behind an
// ⚔️ that AcceptDuel can only refuse.
func TestDuelRefusesAnOpponentWhoIsAlreadyPlaying(t *testing.T) {
	c, bank := newDuelCommandForTest(t, true)
	r := &fakeCasinoResponder{}
	if err := bank.OpenGame("g1", duelOpponent, string(casino.GameBlackjack), testEscrowSession, 100, time.Now()); err != nil {
		t.Fatalf("putting the opponent in a game: %v", err)
	}

	if err := c.handle(r, duelSlashInteraction(duelOpponent, false, 100)); err != nil {
		t.Fatalf("/duel: %v", err)
	}

	want := fmt.Sprintf(duelOpponentInProgressFormat, duelOpponent)
	if got := r.last(t).Data.Content; got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}
	// The whole point of refusing at the start: nothing of the challenger's
	// was staked, so there is nothing to sit in escrow for three minutes.
	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 1000 || escrow != 0 {
		t.Errorf("challenger: %d chips / %d escrow, want 1000 / 0 — a refused challenge must not stake", chips, escrow)
	}
	if c.sessions.Len() != 0 {
		t.Errorf("%d challenges are live after the refusal, want 0", c.sessions.Len())
	}
	// The opponent's own game is untouched: this path reads, it does not
	// settle somebody else's hand.
	if chips, escrow := duelHoldings(t, bank, duelOpponent); chips != 900 || escrow != 100 {
		t.Errorf("opponent: %d chips / %d escrow, want 900 / 100 — the read must not move their stake", chips, escrow)
	}
}

// The control for the test above: an opponent who has never touched the
// casino has no account, and having none is not "in a game". The read must
// also not open one for them — being named in someone else's challenge would
// otherwise pay out the 1,000-chip welcome bonus.
func TestDuelStartsAgainstAnOpponentWhoHasNoAccountYet(t *testing.T) {
	c, bank := newDuelCommandForTest(t, true)
	r := &fakeCasinoResponder{}

	sessionID := startDuel(t, c, r, 100)

	if c.sessions.Len() != 1 {
		t.Fatalf("%d challenges are live, want exactly 1", c.sessions.Len())
	}
	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 900 || escrow != 100 {
		t.Errorf("challenger: %d chips / %d escrow, want 900 / 100", chips, escrow)
	}
	// Pressing ⚔️ is what opens the opponent's account (and the welcome bonus
	// that comes with it), not the check at the start.
	if resp := duelPress(t, c, r, sessionID, duelActionAccept, duelOpponent); resp.Data.Embeds[0].Title != duelResultTitle {
		t.Errorf("accept title = %q, want %q", resp.Data.Embeds[0].Title, duelResultTitle)
	}
}

// The check at the start is a courtesy, not the guarantee: three minutes pass
// between the challenge and the ⚔️, and the opponent is free to start a game
// in them. AcceptDuel's own check is the one that keeps the rule, and this
// pins it — the acceptance is refused, and because nothing was persisted the
// challenge stays live with its stake still refundable.
func TestDuelAcceptStillRefusesAnOpponentWhoStartedAGameMeanwhile(t *testing.T) {
	c, bank := newDuelCommandForTest(t, true)
	r := &fakeCasinoResponder{}
	sessionID := startDuel(t, c, r, 100)

	if err := bank.OpenGame("g1", duelOpponent, string(casino.GameBlackjack), testEscrowSession, 100, time.Now()); err != nil {
		t.Fatalf("putting the opponent in a game after the challenge went up: %v", err)
	}

	resp := duelPress(t, c, r, sessionID, duelActionAccept, duelOpponent)

	if resp.Data.Content != duelInProgressMessage {
		t.Errorf("reply = %q, want %q", resp.Data.Content, duelInProgressMessage)
	}
	if c.sessions.Len() != 1 {
		t.Errorf("%d challenges are live after the refused acceptance, want 1 — the stake still needs a board that can release it", c.sessions.Len())
	}
	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 900 || escrow != 100 {
		t.Errorf("challenger: %d chips / %d escrow, want 900 / 100 — a refused acceptance settles nothing", chips, escrow)
	}
	if chips, escrow := duelHoldings(t, bank, duelOpponent); chips != 900 || escrow != 100 {
		t.Errorf("opponent: %d chips / %d escrow, want 900 / 100 — their own game must be left alone", chips, escrow)
	}

	// 🚫 still works, so the challenger's chips are not stranded.
	duelPress(t, c, r, sessionID, duelActionDecline, duelOpponent)
	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 1000 || escrow != 0 {
		t.Errorf("challenger after the decline: %d chips / %d escrow, want 1000 / 0", chips, escrow)
	}
}

func TestDuelRefusesABetOutsideTheRange(t *testing.T) {
	for _, bet := range []int64{duelMinBet - 1, duelMaxBet + 1, 0, -100} {
		c, bank := newDuelCommandForTest(t, true)
		r := &fakeCasinoResponder{}

		if err := c.handle(r, duelSlashInteraction(duelOpponent, false, bet)); err != nil {
			t.Fatalf("/duel %d: %v", bet, err)
		}
		if got := r.last(t).Data.Content; got != duelBetRangeMessage {
			t.Errorf("bet %d: reply = %q, want %q", bet, got, duelBetRangeMessage)
		}
		if _, escrow := duelHoldings(t, bank, duelChallenger); escrow != 0 {
			t.Errorf("bet %d staked %d chips anyway", bet, escrow)
		}
	}
}

// --- the buttons -----------------------------------------------------------

func TestDuelAcceptTossesTheCoinSettlesBothSidesAndDisablesTheButtons(t *testing.T) {
	tests := []struct {
		name                string
		challengerWins      bool
		wantChallengerChips int64
		wantOpponentChips   int64
		wantWinner          string
		wantCoin            string
	}{
		{name: "挑戦者の勝ち", challengerWins: true, wantChallengerChips: 1100, wantOpponentChips: 900, wantWinner: duelChallenger, wantCoin: "表"},
		{name: "受け手の勝ち", challengerWins: false, wantChallengerChips: 900, wantOpponentChips: 1100, wantWinner: duelOpponent, wantCoin: "裏"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, bank := newDuelCommandForTest(t, tc.challengerWins)
			r := &fakeCasinoResponder{}
			sessionID := startDuel(t, c, r, 100)

			resp := duelPress(t, c, r, sessionID, duelActionAccept, duelOpponent)

			if resp.Type != discordgo.InteractionResponseUpdateMessage {
				t.Errorf("response type = %v, want an edit of the challenge itself", resp.Type)
			}
			if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != tc.wantChallengerChips || escrow != 0 {
				t.Errorf("challenger: %d chips / %d escrow, want %d / 0", chips, escrow, tc.wantChallengerChips)
			}
			if chips, escrow := duelHoldings(t, bank, duelOpponent); chips != tc.wantOpponentChips || escrow != 0 {
				t.Errorf("opponent: %d chips / %d escrow, want %d / 0", chips, escrow, tc.wantOpponentChips)
			}

			body := duelBodyOf(t, resp)
			for _, want := range []string{"🪙 コイン: **" + tc.wantCoin + "**", "🏆 勝者: <@" + tc.wantWinner + ">"} {
				if !strings.Contains(body, want) {
					t.Errorf("result body is missing %q: %q", want, body)
				}
			}
			if !strings.Contains(body, casinoPayoutLine(200, tc.wantChallengerChips)) && !strings.Contains(body, casinoPayoutLine(200, tc.wantOpponentChips)) {
				t.Errorf("neither side's payout line names the 200-chip pot: %q", body)
			}
			for idx, button := range duelButtonsOf(t, resp) {
				if !button.Disabled {
					t.Errorf("button %d is still live on a decided duel", idx)
				}
			}
			if c.sessions.Len() != 0 {
				t.Errorf("%d challenges are live after the duel was decided, want 0", c.sessions.Len())
			}
		})
	}
}

// The order the escrow discipline depends on: the chips are the part that
// must survive a crash, so both accounts are settled BEFORE the message the
// players see changes. The fake responder reads the store at the exact moment
// of the edit.
func TestDuelAcceptSettlesBeforeEditingTheMessage(t *testing.T) {
	c, bank := newDuelCommandForTest(t, true)
	r := &fakeCasinoResponder{}
	sessionID := startDuel(t, c, r, 100)

	var editSeen bool
	var chipsAtEditTime, escrowAtEditTime int64 = -1, -1
	r.onRespond = func(resp *discordgo.InteractionResponse) {
		if resp.Type != discordgo.InteractionResponseUpdateMessage {
			return
		}
		editSeen = true
		view, err := bank.ViewAccount("g1", duelChallenger, time.Now())
		if err != nil {
			t.Errorf("ViewAccount during the edit: %v", err)
			return
		}
		chipsAtEditTime, escrowAtEditTime = view.Account.Chips, view.Account.Escrow
	}

	duelPress(t, c, r, sessionID, duelActionAccept, duelOpponent)

	if !editSeen {
		t.Fatal("the acceptance never edited the challenge")
	}
	if chipsAtEditTime != 1100 || escrowAtEditTime != 0 {
		t.Fatalf("at edit time the challenger held %d chips / %d escrow, want 1100 / 0 — the message must not lead the money", chipsAtEditTime, escrowAtEditTime)
	}
}

// 設計書 §6: 押せるのは受け手だけ。The challenger owns the session and still
// cannot press — and the refusal must cost nothing, so the challenge is left
// exactly as it was.
func TestDuelOnlyTheReceiverCanPress(t *testing.T) {
	for _, presser := range []string{duelChallenger, "u9"} {
		c, bank := newDuelCommandForTest(t, true)
		r := &fakeCasinoResponder{}
		sessionID := startDuel(t, c, r, 100)

		resp := duelPress(t, c, r, sessionID, duelActionAccept, presser)

		if resp.Data.Content != duelNotYoursMessage {
			t.Errorf("%s: reply = %q, want %q", presser, resp.Data.Content, duelNotYoursMessage)
		}
		if resp.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
			t.Errorf("%s: the refusal is public, want ephemeral", presser)
		}
		if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 900 || escrow != 100 {
			t.Errorf("%s: challenger %d chips / %d escrow, want 900 / 100 — a stranger's press must settle nothing", presser, chips, escrow)
		}
		if c.sessions.Len() != 1 {
			t.Errorf("%s: %d challenges are live, want 1 — the challenge must survive a stranger's press", presser, c.sessions.Len())
		}
	}
}

func TestDuelDeclineRefundsTheChallengerAndClosesTheChallenge(t *testing.T) {
	c, bank := newDuelCommandForTest(t, true)
	r := &fakeCasinoResponder{}
	sessionID := startDuel(t, c, r, 100)

	resp := duelPress(t, c, r, sessionID, duelActionDecline, duelOpponent)

	if resp.Type != discordgo.InteractionResponseUpdateMessage {
		t.Errorf("response type = %v, want an edit of the challenge itself", resp.Type)
	}
	if resp.Data.Embeds[0].Title != duelDeclinedTitle {
		t.Errorf("title: got %q, want %q", resp.Data.Embeds[0].Title, duelDeclinedTitle)
	}
	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 1000 || escrow != 0 {
		t.Errorf("challenger: %d chips / %d escrow, want 1000 / 0 — a declined challenge is refunded whole", chips, escrow)
	}
	// The opponent never staked, so declining must not open an account move
	// for them either.
	if chips, escrow := duelHoldings(t, bank, duelOpponent); chips != 1000 || escrow != 0 {
		t.Errorf("opponent: %d chips / %d escrow, want 1000 / 0", chips, escrow)
	}
	for idx, button := range duelButtonsOf(t, resp) {
		if !button.Disabled {
			t.Errorf("button %d is still live on a declined challenge", idx)
		}
	}
	if c.sessions.Len() != 0 {
		t.Errorf("%d challenges are live after a decline, want 0", c.sessions.Len())
	}
}

// 設計書 §6: 受諾時に受け手のチップが足りないときは ephemeral で断り、盤面は
// 残す。Nobody else can take the challenge, so the board waits for the 🚫 or
// for the sweeper — and the challenger's stake stays where it is.
func TestDuelAcceptWithoutEnoughChipsKeepsTheChallengeStanding(t *testing.T) {
	c, bank := newDuelCommandForTest(t, true)
	r := &fakeCasinoResponder{}
	// The opponent's account has to exist before it can be drained; a bet of
	// 1,000 against 500 chips is the refusal under test.
	duelDrainChips(t, bank, duelOpponent, 500)
	sessionID := startDuel(t, c, r, 1000)

	resp := duelPress(t, c, r, sessionID, duelActionAccept, duelOpponent)

	if want := "❌ チップが足りません(現在: 500枚)"; resp.Data.Content != want {
		t.Errorf("reply = %q, want %q", resp.Data.Content, want)
	}
	if resp.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Error("the refusal is public, want ephemeral")
	}
	if resp.Type == discordgo.InteractionResponseUpdateMessage {
		t.Error("a refused acceptance rewrote the challenge, want the board left alone")
	}
	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 0 || escrow != 1000 {
		t.Errorf("challenger: %d chips / %d escrow, want 0 / 1000 — a refused acceptance must not refund", chips, escrow)
	}
	if chips, escrow := duelHoldings(t, bank, duelOpponent); chips != 500 || escrow != 0 {
		t.Errorf("opponent: %d chips / %d escrow, want 500 / 0 — nothing of theirs may be staked", chips, escrow)
	}
	if c.sessions.Len() != 1 {
		t.Fatalf("%d challenges are live, want 1 — the challenge must survive a refused acceptance", c.sessions.Len())
	}

	// And it is still answerable: 🚫 returns the stake.
	duelPress(t, c, r, sessionID, duelActionDecline, duelOpponent)
	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 1000 || escrow != 0 {
		t.Errorf("after the decline the challenger holds %d chips / %d escrow, want 1000 / 0", chips, escrow)
	}
}

// A button pressed after the duel is over must not settle anything a second
// time — the challenge is gone from the manager, which is what makes the
// second press a no-op with an explanation.
func TestDuelPressAfterTheChallengeIsOverIsRefused(t *testing.T) {
	tests := []struct{ name, first, second string }{
		{name: "受諾のあとに断る", first: duelActionAccept, second: duelActionDecline},
		{name: "受諾のあとにもう一度受ける", first: duelActionAccept, second: duelActionAccept},
		{name: "辞退のあとに受ける", first: duelActionDecline, second: duelActionAccept},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, bank := newDuelCommandForTest(t, true)
			r := &fakeCasinoResponder{}
			sessionID := startDuel(t, c, r, 100)

			duelPress(t, c, r, sessionID, tc.first, duelOpponent)
			challengerChips, _ := duelHoldings(t, bank, duelChallenger)
			opponentChips, _ := duelHoldings(t, bank, duelOpponent)

			resp := duelPress(t, c, r, sessionID, tc.second, duelOpponent)

			if resp.Data.Content != duelChallengeOverMessage {
				t.Errorf("reply = %q, want %q", resp.Data.Content, duelChallengeOverMessage)
			}
			if resp.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
				t.Error("the refusal is public, want ephemeral")
			}
			if chips, _ := duelHoldings(t, bank, duelChallenger); chips != challengerChips {
				t.Errorf("the second press moved the challenger from %d to %d chips", challengerChips, chips)
			}
			if chips, _ := duelHoldings(t, bank, duelOpponent); chips != opponentChips {
				t.Errorf("the second press moved the opponent from %d to %d chips", opponentChips, chips)
			}
		})
	}
}

// --- the three-minute sweep ------------------------------------------------

// 設計書 §4.5: 失効は辞退と同じ扱い — 挑戦者へ全額返金し、「⌛ 時間切れ —
// 挑戦は取り下げられました」に差し替える。
//
// The refund must NOT go through Store.SettleGame: that credit is capped, so
// a challenger already at the chip cap would have part of their own stake
// destroyed by taking it back. bank.settles is what pins that.
func TestDuelTimedOutChallengeIsWithdrawnAndRefunded(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)

	c, bank := newDuelCommandForTest(t, true)
	RegisterComponent(c) // the real renderer and settler, not a stand-in

	opened := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	if err := bank.EnsureCasinoAccess("g1", duelChallenger, opened); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	// The stake names the challenge from its first write, as /duel mints the
	// board's ID before it stakes anything (C-3b) — the sweeper withdraws by
	// that same ID.
	sessionID, err := casino.NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	if err := bank.OpenGame("g1", duelChallenger, string(casino.GameDuel), sessionID, 100, opened); err != nil {
		t.Fatalf("OpenGame: %v", err)
	}
	// The manager's clock is frozen at the challenge, so LastActionAt is
	// `opened` and the sweeper's own clock alone decides what is expired.
	mgr := casino.NewSessionManager(func() time.Time { return opened }, casino.DefaultSessionTTL)
	c.sessions = mgr
	board := &casino.DuelState{ChallengerID: duelChallenger, OpponentID: duelOpponent, Bet: 100, Stage: casino.DuelPending}
	if _, err := mgr.OpenWithID(sessionID, "g1", duelChallenger, casino.GameDuel, board, casino.MessageRef{ChannelID: "c1", MessageID: "m1"}); err != nil {
		t.Fatalf("opening the challenge: %v", err)
	}
	// An open hands back a HELD challenge (C3B-P3); ending the hold is what
	// the real start path does before it returns.
	mgr.Release(sessionID)

	editor := newRecordingEditor()
	sweepIdleBoards(editor, mgr, bank, opened.Add(casino.DefaultSessionTTL))

	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 1000 || escrow != 0 {
		t.Errorf("challenger: %d chips / %d escrow, want 1000 / 0 — an expired challenge is refunded whole", chips, escrow)
	}
	if bank.settles != 0 {
		t.Errorf("SettleGame was called %d times, want 0: a withdrawal must not go through the capped credit", bank.settles)
	}
	if mgr.Len() != 0 {
		t.Errorf("%d challenges are still live, want 0", mgr.Len())
	}
	if board.Stage != casino.DuelSettled {
		t.Errorf("board stage = %q after the sweep, want %q", board.Stage, casino.DuelSettled)
	}

	edit := editor.took(t)
	if edit.Channel != "c1" || edit.ID != "m1" {
		t.Errorf("edited %s/%s, want c1/m1", edit.Channel, edit.ID)
	}
	if edit.Embeds == nil || len(*edit.Embeds) != 1 {
		t.Fatal("the edit carries no single embed")
	}
	embed := (*edit.Embeds)[0]
	if embed.Title != duelTimedOutTitle {
		t.Errorf("title: got %q, want %q — an expired challenge is a withdrawal, not 自動決着", embed.Title, duelTimedOutTitle)
	}
	if !strings.Contains(embed.Description, "<@u1> へ 100枚 を返金しました。") {
		t.Errorf("the closing line does not name the refund: %q", embed.Description)
	}
	if edit.Components == nil || len(*edit.Components) != 1 {
		t.Fatal("the expired challenge lost its buttons instead of keeping them disabled")
	}
	for idx, component := range (*edit.Components)[0].(discordgo.ActionsRow).Components {
		if !component.(discordgo.Button).Disabled {
			t.Errorf("button %d is still live on an expired challenge", idx)
		}
	}
}

// --- refusals that must not cost the challenger their stake -----------------

// duelFailingBank makes ONE AcceptDuel fail the way a disk error inside
// Store.Update does: nothing is written, so the challenger's stake is still
// sitting in escrow when the command sees the error.
type duelFailingBank struct {
	*flakyBank
	acceptErr error
}

func (b *duelFailingBank) AcceptDuel(guildID, challengerID, opponentID, sessionID string, bet int64, challengerWins bool) (casino.DuelSettlement, error) {
	if err := b.acceptErr; err != nil {
		b.acceptErr = nil // once — the retry must reach the real store
		return casino.DuelSettlement{}, err
	}
	return b.flakyBank.AcceptDuel(guildID, challengerID, opponentID, sessionID, bet, challengerWins)
}

// 反復 4 の指摘 1: 保存前に失敗した受諾は、預かりを口座に残したまま返ってくる。
// ここでセッションを閉じると預かりを解く経路が両方(🚫 と掃除人)消えるので、
// 挑戦者は再起動まで「進行中のゲーム」で固まる。挑戦は立ったままでなければならない。
func TestDuelAcceptFailingWithoutWritingKeepsTheChallengeRefundable(t *testing.T) {
	c, bank := newDuelCommandForTest(t, true)
	c.store = &duelFailingBank{flakyBank: bank, acceptErr: errors.New("casino: write casino.json: disk full")}
	r := &fakeCasinoResponder{}
	sessionID := startDuel(t, c, r, 100)

	resp := duelPress(t, c, r, sessionID, duelActionAccept, duelOpponent)

	if resp.Data.Content != duelActionFailedMessage {
		t.Errorf("reply = %q, want %q", resp.Data.Content, duelActionFailedMessage)
	}
	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 900 || escrow != 100 {
		t.Errorf("challenger: %d chips / %d escrow, want 900 / 100 — a failed save moves nothing", chips, escrow)
	}
	if c.sessions.Len() != 1 {
		t.Fatalf("%d challenges are live, want 1 — a stake still in escrow needs a board that can release it", c.sessions.Len())
	}

	// The whole point of keeping the board: 🚫 still works afterwards.
	duelPress(t, c, r, sessionID, duelActionDecline, duelOpponent)

	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 1000 || escrow != 0 {
		t.Errorf("after 🚫: %d chips / %d escrow, want 1000 / 0", chips, escrow)
	}
	if c.sessions.Len() != 0 {
		t.Errorf("%d challenges are live after the decline, want 0", c.sessions.Len())
	}
}

// The other side of the same test: a challenge with no stake left to release
// must go, or it keeps a button that can only ever fail.
//
// The stake is taken off the account for REAL — rather than by a bank that
// answers ErrNoGameInProgress while the chips are still in escrow — because
// the door a board leaves through reads the account rather than the refusal
// (C3B-P4). A double whose error and whose escrow disagree would be pinning a
// state the store cannot produce, and the real store produces this one:
// AcceptDuel refuses an empty escrow with exactly that error.
func TestDuelAcceptWithoutAStakeDropsTheChallenge(t *testing.T) {
	c, bank := newDuelCommandForTest(t, true)
	r := &fakeCasinoResponder{}
	sessionID := startDuel(t, c, r, 100)

	// Whatever took it — a restart's RefundStaleEscrows, a settlement this
	// board never saw — the stake behind this challenge is gone.
	if _, err := bank.SettleGame("g1", duelChallenger, sessionID, 100); err != nil {
		t.Fatalf("clearing the challenger's stake: %v", err)
	}

	resp := duelPress(t, c, r, sessionID, duelActionAccept, duelOpponent)

	if resp.Data.Content != duelChallengeOverMessage {
		t.Errorf("reply = %q, want %q", resp.Data.Content, duelChallengeOverMessage)
	}
	if c.sessions.Len() != 0 {
		t.Errorf("%d challenges are live, want 0 — nothing is staked behind this board", c.sessions.Len())
	}
}

// 反復 4 の指摘 2: 受け手以外の押下で失効期限が延びてはならない。延びると、
// 拒否され続ける第三者がボタンを叩くだけで挑戦者への返金を無期限に止められる。
func TestDuelRefusedPressDoesNotPushBackTheExpiry(t *testing.T) {
	for _, action := range []string{duelActionAccept, duelActionDecline} {
		t.Run(action, func(t *testing.T) {
			resetComponentsForTest()
			t.Cleanup(resetComponentsForTest)

			c, bank := newDuelCommandForTest(t, true)
			RegisterComponent(c)

			opened := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
			now := opened
			mgr := casino.NewSessionManager(func() time.Time { return now }, casino.DefaultSessionTTL)
			c.sessions = mgr

			if err := bank.EnsureCasinoAccess("g1", duelChallenger, opened); err != nil {
				t.Fatalf("EnsureCasinoAccess: %v", err)
			}
			sessionID, err := casino.NewSessionID()
			if err != nil {
				t.Fatalf("NewSessionID: %v", err)
			}
			if err := bank.OpenGame("g1", duelChallenger, string(casino.GameDuel), sessionID, 100, opened); err != nil {
				t.Fatalf("OpenGame: %v", err)
			}
			board := &casino.DuelState{ChallengerID: duelChallenger, OpponentID: duelOpponent, Bet: 100, Stage: casino.DuelPending}
			session, err := mgr.OpenWithID(sessionID, "g1", duelChallenger, casino.GameDuel, board, casino.MessageRef{ChannelID: "c1", MessageID: "m1"})
			if err != nil {
				t.Fatalf("opening the challenge: %v", err)
			}
			mgr.Release(session.ID) // the open is over, so the sweeper may take it

			// One second before the deadline, a bystander presses.
			r := &fakeCasinoResponder{}
			now = opened.Add(casino.DefaultSessionTTL - time.Second)
			resp := duelPress(t, c, r, session.ID, action, "u9")
			if resp.Data.Content != duelNotYoursMessage {
				t.Fatalf("reply = %q, want %q", resp.Data.Content, duelNotYoursMessage)
			}

			// The deadline is still measured from the challenge itself.
			now = opened.Add(casino.DefaultSessionTTL)
			sweepIdleBoards(newRecordingEditor(), mgr, bank, now)

			if mgr.Len() != 0 {
				t.Errorf("%d challenges survived the sweep, want 0 — a refused press must not extend the deadline", mgr.Len())
			}
			if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 1000 || escrow != 0 {
				t.Errorf("challenger: %d chips / %d escrow, want 1000 / 0 — the expired challenge must refund", chips, escrow)
			}
		})
	}
}

// --- the undelivered challenge (C3B-14) ------------------------------------

// duelBankThatRefusesOneRefund is a real store whose NEXT DeclineDuel fails,
// and that remembers the board the stake was opened for. Neither is reachable
// from the outside — a refund only moves chips the account already owns, and
// the board's ID is minted inside /duel — but "the withdrawal was decided and
// the chips did not move" is exactly the state the retry exists for, and the
// recorded ID is how a test presses the board the failing call is standing
// on.
type duelBankThatRefusesOneRefund struct {
	*flakyBank
	refuseNextRefund error
	declines         int
	board            string
	// beforeDecline runs once, immediately before a refund is attempted —
	// the window this cleanup shares with ⚔️, and the only way a test gets
	// to act inside it instead of hoping to hit it by timing.
	beforeDecline func()
	// afterDecline runs once, immediately after a refund has LANDED and
	// before the board it belongs to is closed. That is the interval the
	// 2 周目 review's reproduction steps into: the escrow is already back and
	// the old board is still registered.
	afterDecline func()
}

func (b *duelBankThatRefusesOneRefund) OpenGame(guildID, userID, game, sessionID string, bet int64, now time.Time) error {
	if err := b.flakyBank.OpenGame(guildID, userID, game, sessionID, bet, now); err != nil {
		return err
	}
	b.board = sessionID
	return nil
}

func (b *duelBankThatRefusesOneRefund) DeclineDuel(guildID, challengerID, sessionID string) error {
	b.declines++
	if hook := b.beforeDecline; hook != nil {
		b.beforeDecline = nil // once — the hook must not re-enter its own refund
		hook()
	}
	if err := b.refuseNextRefund; err != nil {
		b.refuseNextRefund = nil
		return err
	}
	if err := b.flakyBank.DeclineDuel(guildID, challengerID, sessionID); err != nil {
		return err
	}
	if hook := b.afterDecline; hook != nil {
		b.afterDecline = nil // once — the hook must not re-enter its own refund
		hook()
	}
	return nil
}

// newDuelCommandOnRefusingBank is newDuelCommandForTest with a store whose
// refunds the test can make fail, and with the manager's clock in the test's
// hand so a sweep happens exactly when it asks for one.
func newDuelCommandOnRefusingBank(t *testing.T, now func() time.Time) (*DuelCommand, *duelBankThatRefusesOneRefund) {
	t.Helper()
	bank := &duelBankThatRefusesOneRefund{flakyBank: &flakyBank{Store: casino.New(filepath.Join(t.TempDir(), "casino.json"))}}
	return &DuelCommand{
		store:    bank,
		sessions: casino.NewSessionManager(now, casino.DefaultSessionTTL),
		flip:     func() bool { return true },
	}, bank
}

// A challenge that never reached Discord has nothing to press, so the stake
// behind it must come back. The board is what the sweeper retries through, so
// it may only be closed once the refund has actually landed: closing it first
// leaves a stake in escrow that no button, no sweep and no 🚫 can release —
// the account is "in a game" until the next restart.
func TestDuelUndeliveredChallengeKeepsTheBoardUntilTheRefundLands(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)

	opened := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	c, bank := newDuelCommandOnRefusingBank(t, func() time.Time { return opened })
	RegisterComponent(c) // the real settler, so the sweep withdraws like production

	bank.refuseNextRefund = errors.New("the refund never reached the disk")
	r := &fakeCasinoResponder{respondErr: errors.New("https://discord.com/api/v9/interactions/1/SECRET_TOKEN/callback: 500")}
	if err := c.handle(r, duelSlashInteraction(duelOpponent, false, 100)); err == nil {
		t.Fatal("/duel reported success although the challenge never reached Discord")
	}

	if chips, escrow := duelHoldings(t, bank.flakyBank, duelChallenger); chips != 900 || escrow != 100 {
		t.Fatalf("challenger: %d chips / %d escrow after a refused refund, want 900 / 100", chips, escrow)
	}
	if c.sessions.Len() != 1 {
		t.Fatalf("%d challenges are live after a refused refund, want 1 — a closed board is one nothing can retry", c.sessions.Len())
	}

	// Three minutes on, the sweeper withdraws the challenge it can still see.
	sweepIdleBoards(newRecordingEditor(), c.sessions, bank, opened.Add(casino.DefaultSessionTTL))

	if chips, escrow := duelHoldings(t, bank.flakyBank, duelChallenger); chips != 1000 || escrow != 0 {
		t.Errorf("challenger: %d chips / %d escrow after the sweep, want 1000 / 0 — the stake is stranded", chips, escrow)
	}
	if c.sessions.Len() != 0 {
		t.Errorf("%d challenges survived the sweep, want 0", c.sessions.Len())
	}
	if bank.declines != 2 {
		t.Errorf("DeclineDuel was called %d times, want 2 (the undelivered cleanup, then the sweep's retry)", bank.declines)
	}
}

// The cleanup above is a third presser of a live board: Discord can create
// the challenge message and still fail the call that sent it, so ⚔️ can
// arrive while the withdrawal is in flight. Both must not pay — the board
// lock is what serializes them, exactly as it does ⚔️ against 🚫.
func TestDuelUndeliveredCleanupAndAcceptanceDoNotBothPay(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)

	opened := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	c, bank := newDuelCommandOnRefusingBank(t, func() time.Time { return opened })
	RegisterComponent(c)

	accepted := make(chan struct{})
	var acceptResponder *fakeCasinoResponder
	bank.beforeDecline = func() {
		acceptResponder = &fakeCasinoResponder{}
		sessionID := bank.board
		go func() {
			defer close(accepted)
			customID := BuildCustomID(c.Prefix(), sessionID, duelActionAccept)
			if err := c.handleComponent(acceptResponder, duelButtonInteraction(customID, duelOpponent), sessionID, duelActionAccept); err != nil {
				t.Errorf("pressing ⚔️ during the cleanup: %v", err)
			}
		}()
	}

	r := &fakeCasinoResponder{respondErr: errors.New("the challenge never reached Discord")}
	if err := c.handle(r, duelSlashInteraction(duelOpponent, false, 100)); err == nil {
		t.Fatal("/duel reported success although the challenge never reached Discord")
	}
	<-accepted

	challengerChips, challengerEscrow := duelHoldings(t, bank.flakyBank, duelChallenger)
	opponentChips, opponentEscrow := duelHoldings(t, bank.flakyBank, duelOpponent)
	if challengerEscrow != 0 || opponentEscrow != 0 {
		t.Errorf("escrow: challenger %d / opponent %d, want 0 / 0", challengerEscrow, opponentEscrow)
	}
	// The withdrawal is the one that happened: nobody was paid out of a
	// challenge that was already being taken back.
	if challengerChips != 1000 || opponentChips != 1000 {
		t.Errorf("chips: challenger %d / opponent %d, want 1000 / 1000 — the cleanup and ⚔️ both moved chips", challengerChips, opponentChips)
	}
	if got := acceptResponder.last(t).Data.Content; got != duelChallengeOverMessage {
		t.Errorf("⚔️ answered %q, want %q", got, duelChallengeOverMessage)
	}
	if c.sessions.Len() != 0 {
		t.Errorf("%d challenges are still live after the withdrawal, want 0", c.sessions.Len())
	}
}

// --- 開始の順序(C3B-P2)----------------------------------------------------

// The challenge board is registered BEFORE the challenger is staked, so a
// challenge that cannot be registered moves no chips and has nothing to give
// back — the refund it used to need was the one that could fail.
func TestDuelTakesTheChallengeBoardBeforeTheChipsMove(t *testing.T) {
	c, bank := newDuelCommandForTest(t, true)
	r := &fakeCasinoResponder{}

	// A challenge this user already has: sessions.OpenWithID refuses the new one.
	if _, err := c.sessions.Open("g1", duelChallenger, casino.GameDuel, &casino.DuelState{}, casino.MessageRef{}); err != nil {
		t.Fatalf("seeding a live challenge: %v", err)
	}

	if err := c.handle(r, duelSlashInteraction(duelOpponent, false, 100)); err != nil {
		t.Fatalf("/duel: %v", err)
	}
	if got, want := r.last(t).Data.Content, duelInProgressMessage; got != want {
		t.Errorf("refusal: got %q, want %q", got, want)
	}
	if bank.opens != 0 {
		t.Fatalf("OpenGame was called %d times, want 0: a challenge that cannot be registered must not stake a single chip", bank.opens)
	}
	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 1000 || escrow != 0 {
		t.Errorf("challenger: %d chips / %d escrow after the refusal, want 1000 / 0", chips, escrow)
	}
}

// And a stake the store refuses leaves no challenge standing: a board with no
// escrow behind it holds the challenger's "one game at a time" slot and would
// let ⚔️ be pressed on a pot that was never funded.
func TestDuelLeavesNoChallengeWhenTheStakeIsRefused(t *testing.T) {
	c, bank := newDuelCommandForTest(t, true)
	r := &fakeCasinoResponder{}
	bank.openErr = errors.New("the stake never reached the disk")

	if err := c.handle(r, duelSlashInteraction(duelOpponent, false, 100)); err != nil {
		t.Fatalf("/duel: %v", err)
	}
	if got, want := r.last(t).Data.Content, duelStartFailedMessage; got != want {
		t.Errorf("refusal: got %q, want %q", got, want)
	}
	if c.sessions.Len() != 0 {
		t.Errorf("a refused stake left %d challenges standing, want 0", c.sessions.Len())
	}
	if chips, escrow := duelHoldings(t, bank, duelChallenger); chips != 1000 || escrow != 0 {
		t.Errorf("challenger: %d chips / %d escrow after the refusal, want 1000 / 0", chips, escrow)
	}
}

// The 2 周目 review's reproduction, end to end. The undelivered cleanup of a
// first challenge refunds and then closes its board, so between the two there
// is an instant with an empty escrow and a board still registered. A SECOND
// /duel arriving exactly there used to stake its chips (the escrow was free),
// fail to register its board (the first one was still there) and then be
// unable to give the chips back — an escrow named by a board that was never
// registered, which no button, no 🚫 and no sweep can reach.
//
// With the board taken first the second start is refused before it stakes
// anything, so the interval carries no money at all. The armed refund below
// is what makes that a claim and not a coincidence: under the old order the
// second start's own refund would fail here and strand its 100 chips.
func TestDuelASecondStartInsideAWithdrawalStrandsNothing(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)

	opened := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	c, bank := newDuelCommandOnRefusingBank(t, func() time.Time { return opened })
	RegisterComponent(c)

	second := &fakeCasinoResponder{}
	bank.afterDecline = func() {
		// Any refund the second start needs will fail, exactly as the
		// reproduction's unsaved I/O error does.
		bank.refuseNextRefund = errors.New("the refund never reached the disk")
		if err := c.handle(second, duelSlashInteraction(duelOpponent, false, 100)); err != nil {
			t.Errorf("the second /duel: %v", err)
		}
	}

	r := &fakeCasinoResponder{respondErr: errors.New("the challenge never reached Discord")}
	if err := c.handle(r, duelSlashInteraction(duelOpponent, false, 100)); err == nil {
		t.Fatal("/duel reported success although the challenge never reached Discord")
	}

	if got, want := second.last(t).Data.Content, duelInProgressMessage; got != want {
		t.Errorf("the second start answered %q, want %q — the first board still holds the slot", got, want)
	}
	if chips, escrow := duelHoldings(t, bank.flakyBank, duelChallenger); chips != 1000 || escrow != 0 {
		t.Errorf("challenger: %d chips / %d escrow, want 1000 / 0 — a stake is stranded with no board to reach it", chips, escrow)
	}
	if bank.opens != 1 {
		t.Errorf("OpenGame was called %d times, want 1: the second start staked chips inside the withdrawal", bank.opens)
	}
	if c.sessions.Len() != 0 {
		t.Errorf("%d challenges are still live after the withdrawal, want 0", c.sessions.Len())
	}
}

// --- C3B-P5: 返金待ちの挑戦は受けられない -----------------------------------

// A refund DeclineDuel would not take leaves the challenge STANDING (C3B-P3)
// so the idle sweep can hand the stake back. Until C3B-P5 it was left standing
// and still pressable, and /duel is the game where that costs a SECOND player:
// ⚔️ stakes the opponent's own 100 and flips a coin for a challenge whose
// chips are already on their way back to the challenger.
//
// /duel needs no payout fixed for its sweep — SettleTimedOutBoard is that same
// DeclineDuel — but it records the mark anyway, because the mark is also what
// casino.Session.RefundPending answers, and that one read is what all three
// games ask before they take a press.
func TestDuelRefusesAPressWhileItsRefundIsPending(t *testing.T) {
	resetComponentsForTest()
	t.Cleanup(resetComponentsForTest)

	opened := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	c, bank := newDuelCommandOnRefusingBank(t, func() time.Time { return opened })
	RegisterComponent(c) // the real settler, so the sweep withdraws like production

	bank.refuseNextRefund = errors.New("the refund never reached the disk")
	opener := &fakeCasinoResponder{respondErr: errors.New("https://discord.com/api/v9/interactions/1/SECRET_TOKEN/callback: 500")}
	if err := c.handle(opener, duelSlashInteraction(duelOpponent, false, 100)); err == nil {
		t.Fatal("/duel reported success although the challenge never reached Discord")
	}

	sessionID := bank.board
	if chips, escrow := duelHoldings(t, bank.flakyBank, duelChallenger); chips != 900 || escrow != 100 {
		t.Fatalf("challenger: %d chips / %d escrow after a refused refund, want 900 / 100", chips, escrow)
	}
	if session, live := c.sessions.Get(sessionID); !live || !session.RefundPending() {
		t.Fatal("the challenge whose refund was refused is not marked, so every gate below reads false")
	}

	presser := &fakeCasinoResponder{}
	for _, action := range []string{duelActionAccept, duelActionDecline} {
		customID := BuildCustomID(c.Prefix(), sessionID, action)
		if err := c.handleComponent(presser, duelButtonInteraction(customID, duelOpponent), sessionID, action); err != nil {
			t.Fatalf("press %q: %v", action, err)
		}
		if got := presser.last(t).Data.Content; got != casinoSettleFailedMessage {
			t.Errorf("%q on a challenge awaiting its refund answered %q, want %q", action, got, casinoSettleFailedMessage)
		}
	}

	// Neither side moved a chip: the coin was never flipped, and the stake is
	// still where the sweep will find it.
	if chips, escrow := duelHoldings(t, bank.flakyBank, duelChallenger); chips != 900 || escrow != 100 {
		t.Errorf("challenger: %d chips / %d escrow after the presses, want 900 / 100 unchanged", chips, escrow)
	}
	if chips, escrow := duelHoldings(t, bank.flakyBank, duelOpponent); chips != 1000 || escrow != 0 {
		t.Errorf("opponent: %d chips / %d escrow after the presses, want 1000 / 0 — ⚔️ staked them on a challenge being refunded", chips, escrow)
	}
	if bank.declines != 1 {
		t.Errorf("DeclineDuel was called %d times, want 1 (the undelivered cleanup alone) — a refused press must not refund either", bank.declines)
	}
	if c.sessions.Len() != 1 {
		t.Fatalf("%d challenges are live after the presses, want 1 — the sweep is what pays this one", c.sessions.Len())
	}

	// Three minutes on, the sweeper withdraws the stake it can still see.
	sweepIdleBoards(newRecordingEditor(), c.sessions, bank, opened.Add(casino.DefaultSessionTTL))

	if chips, escrow := duelHoldings(t, bank.flakyBank, duelChallenger); chips != 1000 || escrow != 0 {
		t.Errorf("challenger: %d chips / %d escrow after the sweep, want 1000 / 0", chips, escrow)
	}
	if c.sessions.Len() != 0 {
		t.Errorf("%d challenges survived the sweep, want 0", c.sessions.Len())
	}
}
