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
	want := "💰 **残高**\nチップ: 1500枚\nコイン: 20枚\nストリーク: 3日\n今月の純利: +0(/season で順位)\n総資産: 3500相当(本日レート 100)"
	if got := formatBalanceMessage(view); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

// While a board is in flight the stake is out of Chips but still counted in
// TotalAssets, so /balance has to name it — otherwise a mid-game balance
// looks like chips went missing.
func TestFormatBalanceMessage_ShowsTheStakeOfAGameInFlight(t *testing.T) {
	view := casino.AccountView{
		Account:     casino.UserAccount{Coins: 20, Chips: 1400, Escrow: 100, EscrowGame: "highlow", StreakDays: 3},
		RateUsed:    100,
		TotalAssets: 3500,
	}
	want := "💰 **残高**\nチップ: 1400枚\nゲーム中の預かり: 100枚（highlow）\nコイン: 20枚\nストリーク: 3日\n今月の純利: +0(/season で順位)\n総資産: 3500相当(本日レート 100)"
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

// 設計書 C-3b §5「/balance に『今月の純利』を 1 行足す」。The sign is the
// point: a losing month must read as a loss, and the zero of a player who has
// not bet must not be mistaken for a player who broke even after a hundred
// hands — which is why the line points at /season for the ranking.
func TestFormatBalanceMessage_ShowsThisMonthsNetWithItsSign(t *testing.T) {
	tests := []struct {
		name string
		net  int64
		want string
	}{
		{"勝ち越し", 1_200, "今月の純利: +1200(/season で順位)"},
		{"負け越し", -350, "今月の純利: -350(/season で順位)"},
		{"まだ勝敗なし", 0, "今月の純利: +0(/season で順位)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			view := casino.AccountView{
				Account:     casino.UserAccount{Coins: 20, Chips: 1500, StreakDays: 3, SeasonNet: tc.net},
				RateUsed:    100,
				TotalAssets: 3500,
			}
			if got := formatBalanceMessage(view); !strings.Contains(got, tc.want+"\n") {
				t.Fatalf("the 純利 line is missing or wrong:\n got: %q\nwant it to contain: %q", got, tc.want)
			}
		})
	}
}

// The number comes off the account the store keeps, so a result booked by a
// game shows up in /balance without /balance knowing how the game settled.
func TestBalanceCommand_ShowsTheNetTheEconomyRecorded(t *testing.T) {
	store := newTestCasinoStore(t)
	now := testNow()
	if err := store.EnsureCasinoAccess("g1", "u1", now); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	if err := store.OpenGame("g1", "u1", string(casino.GameHighLow), testEscrowSession, 200, now); err != nil {
		t.Fatalf("OpenGame: %v", err)
	}
	if _, err := store.SettleGame("g1", "u1", testEscrowSession, 0); err != nil {
		t.Fatalf("SettleGame: %v", err)
	}

	view, err := store.ViewAccount("g1", "u1", now)
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}

	if got := formatBalanceMessage(view); !strings.Contains(got, "今月の純利: -200(/season で順位)\n") {
		t.Fatalf("the lost stake is not reflected in 今月の純利:\n%s", got)
	}
}
