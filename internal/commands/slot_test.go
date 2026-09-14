package commands

import (
	"bytes"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

// fakeSlotResponder records every Discord call runReveal makes, so the
// reveal animation can be asserted without a Discord connection.
type fakeSlotResponder struct {
	respondErr    error
	editErr       error // error the failing edit returns; nil = a generic one
	editErrAtCall int   // 1-based edit call that fails; 0 = never
	responded     []string
	edited        []string
	sentChannels  []string
	sentContents  []string
}

func (f *fakeSlotResponder) InteractionRespond(interaction *discordgo.Interaction, resp *discordgo.InteractionResponse, options ...discordgo.RequestOption) error {
	if f.respondErr != nil {
		return f.respondErr
	}
	f.responded = append(f.responded, resp.Data.Content)
	return nil
}

func (f *fakeSlotResponder) InteractionResponseEdit(interaction *discordgo.Interaction, newresp *discordgo.WebhookEdit, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	if newresp.Content == nil {
		return nil, errors.New("WebhookEdit.Content must not be nil")
	}
	f.edited = append(f.edited, *newresp.Content)
	if f.editErrAtCall != 0 && len(f.edited) == f.editErrAtCall {
		if f.editErr != nil {
			return nil, f.editErr
		}
		return nil, errors.New("edit failed")
	}
	return &discordgo.Message{}, nil
}

func (f *fakeSlotResponder) ChannelMessageSend(channelID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.sentChannels = append(f.sentChannels, channelID)
	f.sentContents = append(f.sentContents, content)
	return &discordgo.Message{}, nil
}

func TestSlotCommand_Definition_BetRange10To1000(t *testing.T) {
	def := (&SlotCommand{}).Definition()
	if def.Name != "slot" {
		t.Fatalf("unexpected command name: %q", def.Name)
	}
	assertGuildOnly(t, def)
	if len(def.Options) != 1 {
		t.Fatalf("expected exactly one option, got %d", len(def.Options))
	}
	bet := def.Options[0]
	if bet.Name != "bet" || bet.Type != discordgo.ApplicationCommandOptionInteger || !bet.Required {
		t.Fatalf("expected a required integer option named bet, got %+v", bet)
	}
	if bet.MinValue == nil || *bet.MinValue != 10 {
		t.Fatalf("expected MinValue 10, got %v", bet.MinValue)
	}
	if bet.MaxValue != 1000 {
		t.Fatalf("expected MaxValue 1000, got %v", bet.MaxValue)
	}
}

func TestBuildSlotRevealStages_PlaceholdersRevealProgressively(t *testing.T) {
	result := casino.SpinResult{
		Reels:       [3]casino.SlotSymbol{casino.SymbolCherry, casino.SymbolLemon, casino.SymbolGrape},
		Bet:         10,
		Payout:      0,
		JackpotPool: 1200,
	}
	stages := buildSlotRevealStages(result)
	if len(stages) != 4 {
		t.Fatalf("expected 4 stages, got %d", len(stages))
	}
	want := []string{
		"🎰 ❔ ❔ ❔",
		"🎰 🍒 ❔ ❔",
		"🎰 🍒 🍋 ❔",
		"🎰 🍒 🍋 🍇\n😢 ハズレ(ベット10枚)\n🎰 ジャックポット: 1200 チップ",
	}
	for i := range want {
		if stages[i] != want[i] {
			t.Fatalf("stage %d:\n got: %q\nwant: %q", i, stages[i], want[i])
		}
	}
}

func TestBuildSlotRevealStages_WinShowsPayoutLine(t *testing.T) {
	result := casino.SpinResult{
		Reels:       [3]casino.SlotSymbol{casino.SymbolDiamond, casino.SymbolDiamond, casino.SymbolDiamond},
		Bet:         100,
		Payout:      5000,
		IsJackpot:   true,
		JackpotPool: 1200,
	}
	stages := buildSlotRevealStages(result)
	if len(stages) != 4 {
		t.Fatalf("expected 4 stages, got %d", len(stages))
	}
	want := "🎰 💎 💎 💎\n🎉 5000枚 獲得!(ベット100枚)\n🎰 ジャックポット: 1200 チップ"
	if stages[3] != want {
		t.Fatalf("final stage:\n got: %q\nwant: %q", stages[3], want)
	}
}

func TestBuildSlotRevealStages_LossShowsHazureLine(t *testing.T) {
	result := casino.SpinResult{
		Reels:       [3]casino.SlotSymbol{casino.SymbolBell, casino.SymbolSeven, casino.SymbolLemon},
		Bet:         50,
		Payout:      0,
		JackpotPool: 1200,
	}
	stages := buildSlotRevealStages(result)
	if len(stages) != 4 {
		t.Fatalf("expected 4 stages, got %d", len(stages))
	}
	if got := stages[3]; got != "🎰 🔔 7️⃣ 🍋\n😢 ハズレ(ベット50枚)\n🎰 ジャックポット: 1200 チップ" {
		t.Fatalf("final stage: %q", got)
	}
}

func TestTranslateSlotError_InsufficientChips(t *testing.T) {
	if got := translateSlotError(&casino.ErrInsufficientChips{Balance: 3}); got != "❌ チップが足りません(現在: 3枚)" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestTranslateSlotError_BetOutOfRange(t *testing.T) {
	if got := translateSlotError(casino.ErrBetOutOfRange); got != "❌ ベットは10〜1,000チップです" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestRunReveal_SendsInitialResponseThenThreeEdits(t *testing.T) {
	cmd := &SlotCommand{}
	responder := &fakeSlotResponder{}
	slept := 0
	result := casino.SpinResult{
		Reels:       [3]casino.SlotSymbol{casino.SymbolCherry, casino.SymbolLemon, casino.SymbolGrape},
		Bet:         10,
		Payout:      0,
		JackpotPool: 1200,
	}
	err := cmd.runReveal(responder, &discordgo.Interaction{}, "c1", "u1", result, func(d time.Duration) { slept++ })
	if err != nil {
		t.Fatalf("runReveal returned %v, want nil", err)
	}
	if len(responder.responded) != 1 {
		t.Fatalf("expected exactly 1 InteractionRespond, got %d", len(responder.responded))
	}
	if responder.responded[0] != "🎰 ❔ ❔ ❔" {
		t.Fatalf("unexpected initial response: %q", responder.responded[0])
	}
	if len(responder.edited) != 3 {
		t.Fatalf("expected exactly 3 InteractionResponseEdit calls, got %d", len(responder.edited))
	}
	if responder.edited[2] != "🎰 🍒 🍋 🍇\n😢 ハズレ(ベット10枚)\n🎰 ジャックポット: 1200 チップ" {
		t.Fatalf("unexpected final edit: %q", responder.edited[2])
	}
	if slept != 3 {
		t.Fatalf("expected 3 injected sleeps (no real wall-clock wait), got %d", slept)
	}
}

func TestRunReveal_Jackpot_SendsPublicCelebrationMention(t *testing.T) {
	cmd := &SlotCommand{}
	responder := &fakeSlotResponder{}
	result := casino.SpinResult{
		Reels:       [3]casino.SlotSymbol{casino.SymbolSeven, casino.SymbolSeven, casino.SymbolSeven},
		Bet:         100,
		Payout:      14321,
		IsJackpot:   true,
		JackpotWon:  4321,
		JackpotPool: casino.JackpotSeed,
	}
	if err := cmd.runReveal(responder, &discordgo.Interaction{}, "c1", "u1", result, func(time.Duration) {}); err != nil {
		t.Fatalf("runReveal returned %v, want nil", err)
	}
	if len(responder.sentContents) != 1 {
		t.Fatalf("expected exactly 1 public celebration message, got %d", len(responder.sentContents))
	}
	if responder.sentChannels[0] != "c1" {
		t.Fatalf("celebration posted to %q, want the interaction's channel", responder.sentChannels[0])
	}
	if !strings.Contains(responder.sentContents[0], "<@u1>") {
		t.Fatalf("celebration must mention the winner: %q", responder.sentContents[0])
	}
	if !strings.Contains(responder.sentContents[0], "7️⃣7️⃣7️⃣") {
		t.Fatalf("celebration must show the winning reels: %q", responder.sentContents[0])
	}
	if !strings.Contains(responder.sentContents[0], "4321") {
		t.Fatalf("celebration must show the jackpot pool won (設計書 C-3a §2): %q", responder.sentContents[0])
	}
}

func TestRunReveal_NonJackpot_NoPublicMessage(t *testing.T) {
	cmd := &SlotCommand{}
	responder := &fakeSlotResponder{}
	result := casino.SpinResult{
		Reels:  [3]casino.SlotSymbol{casino.SymbolCherry, casino.SymbolCherry, casino.SymbolLemon},
		Bet:    10,
		Payout: 20,
	}
	if err := cmd.runReveal(responder, &discordgo.Interaction{}, "c1", "u1", result, func(time.Duration) {}); err != nil {
		t.Fatalf("runReveal returned %v, want nil", err)
	}
	if len(responder.sentContents) != 0 {
		t.Fatalf("expected no public message for a non-jackpot spin, got %v", responder.sentContents)
	}
}

func TestRunReveal_EditFailure_DoesNotPropagateError(t *testing.T) {
	cmd := &SlotCommand{}
	responder := &fakeSlotResponder{editErrAtCall: 2}
	result := casino.SpinResult{
		Reels:  [3]casino.SlotSymbol{casino.SymbolCherry, casino.SymbolLemon, casino.SymbolGrape},
		Bet:    10,
		Payout: 0,
	}
	if err := cmd.runReveal(responder, &discordgo.Interaction{}, "c1", "u1", result, func(time.Duration) {}); err != nil {
		t.Fatalf("a reveal failure must not propagate (the payout is already persisted), got %v", err)
	}
	if len(responder.edited) != 2 {
		t.Fatalf("expected the reveal to stop at the failed edit, got %d edits", len(responder.edited))
	}
}

func TestRunReveal_InitialRespondFailure_DoesNotPropagateError(t *testing.T) {
	// SF-8: returning an error here makes cmd/bot/main.go respond to the SAME
	// interaction a second time, which Discord rejects as "already
	// acknowledged".
	cmd := &SlotCommand{}
	responder := &fakeSlotResponder{respondErr: errors.New("respond failed")}
	result := casino.SpinResult{
		Reels:     [3]casino.SlotSymbol{casino.SymbolSeven, casino.SymbolSeven, casino.SymbolSeven},
		Bet:       100,
		Payout:    10000,
		IsJackpot: true,
	}
	if err := cmd.runReveal(responder, &discordgo.Interaction{}, "c1", "u1", result, func(time.Duration) {}); err != nil {
		t.Fatalf("the initial InteractionRespond failure must not propagate, got %v", err)
	}
	if len(responder.edited) != 0 {
		t.Fatalf("expected no edits after the initial response failed, got %d", len(responder.edited))
	}
	if len(responder.sentContents) != 0 {
		t.Fatalf("expected no celebration after the initial response failed, got %v", responder.sentContents)
	}
}

func TestSlotCommand_FirstAccess_OpensAccountWithWelcomeBonus(t *testing.T) {
	store := newTestCasinoStore(t)
	now := testNow()
	if err := store.EnsureCasinoAccess("g1", "u1", now); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	view, err := store.ViewAccount("g1", "u1", now)
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Chips != 1000 {
		t.Fatalf("expected the 1,000-chip welcome bonus on first access, got %d", view.Account.Chips)
	}
	// The welcome bonus is what makes a brand-new user's first /slot playable.
	if _, err := store.Spin("g1", "u1", 10); err != nil {
		t.Fatalf("Spin right after the first access: %v", err)
	}
}

// captureSlogOutput swaps the default slog logger for the duration of the
// test and returns the buffer every log line lands in. runReveal logs through
// the package-level slog, which is what production writes to a file.
func captureSlogOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// TestRunReveal_DiscordFailure_DoesNotLogInteractionToken is the §R-003
// regression: net/http returns a *url.Error carrying the full request URL,
// and that URL contains the interaction token. Logging the error verbatim
// writes the token to disk. Both the initial respond and the reveal edits
// must go through redactInteractionError.
func TestRunReveal_DiscordFailure_DoesNotLogInteractionToken(t *testing.T) {
	result := casino.SpinResult{
		Reels:  [3]casino.SlotSymbol{casino.SymbolCherry, casino.SymbolLemon, casino.SymbolGrape},
		Bet:    10,
		Payout: 0,
	}
	tests := []struct {
		name      string
		responder *fakeSlotResponder
		wantLog   string
	}{
		{
			name: "initial respond fails",
			responder: &fakeSlotResponder{respondErr: &url.Error{
				Op:  "Post",
				URL: "https://discord.com/api/v9/interactions/1/TEST_TOKEN/callback",
				Err: errors.New("connection reset by peer"),
			}},
			wantLog: "Post: *errors.errorString",
		},
		{
			name: "a reveal edit fails",
			responder: &fakeSlotResponder{editErrAtCall: 1, editErr: &url.Error{
				Op:  "Patch",
				URL: "https://discord.com/api/v9/webhooks/1/TEST_TOKEN/messages/@original",
				Err: errors.New("connection reset by peer"),
			}},
			wantLog: "Patch: *errors.errorString",
		},
		{
			// The token does not only live in the URL: net/http wraps
			// whatever the transport produced, and that cause can quote the
			// request. Only the type of the cause is logged.
			name: "the wrapped cause carries the token",
			responder: &fakeSlotResponder{respondErr: &url.Error{
				Op:  "Post",
				URL: "https://discord.com/api/v9/interactions/1/TEST_TOKEN/callback",
				Err: errors.New("request token TEST_TOKEN"),
			}},
			wantLog: "Post: *errors.errorString",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logged := captureSlogOutput(t)
			cmd := &SlotCommand{}

			if err := cmd.runReveal(tc.responder, &discordgo.Interaction{}, "c1", "u1", result, func(time.Duration) {}); err != nil {
				t.Fatalf("runReveal returned error: %v", err)
			}

			out := logged.String()
			if !strings.Contains(out, tc.wantLog) {
				t.Fatalf("the log line does not say which call failed and why: %q, want %q", out, tc.wantLog)
			}
			if strings.Contains(out, "TEST_TOKEN") {
				t.Fatalf("the interaction token reached the log: %q", out)
			}
			if strings.Contains(out, "discord.com/api") {
				t.Fatalf("the request URL reached the log: %q", out)
			}
		})
	}
}

// スロットの結果には必ずプールの1行が付く(設計書 C-3a §2)。
func TestSlotJackpotLine_NotFired_ShowsPoolAfterThisSpin(t *testing.T) {
	got := slotJackpotLine(casino.SpinResult{Bet: 10, JackpotPool: 1234})
	if got != "🎰 ジャックポット: 1234 チップ" {
		t.Fatalf("unexpected jackpot line: %q", got)
	}
}

func TestSlotJackpotLine_Fired_ShowsWonAmountNotTheReseededPool(t *testing.T) {
	// 発火後の JackpotPool は種に戻っているので、その数を出すと
	// 「種も賞金のうち」と読めてしまう。出すのは獲得額だけ。
	got := slotJackpotLine(casino.SpinResult{Bet: 100, JackpotWon: 54321, JackpotPool: casino.JackpotSeed})
	if got != "🎰 JACKPOT!! +54321 チップ" {
		t.Fatalf("unexpected jackpot line: %q", got)
	}
	if strings.Contains(got, "1000") {
		t.Fatalf("the reseeded pool must not appear on a fired line: %q", got)
	}
}

func TestBuildSlotRevealStages_JackpotFireShowsTheWinLine(t *testing.T) {
	result := casino.SpinResult{
		Reels:       [3]casino.SlotSymbol{casino.SymbolSeven, casino.SymbolSeven, casino.SymbolSeven},
		Bet:         100,
		Payout:      24600,
		IsJackpot:   true,
		JackpotWon:  5000,
		JackpotPool: casino.JackpotSeed,
	}
	want := "🎰 7️⃣ 7️⃣ 7️⃣\n🎉 24600枚 獲得!(ベット100枚)\n🎰 JACKPOT!! +5000 チップ"
	if got := buildSlotRevealStages(result)[3]; got != want {
		t.Fatalf("final stage:\n got: %q\nwant: %q", got, want)
	}
}

func TestSlotCelebrationMessage_JackpotFire_CarriesThePool(t *testing.T) {
	got := slotCelebrationMessage("u1", casino.SpinResult{
		Reels:       [3]casino.SlotSymbol{casino.SymbolSeven, casino.SymbolSeven, casino.SymbolSeven},
		Bet:         100,
		Payout:      24600,
		IsJackpot:   true,
		JackpotWon:  5000,
		JackpotPool: casino.JackpotSeed,
	})
	want := "🎉🎉🎉 <@u1> が 7️⃣7️⃣7️⃣ で大当たり!! 🎰 ジャックポット 5000 チップ 獲得!! 🎉🎉🎉"
	if got != want {
		t.Fatalf("celebration:\n got: %q\nwant: %q", got, want)
	}
}

func TestSlotCelebrationMessage_Diamonds_CarryNoPool(t *testing.T) {
	// 💎💎💎 は配当と祝いだけ。プールは動かないので額も出さない。
	got := slotCelebrationMessage("u1", casino.SpinResult{
		Reels:       [3]casino.SlotSymbol{casino.SymbolDiamond, casino.SymbolDiamond, casino.SymbolDiamond},
		Bet:         100,
		Payout:      5000,
		IsJackpot:   true,
		JackpotPool: 7777,
	})
	want := "🎉🎉🎉 <@u1> が 💎💎💎 で大当たり!! 🎉🎉🎉"
	if got != want {
		t.Fatalf("celebration:\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(got, "7777") {
		t.Fatalf("a non-firing spin must not advertise the pool as won: %q", got)
	}
}
