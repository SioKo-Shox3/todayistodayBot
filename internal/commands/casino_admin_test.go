package commands

import (
	"testing"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

// adminInteraction builds a /casino-admin invocation by an administrator,
// carrying subcommand `sub` with `opts`.
func adminInteraction(guildID, userID, sub string, opts []*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: guildID,
		Member: &discordgo.Member{
			User:        &discordgo.User{ID: userID},
			Permissions: discordgo.PermissionAdministrator,
		},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "casino-admin",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: sub, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts},
			},
		},
	}}
}

func TestCasinoAdminCommand_Definition_HasAdministratorPermissionAndGuildContext(t *testing.T) {
	def := (&CasinoAdminCommand{}).Definition()
	if def.Name != "casino-admin" {
		t.Fatalf("unexpected command name: %q", def.Name)
	}
	assertGuildOnly(t, def)
	if def.DefaultMemberPermissions == nil {
		t.Fatal("DefaultMemberPermissions must be set so Discord hides the command from non-administrators")
	}
	if *def.DefaultMemberPermissions != discordgo.PermissionAdministrator {
		t.Fatalf("expected the Administrator permission, got %d", *def.DefaultMemberPermissions)
	}
	if len(def.Options) != 2 {
		t.Fatalf("expected the mint and channel subcommands, got %d options", len(def.Options))
	}
	mint := def.Options[0]
	if mint.Name != "mint" || def.Options[1].Name != "channel" {
		t.Fatalf("unexpected subcommands: %q, %q", mint.Name, def.Options[1].Name)
	}
	var amount *discordgo.ApplicationCommandOption
	for _, opt := range mint.Options {
		if opt.Name == "amount" {
			amount = opt
		}
	}
	if amount == nil {
		t.Fatal("mint must take an amount option")
	}
	if amount.MinValue == nil || *amount.MinValue != 1 {
		t.Fatalf("mint.amount must have MinValue 1, got %v", amount.MinValue)
	}
	if amount.MaxValue != float64(casino.MaxCoins) {
		t.Fatalf("mint.amount must have MaxValue %v, got %v", float64(casino.MaxCoins), amount.MaxValue)
	}
}

