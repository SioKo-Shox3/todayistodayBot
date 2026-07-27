package commands

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

func init() { Register(&BalanceCommand{store: casino.Default()}) }

// BalanceCommand implements /balance — the invoker's own coin, chip,
// streak and total-asset snapshot, shown to nobody else (ephemeral).
type BalanceCommand struct{ store *casino.Store }

func (c *BalanceCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "balance",
		Description: "自分のコイン・チップ・ストリーク・総資産を表示します（本人のみに表示）",
		Contexts:    &guildOnlyContexts,
	}
}

// formatBalanceMessage renders /balance's response. Wording is this plan's
// own judgment (設計書 doesn't pin exact copy for /balance) — consistent
// with the rest of the bot's tone.
func formatBalanceMessage(view casino.AccountView) string {
	return fmt.Sprintf("💰 **残高**\nチップ: %d枚\nコイン: %d枚\nストリーク: %d日\n総資産: %d相当(本日レート %d)",
		view.Account.Chips, view.Account.Coins, view.Account.StreakDays, view.TotalAssets, view.RateUsed)
}

func (c *BalanceCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	content := ""
	if msg := requireGuildContext(i); msg != "" {
		content = msg
	} else {
		now, userID := time.Now(), resolveUserID(i)
		// §3.8: open the account (+ welcome bonus) and today's rate as their
		// OWN committed transaction, before anything that can fail.
		if err := c.store.EnsureCasinoAccess(i.GuildID, userID, now); err != nil {
			slog.Error("casino: EnsureCasinoAccess failed", "command", "balance", "error", err)
			content = "❌ カジノの初期化に失敗しました。"
		} else if view, err := c.store.ViewAccount(i.GuildID, userID, now); err != nil {
			slog.Error("casino: ViewAccount failed", "error", err)
			content = "❌ 残高の取得に失敗しました。"
		} else {
			content = formatBalanceMessage(view)
		}
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral, // 本人のみ表示(設計書) — ガード失敗時の❌もこの方針を踏襲し全応答をephemeralに統一する(判断の開示)
		},
	})
}
