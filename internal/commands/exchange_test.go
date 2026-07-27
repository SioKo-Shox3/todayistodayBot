package commands

import (
	"testing"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

func TestExchangeCommand_Definition_HasTwoSubcommands(t *testing.T) {
	def := (&ExchangeCommand{}).Definition()
	if def.Name != "exchange" {
		t.Fatalf("unexpected command name: %q", def.Name)
	}
	assertGuildOnly(t, def)
	if len(def.Options) != 2 {
		t.Fatalf("expected 2 subcommands, got %d", len(def.Options))
	}
	wantNames := []string{"coin-to-chip", "chip-to-coin"}
	for idx, sub := range def.Options {
		if sub.Type != discordgo.ApplicationCommandOptionSubCommand {
			t.Fatalf("option %d must be a subcommand, got %v", idx, sub.Type)
		}
		if sub.Name != wantNames[idx] {
			t.Fatalf("option %d: expected %q, got %q", idx, wantNames[idx], sub.Name)
		}
		if len(sub.Options) != 1 || sub.Options[0].Name != "amount" {
			t.Fatalf("subcommand %q must take exactly one option named amount", sub.Name)
		}
		amount := sub.Options[0]
		if amount.Type != discordgo.ApplicationCommandOptionInteger || !amount.Required {
			t.Fatalf("subcommand %q: amount must be a required integer", sub.Name)
		}
		if amount.MinValue == nil || *amount.MinValue != 1 {
			t.Fatalf("subcommand %q: amount must have MinValue 1", sub.Name)
		}
	}
}

func TestTranslateExchangeError_InsufficientCoins(t *testing.T) {
	got := translateExchangeError(&casino.ErrInsufficientCoins{Balance: 7})
	if got != "❌ コインが足りません(現在: 7枚)" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestTranslateExchangeError_InsufficientChips(t *testing.T) {
	got := translateExchangeError(&casino.ErrInsufficientChips{Balance: 42})
	if got != "❌ チップが足りません(現在: 42枚)" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestTranslateExchangeError_InvalidAmount(t *testing.T) {
	if got := translateExchangeError(casino.ErrInvalidAmount); got != "❌ 両替量は1以上を指定してください。" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestTranslateExchangeError_BelowMinimum(t *testing.T) {
	want := "❌ その枚数では両替後の受取量が1コイン未満になります。もう少し多い枚数を指定してください。"
	if got := translateExchangeError(casino.ErrBelowMinimumExchange); got != want {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestTranslateExchangeError_Overflow(t *testing.T) {
	if got := translateExchangeError(casino.ErrAmountOverflow); got != "❌ 指定された枚数が大きすぎます。" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestTranslateExchangeError_ChipCapExceeded(t *testing.T) {
	want := "❌ 両替するとチップ残高が上限を超えます。もっと少ない枚数を指定してください。"
	if got := translateExchangeError(casino.ErrChipCapExceeded); got != want {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestTranslateExchangeError_CoinCapExceeded(t *testing.T) {
	want := "❌ 両替するとコイン残高が上限を超えます。もっと少ない枚数を指定してください。"
	if got := translateExchangeError(casino.ErrCoinCapExceeded); got != want {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestHandleCoinToChip_Success(t *testing.T) {
	store := newTestCasinoStore(t)
	cmd := &ExchangeCommand{store: store}
	now := testNow()
	if err := store.EnsureCasinoAccess("g1", "u1", now); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	if err := store.Mint("g1", "u1", 100); err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// A guild's very first day is a fixed rate of 100 (casino.nextRate with
	// hasPrev=false), so 100 coins * 100 * 97% = 9,700 chips is deterministic.
	want := "✅ 100コインを9700チップに両替しました(レート100)\n今日のレートは直近1日で1番目の高値です"
	if got := cmd.handleCoinToChip("g1", "u1", 100, now); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

func TestHandleCoinToChip_InsufficientCoins(t *testing.T) {
	store := newTestCasinoStore(t)
	cmd := &ExchangeCommand{store: store}
	now := testNow()
	if err := store.EnsureCasinoAccess("g1", "u1", now); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	if got := cmd.handleCoinToChip("g1", "u1", 5, now); got != "❌ コインが足りません(現在: 0枚)" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestHandleChipToCoin_Success(t *testing.T) {
	store := newTestCasinoStore(t)
	cmd := &ExchangeCommand{store: store}
	now := testNow()
	if err := store.EnsureCasinoAccess("g1", "u1", now); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	// 1,000 chips * 97% / 100 = 9 coins at the day-1 rate of 100.
	want := "✅ 1000チップを9コインに両替しました(レート100)\n今日のレートは直近1日で1番目の高値です"
	if got := cmd.handleChipToCoin("g1", "u1", 1000, now); got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

func TestHandleChipToCoin_BelowMinimumResult(t *testing.T) {
	store := newTestCasinoStore(t)
	cmd := &ExchangeCommand{store: store}
	now := testNow()
	if err := store.EnsureCasinoAccess("g1", "u1", now); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	want := "❌ その枚数では両替後の受取量が1コイン未満になります。もう少し多い枚数を指定してください。"
	if got := cmd.handleChipToCoin("g1", "u1", 1, now); got != want {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestExchangeCommand_FirstAccess_OpensAccountWithWelcomeBonus(t *testing.T) {
	// BL-4: EnsureCasinoAccess commits in its OWN transaction, so a first
	// /exchange that fails on insufficient funds must NOT roll the brand-new
	// account and its welcome bonus back.
	store := newTestCasinoStore(t)
	cmd := &ExchangeCommand{store: store}
	now := testNow()
	if err := store.EnsureCasinoAccess("g1", "u1", now); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	if got := cmd.handleCoinToChip("g1", "u1", 5, now); got != "❌ コインが足りません(現在: 0枚)" {
		t.Fatalf("expected the exchange to fail: %q", got)
	}
	view, err := store.ViewAccount("g1", "u1", now)
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Chips != 1000 {
		t.Fatalf("the welcome bonus must survive a failed exchange, got %d chips", view.Account.Chips)
	}
}
