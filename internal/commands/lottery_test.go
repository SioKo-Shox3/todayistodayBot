package commands

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

// drawAt is the next 09:00 JST every fixture in this file points at.
var drawAt = time.Date(2026, 9, 15, 9, 0, 0, 0, jstZone)

func TestLotteryDefinition_DeclaresGuildOnlyBuyAndStatus(t *testing.T) {
	def := (&LotteryCommand{}).Definition()

	if def.Name != "lottery" {
		t.Fatalf("command name = %q, want %q", def.Name, "lottery")
	}
	// Guild-only is DECLARED here and re-checked at run time in Handle: the
	// declaration only hides the command in DMs, it does not refuse one.
	if def.Contexts == nil || len(*def.Contexts) != 1 || (*def.Contexts)[0] != discordgo.InteractionContextGuild {
		t.Fatalf("Contexts = %v, want guild-only", def.Contexts)
	}

	subs := map[string]*discordgo.ApplicationCommandOption{}
	for _, opt := range def.Options {
		if opt.Type != discordgo.ApplicationCommandOptionSubCommand {
			t.Fatalf("option %q has type %v, want a subcommand", opt.Name, opt.Type)
		}
		subs[opt.Name] = opt
	}
	if len(subs) != 2 || subs["buy"] == nil || subs["status"] == nil {
		t.Fatalf("subcommands = %v, want exactly buy and status", def.Options)
	}
	if len(subs["status"].Options) != 0 {
		t.Fatalf("/lottery status takes options %v, want none", subs["status"].Options)
	}

	count := subs["buy"].Options
	if len(count) != 1 || count[0].Name != "count" || !count[0].Required {
		t.Fatalf("/lottery buy options = %v, want one required `count`", count)
	}
	if count[0].MinValue == nil || *count[0].MinValue != 1 || count[0].MaxValue != float64(casino.LotteryMaxTicketsPerDraw) {
		t.Fatalf("count bounds = [%v, %v], want [1, %d] (the per-draw cap)",
			count[0].MinValue, count[0].MaxValue, casino.LotteryMaxTicketsPerDraw)
	}
}

func TestValidateLotteryCount(t *testing.T) {
	const refusal = "❌ 枚数は1〜10です"
	for _, tc := range []struct {
		name      string
		count     int64
		wantCount int
		wantMsg   string
	}{
		{"the lowest allowed purchase", 1, 1, ""},
		{"the whole per-draw allowance", 10, 10, ""},
		{"zero buys nothing", 0, 0, refusal},
		{"negative", -3, 0, refusal},
		{"one past the cap", 11, 0, refusal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := validateLotteryCount(tc.count)
			if got != tc.wantCount || msg != tc.wantMsg {
				t.Fatalf("validateLotteryCount(%d) = (%d, %q), want (%d, %q)", tc.count, got, msg, tc.wantCount, tc.wantMsg)
			}
		})
	}
}

