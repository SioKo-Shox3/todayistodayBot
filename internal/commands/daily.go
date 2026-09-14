package commands

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

func init() { Register(&DailyCommand{store: casino.Default()}) }

// DailyCommand implements /daily — the once-per-JST-day chip bonus with a
// consecutive-day streak.
type DailyCommand struct{ store *casino.Store }

func (c *DailyCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "daily",
		Description: "デイリーボーナスを受け取ります（1日1回、連続受け取りでボーナス増加）",
		Contexts:    &guildOnlyContexts,
	}
}

// formatDailyMessage renders /daily's success response with a 🔥×streak
// flourish (設計書). Wording is this plan's own judgment.
func formatDailyMessage(displayName string, result casino.DailyClaimResult) string {
	jackpot := ""
	if result.JackpotBonus {
		jackpot = "\n🎉 大入り袋(+500)発生!"
	}
	return fmt.Sprintf("🔥×%d %sさんがデイリーボーナス**+%dチップ**を受け取りました!%s", result.NewStreak, displayName, result.Amount, jackpot)
}

func (c *DailyCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	content := ""
	if msg := requireGuildContext(i); msg != "" {
		content = msg
	} else {
		userID := resolveUserID(i)
		if err := c.store.EnsureCasinoAccess(i.GuildID, userID, time.Now()); err != nil { // §3.8
			slog.Error("casino: EnsureCasinoAccess failed", "command", "daily", "error", redactInteractionError(err))
			content = "❌ カジノの初期化に失敗しました。"
		} else {
			// No instant is passed: ClaimDaily reads the clock inside its own
			// Update closure, so the claimed date is decided under the store
			// lock and cannot be stale by the time the write lands.
			result, err := c.store.ClaimDaily(i.GuildID, userID)
			switch {
			case errors.Is(err, casino.ErrAlreadyClaimedToday):
				content = "❌ 本日は既に受け取り済みです。また明日お越しください。"
			case errors.Is(err, casino.ErrChipCapExceeded):
				content = "❌ チップ残高が上限に達しているため受け取れません。"
			case err != nil:
				slog.Error("casino: ClaimDaily failed", "error", redactInteractionError(err))
				content = "❌ デイリーボーナスの受け取りに失敗しました。"
			default:
				content = formatDailyMessage(resolveDisplayName(i), result)
			}
		}
	}
	return respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content}, // 公開(設計書)
	})
}
