package commands

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

func init() { Register(&CasinoAdminCommand{store: casino.Default()}) }

// CasinoAdminCommand implements /casino-admin mint and /casino-admin
// channel — administrator-only economy maintenance.
type CasinoAdminCommand struct{ store *casino.Store }

func (c *CasinoAdminCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:                     "casino-admin",
		Description:              "カジノ経済の管理コマンド（コイン発行・掲示チャンネル設定）",
		DefaultMemberPermissions: permPtr(discordgo.PermissionAdministrator), // Discord側で非管理者に非表示(設計書)
		Contexts:                 &guildOnlyContexts,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type: discordgo.ApplicationCommandOptionSubCommand, Name: "mint", Description: "指定ユーザーにコインを発行します",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "コインを発行する相手", Required: true},
					// MaxValue is a plain float64 in discordgo v0.29.0 (NOT a pointer — unlike MinValue),
					// so it is set directly. float64 represents 1e12 exactly (< 2^53), no precision loss.
					// This is UX only; the real enforcement is casino.Mint's MaxCoins check (§3.5).
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "amount", Description: "発行するコインの枚数（1以上）", Required: true, MinValue: floatPtr(1), MaxValue: float64(casino.MaxCoins)},
				},
			},
			{
				Type: discordgo.ApplicationCommandOptionSubCommand, Name: "channel", Description: "毎朝9時のレート掲示先チャンネルを設定します",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "掲示先チャンネル", Required: true, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText}},
				},
			},
		},
	}
}

func translateMintError(err error) string {
	switch {
	case errors.Is(err, casino.ErrInvalidAmount):
		return "❌ 発行枚数は1以上・上限以内で指定してください。"
	case errors.Is(err, casino.ErrCoinCapExceeded), errors.Is(err, casino.ErrAmountOverflow):
		return "❌ 発行枚数が大きすぎます。対象ユーザーの残高上限を超えます。"
	default:
		slog.Error("casino: Mint failed", "error", redactInteractionError(err))
		return "❌ コインの発行に失敗しました。"
	}
}

func (c *CasinoAdminCommand) handleMint(guildID string, opts []*discordgo.ApplicationCommandInteractionDataOption, now time.Time) string {
	var targetUserID string
	var amount int64
	for _, opt := range opts {
		switch opt.Name {
		case "user":
			targetUserID = opt.UserValue(nil).ID
		case "amount":
			amount = opt.IntValue()
		}
	}
	// §3.8: open the RECIPIENT's account in its own transaction first, so a
	// cap-exceeded Mint cannot roll their brand-new account and welcome bonus
	// back. (The invoking admin's own account is opened by buildResponse.)
	if err := c.store.EnsureCasinoAccess(guildID, targetUserID, now); err != nil {
		slog.Error("casino: EnsureCasinoAccess failed", "command", "casino-admin/mint", "error", redactInteractionError(err))
		return "❌ カジノの初期化に失敗しました。"
	}
	if err := c.store.Mint(guildID, targetUserID, amount); err != nil {
		return translateMintError(err)
	}
	return fmt.Sprintf("✅ <@%s> に %d 枚のコインを発行しました。", targetUserID, amount)
}

func (c *CasinoAdminCommand) handleChannel(guildID string, opts []*discordgo.ApplicationCommandInteractionDataOption) string {
	var channelID string
	for _, opt := range opts {
		if opt.Name == "channel" {
			channelID = opt.ChannelValue(nil).ID
		}
	}
	if err := c.store.SetAnnounceChannel(guildID, channelID); err != nil {
		slog.Error("casino: SetAnnounceChannel failed", "error", redactInteractionError(err))
		return "❌ 掲示チャンネルの設定に失敗しました。"
	}
	return fmt.Sprintf("✅ 掲示チャンネルを <#%s> に設定しました。", channelID)
}

// buildResponse is Handle's discordgo-Session-free core, extracted for
// testing (permission/guild checks + subcommand dispatch). now is injected
// so tests are not wall-clock dependent.
func (c *CasinoAdminCommand) buildResponse(i *discordgo.InteractionCreate, now time.Time) string {
	if msg := requireGuildContext(i); msg != "" {
		return msg
	}
	if msg := requireAdministrator(i); msg != "" { // 設計書の文言と完全一致(実行時の二重チェック)
		return msg
	}
	if err := c.store.EnsureCasinoAccess(i.GuildID, resolveUserID(i), now); err != nil { // §3.8
		slog.Error("casino: EnsureCasinoAccess failed", "command", "casino-admin", "error", redactInteractionError(err))
		return "❌ カジノの初期化に失敗しました。"
	}
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return "❌ サブコマンドを指定してください（mint / channel）。"
	}
	sub := data.Options[0]
	switch sub.Name {
	case "mint":
		return c.handleMint(i.GuildID, sub.Options, now)
	case "channel":
		return c.handleChannel(i.GuildID, sub.Options)
	default:
		return "❌ 不明なサブコマンドです。"
	}
}

// buildInteractionResponseData assembles the *discordgo.InteractionResponseData
// Handle sends back — buildResponse's content plus the ephemeral flag (設計書:
// /casino-admin の表示は管理者のみ; every branch of buildResponse, success or
// error, is wrapped ephemeral here). Extracted like buildResponse so tests
// can assert on Flags without a live *discordgo.Session — discordgo's REST
// endpoints are resolved once at package-init time and cannot be redirected
// to a test server (see reminder_test.go's fakeWebhookCreator comment).
func (c *CasinoAdminCommand) buildInteractionResponseData(i *discordgo.InteractionCreate, now time.Time) *discordgo.InteractionResponseData {
	return &discordgo.InteractionResponseData{
		Content: c.buildResponse(i, now),
		Flags:   discordgo.MessageFlagsEphemeral,
	}
}

func (c *CasinoAdminCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: c.buildInteractionResponseData(i, time.Now()),
	})
}
