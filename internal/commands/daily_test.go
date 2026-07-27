package commands

import (
	"testing"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
)

func TestDailyCommand_Definition(t *testing.T) {
	def := (&DailyCommand{}).Definition()
	if def.Name != "daily" {
		t.Fatalf("unexpected command name: %q", def.Name)
	}
	assertGuildOnly(t, def)
}

func TestFormatDailyMessage_NoJackpot(t *testing.T) {
	result := casino.DailyClaimResult{Amount: 200, NewStreak: 3, JackpotBonus: false}
	want := "🔥×3 たろうさんがデイリーボーナス**+200チップ**を受け取りました!"
	if got := formatDailyMessage("たろう", result); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

func TestFormatDailyMessage_WithJackpotBonus(t *testing.T) {
	result := casino.DailyClaimResult{Amount: 700, NewStreak: 7, JackpotBonus: true}
	want := "🔥×7 たろうさんがデイリーボーナス**+700チップ**を受け取りました!\n🎉 大入り袋(+500)発生!"
	if got := formatDailyMessage("たろう", result); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

func TestDailyCommand_FirstAccess_OpensAccountWithWelcomeBonus(t *testing.T) {
	store := newTestCasinoStore(t)
	now := testNow()
	// Same order as Handle: EnsureCasinoAccess first, then the operation.
	if err := store.EnsureCasinoAccess("g1", "u1", now); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	before, err := store.ViewAccount("g1", "u1", now)
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if before.Account.Chips != 1000 {
		t.Fatalf("expected the 1,000-chip welcome bonus on first access, got %d", before.Account.Chips)
	}
	result, err := store.ClaimDaily("g1", "u1", now)
	if err != nil {
		t.Fatalf("ClaimDaily: %v", err)
	}
	if result.NewStreak != 1 {
		t.Fatalf("expected the first claim to start a 1-day streak, got %d", result.NewStreak)
	}
	after, err := store.ViewAccount("g1", "u1", now)
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if after.Account.Chips != before.Account.Chips+result.Amount {
		t.Fatalf("the welcome bonus must survive the claim: before=%d amount=%d after=%d",
			before.Account.Chips, result.Amount, after.Account.Chips)
	}
	if got := formatDailyMessage("たろう", result); got == "" {
		t.Fatal("formatDailyMessage returned an empty message for a successful claim")
	}
}
