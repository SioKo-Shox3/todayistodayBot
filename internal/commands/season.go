package commands

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

func init() { Register(&SeasonCommand{store: casino.Default()}) }

// SeasonCommand implements /season — this month's 純利 table, where the
// caller stands in it, how many days are left and what the previous season
// paid (設計書 C-3b §5).
type SeasonCommand struct{ store *casino.Store }

func (c *SeasonCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "season",
		Description: "今月のシーズン（純利）の順位・自分の成績・残り日数を表示します",
		Contexts:    &guildOnlyContexts,
	}
}

// formatSeasonMessage renders /season. Mentions rather than looked-up names,
// like formatRankMessage, and the same medals for the same three places —
// but the number is 純利, so it carries its sign: a leader on +1,200 and a
// leader on -50 (a month where everybody lost) must not read alike.
func formatSeasonMessage(view casino.SeasonView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "🏆 **シーズン %s**（今日を含めて残り%d日）\n", view.Month, view.DaysLeft)
	if len(view.Ranks) == 0 {
		b.WriteString("まだ誰も勝敗を記録していません。\n")
	} else {
		for i, rank := range view.Ranks {
			marker := fmt.Sprintf("%d.", i+1)
			if i < len(rankMedals) {
				marker = rankMedals[i]
			}
			fmt.Fprintf(&b, "%s <@%s> — 純利 %+d\n", marker, rank.UserID, rank.Net)
		}
		fmt.Fprintf(&b, "参加者: %d人\n", view.Players)
	}
	// The caller's own line is unconditional: someone who has not played is
	// told WHY they are missing from the table instead of being left to
	// wonder whether the ranking is broken.
	if view.SelfRank > 0 {
		fmt.Fprintf(&b, "あなた: %d位 — 純利 %+d\n", view.SelfRank, view.Self.Net)
	} else {
		b.WriteString("あなた: 圏外（純利 0 — 勝敗がつくと順位に載ります）\n")
	}
	b.WriteString(formatLastSeasonLine(view.Last))
	return b.String()
}

// formatLastSeasonLine renders the one season the store keeps (設計書 C-3b
// §4: 直近 1 件だけ). Bonus is the credited amount, which is why it is shown
// per row rather than read off SeasonBonus: a winner at the chip cap took
// less than the table offers, and the line must not promise chips that
// never arrived.
func formatLastSeasonLine(last *casino.SeasonResult) string {
	if last == nil {
		return "前シーズン: まだありません。"
	}
	if len(last.Ranks) == 0 {
		return fmt.Sprintf("前シーズン（%s）: 受賞者なし", last.Month)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "前シーズン（%s・%d人）:", last.Month, last.Players)
	for i, rank := range last.Ranks {
		medal := fmt.Sprintf("%d位", i+1)
		if i < len(rankMedals) {
			medal = rankMedals[i]
		}
		fmt.Fprintf(&b, " %s <@%s>（純利 %+d / 賞与 %d枚）", medal, rank.UserID, rank.Net, rank.Bonus)
	}
	return b.String()
}

func (c *SeasonCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	content := ""
	if msg := requireGuildContext(i); msg != "" {
		content = msg
	} else {
		now, userID := time.Now(), resolveUserID(i)
		// §3.8: the same entry every other casino command takes, so that
		// reading the table cannot be the one path that skips opening an
		// account.
		if err := c.store.EnsureCasinoAccess(i.GuildID, userID, now); err != nil {
			slog.Error("casino: EnsureCasinoAccess failed", "command", "season", "error", redactInteractionError(err))
			content = "❌ カジノの初期化に失敗しました。"
		} else if view, err := c.store.SeasonStatus(i.GuildID, userID, now); err != nil {
			slog.Error("casino: SeasonStatus failed", "error", redactInteractionError(err))
			content = "❌ シーズン情報の取得に失敗しました。"
		} else {
			content = formatSeasonMessage(view)
		}
	}
	return respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
}
