package commands

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

func init() { Register(&RankCommand{store: casino.Default()}) }

// RankCommand implements /rank — the guild's top 10 accounts by total
// assets at today's rate.
type RankCommand struct{ store *casino.Store }

func (c *RankCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "rank",
		Description: "総資産（チップ＋コイン×当日レート）ランキングTOP10を表示します",
		Contexts:    &guildOnlyContexts,
	}
}

var rankMedals = []string{"🥇", "🥈", "🥉"}

// formatRankMessage renders /rank's response using <@userID> mentions —
// Discord resolves these to display names client-side, so this needs no
// *discordgo.Session lookup (a deliberate simplicity choice, also used by
// the jackpot celebration in slot.go and the announce embed).
func formatRankMessage(entries []casino.RankEntry) string {
	if len(entries) == 0 {
		return "📊 まだ誰も口座を持っていません。"
	}
	var b strings.Builder
	b.WriteString("📊 **総資産ランキング TOP10**\n")
	for i, e := range entries {
		marker := fmt.Sprintf("%d.", i+1)
		if i < len(rankMedals) {
			marker = rankMedals[i]
		}
		fmt.Fprintf(&b, "%s <@%s> — %d\n", marker, e.UserID, e.TotalAssets)
	}
	return b.String()
}

func (c *RankCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	content := ""
	if msg := requireGuildContext(i); msg != "" {
		content = msg
	} else {
		now, userID := time.Now(), resolveUserID(i)
		// §3.8: /rank も共通入口を通す(実行者に口座が無いまま順位表だけ見える、
		// という経路依存をなくす)。
		if err := c.store.EnsureCasinoAccess(i.GuildID, userID, now); err != nil {
			slog.Error("casino: EnsureCasinoAccess failed", "command", "rank", "error", redactInteractionError(err))
			content = "❌ カジノの初期化に失敗しました。"
		} else if entries, err := c.store.TopAssets(i.GuildID, now, 10); err != nil {
			slog.Error("casino: TopAssets failed", "error", redactInteractionError(err))
			content = "❌ ランキングの取得に失敗しました。"
		} else {
			content = formatRankMessage(entries)
		}
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
}
