package commands

import (
	"testing"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
)

func TestRateCommand_Definition(t *testing.T) {
	def := (&RateCommand{}).Definition()
	if def.Name != "rate" {
		t.Fatalf("unexpected command name: %q", def.Name)
	}
	assertGuildOnly(t, def)
}

func TestRateChangeLine_Empty(t *testing.T) {
	if got := rateChangeLine(nil); got != "" {
		t.Fatalf("expected an empty string for an empty history, got %q", got)
	}
}

func TestRateChangeLine_SingleEntry(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	if got := rateChangeLine(history); got != "本日が最初の記録です。" {
		t.Fatalf("unexpected line: %q", got)
	}
}

func TestRateChangeLine_Rising(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendBull, Event: casino.EventNone},
		{Rate: 110, Trend: casino.TrendBull, Event: casino.EventNone},
	}
	if got := rateChangeLine(history); got != "📈▲10.0%(前日比 +10)" {
		t.Fatalf("unexpected line: %q", got)
	}
}

func TestRateChangeLine_Falling(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendBear, Event: casino.EventNone},
		{Rate: 90, Trend: casino.TrendBear, Event: casino.EventNone},
	}
	if got := rateChangeLine(history); got != "📉▼-10.0%(前日比 -10)" {
		t.Fatalf("unexpected line: %q", got)
	}
}

func TestRateChangeLine_Flat(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone},
		{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone},
	}
	if got := rateChangeLine(history); got != "➖0.0%(前日比 +0)" {
		t.Fatalf("unexpected line: %q", got)
	}
}

func TestRateChangeLine_SurgeShowsRocket(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendBull, Event: casino.EventNone},
		{Rate: 130, Trend: casino.TrendBull, Event: casino.EventSurge},
	}
	if got := rateChangeLine(history); got != "🚀📈▲30.0%(前日比 +30)" {
		t.Fatalf("unexpected line: %q", got)
	}
}

func TestRateChangeLine_CrashShowsExplosion(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendBear, Event: casino.EventNone},
		{Rate: 75, Trend: casino.TrendBear, Event: casino.EventCrash},
	}
	if got := rateChangeLine(history); got != "💥📉▼-25.0%(前日比 -25)" {
		t.Fatalf("unexpected line: %q", got)
	}
}

func TestRateChangeLine_SurgeClampedSmallDiff_StillShowsRocket(t *testing.T) {
	// 130 * 1.15 = 149.5 clamps to the 140 ceiling, i.e. only +7.7%
	// day-over-day. A percentage threshold would drop 🚀 on a genuine surge
	// day; the persisted EventKind must not (BL-5).
	history := []casino.DailyRate{
		{Rate: 130, Trend: casino.TrendBull, Event: casino.EventNone},
		{Rate: 140, Trend: casino.TrendBull, Event: casino.EventSurge},
	}
	if got := rateChangeLine(history); got != "🚀📈▲7.7%(前日比 +10)" {
		t.Fatalf("unexpected line: %q", got)
	}
}

func TestBuildRateMessage_EmptyHistory(t *testing.T) {
	// SF-9: a discordgo handler goroutine has no recover, so an
	// index-out-of-range here would kill the whole process.
	if got := buildRateMessage(nil); got != "❌ レート情報がまだありません。" {
		t.Fatalf("unexpected message for an empty history: %q", got)
	}
}

func TestBuildRateMessage_FirstDayNoChangeLine(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	want := "💱 **本日のレート**: 1コイン = 100チップ\n本日が最初の記録です。\n直近1日: ▁\n凪1日目。動かない相場も相場だ。\n今日のレートは直近1日で1番目の高値です"
	if got := buildRateMessage(history); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildRateMessage_RisingRateShowsUpArrow(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendBull, Event: casino.EventNone},
		{Rate: 110, Trend: casino.TrendBull, Event: casino.EventNone},
	}
	want := "💱 **本日のレート**: 1コイン = 110チップ\n📈▲10.0%(前日比 +10)\n直近2日: ▁█\nコイン強気2日目。天井はどこだ?\n今日のレートは直近2日で1番目の高値です"
	if got := buildRateMessage(history); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildRateMessage_FallingRateShowsDownArrow(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendBear, Event: casino.EventNone},
		{Rate: 90, Trend: casino.TrendBear, Event: casino.EventNone},
	}
	want := "💱 **本日のレート**: 1コイン = 90チップ\n📉▼-10.0%(前日比 -10)\n直近2日: █▁\nコイン弱気2日目。底値を狙うなら今か。\n今日のレートは直近2日で2番目の高値です"
	if got := buildRateMessage(history); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildRateMessage_FlatRateShowsDash(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone},
		{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone},
	}
	want := "💱 **本日のレート**: 1コイン = 100チップ\n➖0.0%(前日比 +0)\n直近2日: ▁▁\n凪2日目。動かない相場も相場だ。\n今日のレートは直近2日で1番目の高値です"
	if got := buildRateMessage(history); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

func TestRateCommand_FirstAccess_OpensAccountWithWelcomeBonus(t *testing.T) {
	// /rate never passes a userID to a balance-changing call, so without the
	// shared EnsureCasinoAccess entry point a user whose first casino command
	// is /rate would never get an account (BL-4).
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
	history, err := store.RecentRates("g1", now, 7)
	if err != nil {
		t.Fatalf("RecentRates: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("EnsureCasinoAccess must guarantee today's rate exists")
	}
	if got := buildRateMessage(history); got == "❌ レート情報がまだありません。" {
		t.Fatalf("unexpected empty-history message after EnsureCasinoAccess: %q", got)
	}
}
