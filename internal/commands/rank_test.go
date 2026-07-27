package commands

import (
	"testing"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
)

func TestRankCommand_Definition(t *testing.T) {
	def := (&RankCommand{}).Definition()
	if def.Name != "rank" {
		t.Fatalf("unexpected command name: %q", def.Name)
	}
	assertGuildOnly(t, def)
}

func TestFormatRankMessage_Empty(t *testing.T) {
	if got := formatRankMessage(nil); got != "📊 まだ誰も口座を持っていません。" {
		t.Fatalf("unexpected message for an empty ranking: %q", got)
	}
}

func TestFormatRankMessage_TopThreeGetMedals(t *testing.T) {
	entries := []casino.RankEntry{
		{UserID: "u1", TotalAssets: 500},
		{UserID: "u2", TotalAssets: 400},
		{UserID: "u3", TotalAssets: 300},
	}
	want := "📊 **総資産ランキング TOP10**\n🥇 <@u1> — 500\n🥈 <@u2> — 400\n🥉 <@u3> — 300\n"
	if got := formatRankMessage(entries); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

func TestFormatRankMessage_FourthOnwardsGetsNumber(t *testing.T) {
	entries := []casino.RankEntry{
		{UserID: "u1", TotalAssets: 500},
		{UserID: "u2", TotalAssets: 400},
		{UserID: "u3", TotalAssets: 300},
		{UserID: "u4", TotalAssets: 200},
		{UserID: "u5", TotalAssets: 100},
	}
	want := "📊 **総資産ランキング TOP10**\n🥇 <@u1> — 500\n🥈 <@u2> — 400\n🥉 <@u3> — 300\n4. <@u4> — 200\n5. <@u5> — 100\n"
	if got := formatRankMessage(entries); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

func TestRankCommand_FirstAccess_OpensAccountWithWelcomeBonus(t *testing.T) {
	// /rank never passes a userID to a balance-changing call either, so the
	// shared entry point is the only thing that opens the invoker's account
	// (BL-4).
	store := newTestCasinoStore(t)
	now := testNow()
	if err := store.EnsureCasinoAccess("g1", "u1", now); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	entries, err := store.TopAssets("g1", now, 10)
	if err != nil {
		t.Fatalf("TopAssets: %v", err)
	}
	if len(entries) != 1 || entries[0].UserID != "u1" || entries[0].TotalAssets != 1000 {
		t.Fatalf("expected the invoker to appear with the 1,000-chip welcome bonus, got %+v", entries)
	}
	if got := formatRankMessage(entries); got == "📊 まだ誰も口座を持っていません。" {
		t.Fatalf("unexpected empty-ranking message after EnsureCasinoAccess: %q", got)
	}
}
