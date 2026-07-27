package commands

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

// newTestCasinoStore returns a *casino.Store backed by this test's own
// temporary file. Casino command tests MUST use this and never
// casino.Default(), which points at the production data/casino.json.
func newTestCasinoStore(t *testing.T) *casino.Store {
	t.Helper()
	return casino.New(filepath.Join(t.TempDir(), "casino.json"))
}

// testNow is a fixed instant every casino command test uses, so no test
// result depends on when the suite runs (2026-07-10 12:00 JST).
func testNow() time.Time {
	return time.Date(2026, 7, 10, 12, 0, 0, 0, time.FixedZone("JST", 9*3600))
}

// assertGuildOnly fails unless def declares the guild-only context, i.e.
// the Definition() half of the two-layer guild guard.
func assertGuildOnly(t *testing.T, def *discordgo.ApplicationCommand) {
	t.Helper()
	if def.Contexts == nil {
		t.Fatalf("%s: Contexts must be set to the guild-only context", def.Name)
	}
	if len(*def.Contexts) != 1 || (*def.Contexts)[0] != discordgo.InteractionContextGuild {
		t.Fatalf("%s: unexpected Contexts: %v", def.Name, *def.Contexts)
	}
}

func TestBalanceCommand_Definition_GuildOnly(t *testing.T) {
	def := (&BalanceCommand{}).Definition()
	if def.Name != "balance" {
		t.Fatalf("unexpected command name: %q", def.Name)
	}
	assertGuildOnly(t, def)
}

func TestFormatBalanceMessage_RendersAllFields(t *testing.T) {
	view := casino.AccountView{
		Account:     casino.UserAccount{Coins: 20, Chips: 1500, StreakDays: 3},
		RateUsed:    100,
		TotalAssets: 3500,
	}
	want := "💰 **残高**\nチップ: 1500枚\nコイン: 20枚\nストリーク: 3日\n総資産: 3500相当(本日レート 100)"
	if got := formatBalanceMessage(view); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

func TestBalanceCommand_FirstAccess_OpensAccountWithWelcomeBonus(t *testing.T) {
	store := newTestCasinoStore(t)
	now := testNow()
	// Same order as Handle: EnsureCasinoAccess (its own transaction) first.
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
	if !strings.Contains(formatBalanceMessage(view), "チップ: 1000枚") {
		t.Fatalf("the rendered balance must show the welcome bonus: %q", formatBalanceMessage(view))
	}
}