// TestTranslateLotteryError_UsesTheSpecifiedWording pins the two refusals
// 設計書 C-3a §3 spells out verbatim. The headroom in the cap message is the
// part that has to come from the error rather than from a constant — it is
// what tells a buyer holding 7 tickets that 3 are left.
func TestTranslateLotteryError_UsesTheSpecifiedWording(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"cap with room left", &casino.ErrLotteryLimit{Remaining: 3}, "❌ 1回の抽選で買えるのは10枚までです(あと3枚)"},
		{"cap fully used", &casino.ErrLotteryLimit{Remaining: 0}, "❌ 1回の抽選で買えるのは10枚までです(あと0枚)"},
		{"short of chips", &casino.ErrInsufficientChips{Balance: 20}, "❌ チップが足りません(現在: 20枚)"},
		{"broke", &casino.ErrInsufficientChips{Balance: 0}, "❌ チップが足りません(現在: 0枚)"},
		{"count refused by the store", casino.ErrInvalidAmount, "❌ 枚数は1〜10です"},
		{"anything else", errors.New("disk on fire"), "❌ 宝くじの購入に失敗しました。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := translateLotteryError(tc.err); got != tc.want {
				t.Fatalf("translateLotteryError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestTranslateLotteryError_WrappedErrorsStillMatch guards the errors.As /
// errors.Is calls against a store that starts wrapping: a wrapped cap error
// falling through to the default branch would tell a buyer the purchase
// "failed" when it was simply refused.
func TestTranslateLotteryError_WrappedErrorsStillMatch(t *testing.T) {
	wrapped := fmt.Errorf("casino: buy: %w", &casino.ErrLotteryLimit{Remaining: 2})
	if got, want := translateLotteryError(wrapped), "❌ 1回の抽選で買えるのは10枚までです(あと2枚)"; got != want {
		t.Fatalf("translateLotteryError(wrapped cap) = %q, want %q", got, want)
	}
	wrapped = fmt.Errorf("casino: buy: %w", casino.ErrInvalidAmount)
	if got, want := translateLotteryError(wrapped), "❌ 枚数は1〜10です"; got != want {
		t.Fatalf("translateLotteryError(wrapped ErrInvalidAmount) = %q, want %q", got, want)
	}
}

// TestFormatDrawTime_RendersInJST is the OS-independence guard: the bot may
// run on a host in any zone, so the draw time a Japanese player reads must
// come from the fixed +09:00 projection and never from time.Local.
func TestFormatDrawTime_RendersInJST(t *testing.T) {
	// 2026-09-15 00:00 UTC is 09:00 JST the same day.
	if got, want := formatDrawTime(time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)), "9月15日 09:00"; got != want {
		t.Fatalf("formatDrawTime(UTC instant) = %q, want %q", got, want)
	}
	if got, want := formatDrawTime(drawAt), "9月15日 09:00"; got != want {
		t.Fatalf("formatDrawTime(JST instant) = %q, want %q", got, want)
	}
}

func TestLotteryPurchaseMessage_EchoesTheDebitAndThePot(t *testing.T) {
	got := lotteryPurchaseMessage(casino.LotteryPurchase{
		Count: 3, Cost: 150, Balance: 850,
		UserTickets: 3, TicketsSold: 10, Buyers: 3, Prize: 450, NextDrawAt: drawAt,
	})
	want := "🎟️ 宝くじを3枚購入しました(-150チップ / 残高: 850枚)\n" +
		"💰 次回の賞金: 450チップ\n" +
		"🎫 売れた枚数: 10枚(3人) / あなたの枚数: 3枚\n" +
		"⏰ 次回の抽選: 9月15日 09:00"
	if got != want {
		t.Fatalf("lotteryPurchaseMessage =\n%q\nwant\n%q", got, want)
	}
}

// TestLotteryPurchaseMessage_ShowsTheCumulativeHolding separates the two
// ticket counts that are easy to conflate: what this call bought, and what
// the buyer now holds for the draw. Only the second one decides their odds.
func TestLotteryPurchaseMessage_ShowsTheCumulativeHolding(t *testing.T) {
	got := lotteryPurchaseMessage(casino.LotteryPurchase{
		Count: 2, Cost: 100, Balance: 400,
		UserTickets: 9, TicketsSold: 9, Buyers: 1, Prize: 405, NextDrawAt: drawAt,
	})
	if !strings.Contains(got, "宝くじを2枚購入しました") {
		t.Fatalf("the message does not report the 2 tickets THIS call bought:\n%s", got)
	}
	if !strings.Contains(got, "あなたの枚数: 9枚") {
		t.Fatalf("the message does not report the cumulative holding of 9:\n%s", got)
	}
}

func TestLotteryStatusMessage_ReportsThePotAndTheLastDraw(t *testing.T) {
	got := lotteryStatusMessage(casino.LotteryView{
		Prize: 450, TicketsSold: 10, Buyers: 3, UserTickets: 1, NextDrawAt: drawAt,
		LastDraw: &casino.LotteryDraw{Date: "2026-09-14", WinnerID: "u3", Prize: 270, TicketsSold: 6, Buyers: 2},
	})
	want := "🎟️ **宝くじ**\n" +
		"💰 次回の賞金: 450チップ\n" +
		"🎫 売れた枚数: 10枚(3人) / あなたの枚数: 1枚\n" +
		"⏰ 次回の抽選: 9月15日 09:00\n" +
		"🏆 前回(2026-09-14): <@u3> が 270チップ 獲得(6枚 / 2人)"
	if got != want {
		t.Fatalf("lotteryStatusMessage =\n%q\nwant\n%q", got, want)
	}
}

// TestLotteryStatusMessage_BeforeTheFirstDraw covers a brand-new guild: the
// last-draw line must say there is none rather than render an empty <@>
// mention, which Discord shows as a broken "@unknown-user".
func TestLotteryStatusMessage_BeforeTheFirstDraw(t *testing.T) {
	got := lotteryStatusMessage(casino.LotteryView{Prize: 0, NextDrawAt: drawAt})
	if !strings.Contains(got, "🏆 前回の抽選: まだありません") {
		t.Fatalf("a lottery with no history must say so:\n%s", got)
	}
	if strings.Contains(got, "<@") {
		t.Fatalf("no winner exists, so no mention may be rendered:\n%s", got)
	}
	if !strings.Contains(got, "💰 次回の賞金: 0チップ") || !strings.Contains(got, "あなたの枚数: 0枚") {
		t.Fatalf("an empty pot must still report its (zero) numbers:\n%s", got)
	}
}

// TestLotteryStatusMessage_CarriedOverPrizeIsShown is the quiet-day case: no
// tickets are in the pot, but the carryover from a draw nobody entered still
// pays — showing 0 tickets next to a non-zero prize is what tells the next
// player the pot is worth joining.
func TestLotteryStatusMessage_CarriedOverPrizeIsShown(t *testing.T) {
	got := lotteryStatusMessage(casino.LotteryView{Prize: 300, TicketsSold: 0, Buyers: 0, NextDrawAt: drawAt})
	if !strings.Contains(got, "💰 次回の賞金: 300チップ") || !strings.Contains(got, "🎫 売れた枚数: 0枚(0人)") {
		t.Fatalf("a carried-over prize with no buyers is not rendered:\n%s", got)
	}
}

// TestLotteryCommand_IsRegistered checks the init() self-registration the
// whole command layer depends on (絶対規則 2) — main.go has no list to edit,
// so a missing Register is invisible until the command is simply absent.
func TestLotteryCommand_IsRegistered(t *testing.T) {
	for _, cmd := range All() {
		if cmd.Definition().Name == "lottery" {
			return
		}
	}
	t.Fatal("/lottery is not in the command registry: its init() does not Register")
}