func TestTranslateMintError_InvalidAmount(t *testing.T) {
	if got := translateMintError(casino.ErrInvalidAmount); got != "❌ 発行枚数は1以上・上限以内で指定してください。" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestTranslateMintError_CapExceeded(t *testing.T) {
	want := "❌ 発行枚数が大きすぎます。対象ユーザーの残高上限を超えます。"
	if got := translateMintError(casino.ErrCoinCapExceeded); got != want {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestTranslateMintError_Overflow(t *testing.T) {
	want := "❌ 発行枚数が大きすぎます。対象ユーザーの残高上限を超えます。"
	if got := translateMintError(casino.ErrAmountOverflow); got != want {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestBuildResponse_NonGuildContext_ReturnsError(t *testing.T) {
	cmd := &CasinoAdminCommand{store: newTestCasinoStore(t)}
	i := adminInteraction("", "admin", "channel", nil)
	if got := cmd.buildResponse(i, testNow()); got != "❌ このコマンドはサーバー内で使用してください。" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestBuildResponse_NonAdministrator_ReturnsExactDesignMessage(t *testing.T) {
	// The runtime half of the two-layer admin guard: DefaultMemberPermissions
	// only hides the command client-side.
	cmd := &CasinoAdminCommand{store: newTestCasinoStore(t)}
	i := adminInteraction("g1", "u1", "channel", nil)
	i.Member.Permissions = discordgo.PermissionSendMessages
	if got := cmd.buildResponse(i, testNow()); got != "❌ このコマンドはサーバー管理者のみ使用できます" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestBuildResponse_Mint_Success(t *testing.T) {
	store := newTestCasinoStore(t)
	cmd := &CasinoAdminCommand{store: store}
	now := testNow()
	i := adminInteraction("g1", "admin", "mint", []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "user", Type: discordgo.ApplicationCommandOptionUser, Value: "u2"},
		{Name: "amount", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(50)},
	})
	if got := cmd.buildResponse(i, now); got != "✅ <@u2> に 50 枚のコインを発行しました。" {
		t.Fatalf("unexpected message: %q", got)
	}
	view, err := store.ViewAccount("g1", "u2", now)
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Coins != 50 {
		t.Fatalf("expected 50 minted coins, got %d", view.Account.Coins)
	}
}

func TestBuildResponse_Mint_ExceedsMaxCoins_RecipientKeepsWelcomeBonus(t *testing.T) {
	// BL-4: the recipient's account is opened in its OWN transaction, so a
	// refused mint must not take the brand-new account and its welcome bonus
	// down with it.
	store := newTestCasinoStore(t)
	cmd := &CasinoAdminCommand{store: store}
	now := testNow()
	i := adminInteraction("g1", "admin", "mint", []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "user", Type: discordgo.ApplicationCommandOptionUser, Value: "u2"},
		{Name: "amount", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(casino.MaxCoins + 1)},
	})
	if got := cmd.buildResponse(i, now); got != "❌ 発行枚数は1以上・上限以内で指定してください。" {
		t.Fatalf("unexpected message: %q", got)
	}
	view, err := store.ViewAccount("g1", "u2", now)
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Chips != 1000 || view.Account.Coins != 0 {
		t.Fatalf("expected the recipient to keep the welcome bonus, got %d chips / %d coins",
			view.Account.Chips, view.Account.Coins)
	}
}

func TestBuildResponse_Channel_Success(t *testing.T) {
	store := newTestCasinoStore(t)
	cmd := &CasinoAdminCommand{store: store}
	i := adminInteraction("g1", "admin", "channel", []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "channel", Type: discordgo.ApplicationCommandOptionChannel, Value: "c9"},
	})
	if got := cmd.buildResponse(i, testNow()); got != "✅ 掲示チャンネルを <#c9> に設定しました。" {
		t.Fatalf("unexpected message: %q", got)
	}
	data, err := store.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if data["g1"] == nil || data["g1"].AnnounceChannelID != "c9" {
		t.Fatalf("expected the announce channel to be persisted, got %+v", data["g1"])
	}
}

func TestCasinoAdminCommand_Handle_ResponseIsEphemeral_OnSuccess(t *testing.T) {
	store := newTestCasinoStore(t)
	cmd := &CasinoAdminCommand{store: store}
	i := adminInteraction("g1", "admin", "channel", []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "channel", Type: discordgo.ApplicationCommandOptionChannel, Value: "c9"},
	})
	got := cmd.buildInteractionResponseData(i, testNow())
	if got.Flags != discordgo.MessageFlagsEphemeral {
		t.Fatalf("expected MessageFlagsEphemeral on a successful response, got flags %d", got.Flags)
	}
	if got.Content != "✅ 掲示チャンネルを <#c9> に設定しました。" {
		t.Fatalf("unexpected content: %q", got.Content)
	}
}

func TestCasinoAdminCommand_Handle_ResponseIsEphemeral_OnGuardFailure(t *testing.T) {
	// Even the ❌ guard-failure branches (non-administrator here) must stay
	// ephemeral — the design decision applies to every response, not just
	// the success path.
	cmd := &CasinoAdminCommand{store: newTestCasinoStore(t)}
	i := adminInteraction("g1", "u1", "channel", nil)
	i.Member.Permissions = discordgo.PermissionSendMessages
	got := cmd.buildInteractionResponseData(i, testNow())
	if got.Flags != discordgo.MessageFlagsEphemeral {
		t.Fatalf("expected MessageFlagsEphemeral on a guard-failure response, got flags %d", got.Flags)
	}
	if got.Content != "❌ このコマンドはサーバー管理者のみ使用できます" {
		t.Fatalf("unexpected content: %q", got.Content)
	}
}

func TestCasinoAdminCommand_FirstAccess_OpensAccountWithWelcomeBonus(t *testing.T) {
	store := newTestCasinoStore(t)
	cmd := &CasinoAdminCommand{store: store}
	now := testNow()
	i := adminInteraction("g1", "admin", "channel", []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "channel", Type: discordgo.ApplicationCommandOptionChannel, Value: "c9"},
	})
	if got := cmd.buildResponse(i, now); got != "✅ 掲示チャンネルを <#c9> に設定しました。" {
		t.Fatalf("unexpected message: %q", got)
	}
	view, err := store.ViewAccount("g1", "admin", now)
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Chips != 1000 {
		t.Fatalf("the invoking administrator must also get an account: %d chips", view.Account.Chips)
	}
}
