package commands

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

func init() { Register(&RateCommand{store: casino.Default()}) }

// RateCommand implements /rate — today's coin<->chip rate, its day-over-day
// move, a 7-day sparkline and the two commentary lines.
type RateCommand struct{ store *casino.Store }

func (c *RateCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "rate",
		Description: "今日のコイン⇔チップ両替レートと直近7日間のスパークラインを表示します",
		Contexts:    &guildOnlyContexts,
	}
}

// rateChangeLine renders the day-over-day line shared by /rate and the 9am
// announcement embed, so the two can never drift apart (they used to
// disagree on the diff == 0 case). event comes from the PERSISTED
// casino.EventKind of today's DailyRate, never from a percentage threshold
// — the 70/140 clamp can hide a real surge/crash (§3.2). history must be
// oldest..newest.
func rateChangeLine(history []casino.DailyRate) string {
	if len(history) == 0 {
		return ""
	}
	today := history[len(history)-1]
	if len(history) < 2 {
		return "本日が最初の記録です。"
	}
	prev := history[len(history)-2].Rate
	diff := today.Rate - prev
	pct := float64(diff) / float64(prev) * 100
	arrow := "📈▲"
	switch {
	case diff < 0:
		arrow = "📉▼"
	case diff == 0:
		arrow = "➖"
	}
	eventTag := ""
	switch today.Event {
	case casino.EventSurge:
		eventTag = "🚀"
	case casino.EventCrash:
		eventTag = "💥"
	}
	return fmt.Sprintf("%s%s%.1f%%(前日比 %+d)", eventTag, arrow, pct, diff)
}

// buildRateMessage renders /rate's response: today's rate, day-over-day
// change%, a 7-day sparkline (casino_shared.go's sparkline), the 相場コメント
// (marketCommentary) and the "参謀コメント" (rateRankCommentary). history
// must be oldest..newest; an empty history is guarded rather than trusted,
// because a panic here runs in a discordgo handler goroutine and kills the
// whole process (§3.3 / SF-9).
func buildRateMessage(history []casino.DailyRate) string {
	if len(history) == 0 {
		return "❌ レート情報がまだありません。"
	}
	today := history[len(history)-1]
	values := make([]int, len(history))
	for i, r := range history {
		values[i] = r.Rate
	}
	return fmt.Sprintf("💱 **本日のレート**: 1コイン = %dチップ\n%s\n直近%d日: %s\n%s\n%s",
		today.Rate, rateChangeLine(history), len(history), sparkline(values),
		marketCommentary(history), rateRankCommentary(history))
}

func (c *RateCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	content := ""
	if msg := requireGuildContext(i); msg != "" {
		content = msg
	} else {
		now, userID := time.Now(), resolveUserID(i)
		// §3.8: /rate も共通入口を通る。これがないと「/rate を最初に叩いた
		// ユーザーだけ口座が作られない」という経路依存が残る。
		if err := c.store.EnsureCasinoAccess(i.GuildID, userID, now); err != nil {
			slog.Error("casino: EnsureCasinoAccess failed", "command", "rate", "error", redactInteractionError(err))
			content = "❌ カジノの初期化に失敗しました。"
		} else if history, err := c.store.RecentRates(i.GuildID, now, 7); err != nil {
			slog.Error("casino: RecentRates failed", "error", redactInteractionError(err))
			content = "❌ レート情報の取得に失敗しました。"
		} else {
			content = buildRateMessage(history)
		}
	}
	return respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
}
