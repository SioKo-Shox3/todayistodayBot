package commands

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

func init() { Register(&SlotCommand{store: casino.Default()}) }

// SlotCommand implements /slot — deduct, draw, persist the payout, then
// play the reel-by-reel reveal animation.
type SlotCommand struct{ store *casino.Store }

func (c *SlotCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "slot",
		Description: "スロットを回します（ベット: 10〜1,000チップ）",
		Contexts:    &guildOnlyContexts,
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "bet", Description: "ベット額（10〜1,000チップ）", Required: true, MinValue: floatPtr(10), MaxValue: 1000},
		},
	}
}

// slotResponder abstracts the *discordgo.Session methods the reveal
// animation needs, so runReveal can be unit tested without a real Discord
// connection. *discordgo.Session satisfies this structurally (same pattern
// as reminder.go's discordWebhookCreator).
type slotResponder interface {
	InteractionRespond(interaction *discordgo.Interaction, resp *discordgo.InteractionResponse, options ...discordgo.RequestOption) error
	InteractionResponseEdit(interaction *discordgo.Interaction, newresp *discordgo.WebhookEdit, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageSend(channelID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

const slotRevealPlaceholder = "❔"

// buildSlotRevealStages returns the 4 progressive message bodies for the
// "リールを1個ずつ止める" animation (設計書): stage 0 = all ❔, stages
// 1-3 reveal one more reel each; stage 3 also appends the payout line.
func buildSlotRevealStages(result casino.SpinResult) []string {
	reelText := func(revealed int) string {
		cells := make([]string, 3)
		for i := 0; i < 3; i++ {
			if i < revealed {
				cells[i] = string(result.Reels[i])
			} else {
				cells[i] = slotRevealPlaceholder
			}
		}
		return strings.Join(cells, " ")
	}
	stages := make([]string, 4)
	for revealed := 0; revealed <= 3; revealed++ {
		line := fmt.Sprintf("🎰 %s", reelText(revealed))
		if revealed == 3 {
			line += "\n" + slotResultLine(result)
		}
		stages[revealed] = line
	}
	return stages
}

func slotResultLine(result casino.SpinResult) string {
	if result.Payout > 0 {
		return fmt.Sprintf("🎉 %d枚 獲得!(ベット%d枚)", result.Payout, result.Bet)
	}
	return fmt.Sprintf("😢 ハズレ(ベット%d枚)", result.Bet)
}

func translateSlotError(err error) string {
	var insufficientChips *casino.ErrInsufficientChips
	switch {
	case errors.As(err, &insufficientChips):
		return fmt.Sprintf("❌ チップが足りません(現在: %d枚)", insufficientChips.Balance)
	case errors.Is(err, casino.ErrBetOutOfRange):
		return "❌ ベットは10〜1,000チップです" // 設計書と完全一致させる
	case errors.Is(err, casino.ErrChipCapExceeded):
		return "❌ チップ残高が上限に達しているため回せません。"
	default:
		slog.Error("casino: spin failed", "error", redactInteractionError(err))
		return "❌ スロットの実行に失敗しました。"
	}
}

// runReveal performs the reel-by-reel animation via InteractionRespond +
// InteractionResponseEdit at ~800ms intervals (設計書), and — if
// result.IsJackpot — posts a SEPARATE public celebration message
// @mentioning userID (設計書). result's balances are ALREADY
// persisted by the time this runs (c.store.Spin, called from Handle,
// already completed) — a Discord API failure or crash here cannot affect
// balances or duplicate/roll back the payout (設計書:
// 「演出中のクラッシュは払い戻しに一切影響しない」). sleep is injected
// (production: time.Sleep) so tests don't take ~2.4s of real wall-clock
// time. Blocking here for ~2.4s is safe: discordgo dispatches each
// InteractionCreate handler in its own goroutine, so this does not
// stall the gateway read loop or other concurrent interactions.
//
// runReveal ALWAYS returns nil (the error result is kept for signature
// stability). Every Discord failure here is logged and swallowed, initial
// InteractionRespond included. Propagating that first failure would be
// actively harmful: cmd/bot/main.go:53-61 responds to the SAME interaction
// with a second InteractionRespond whenever a handler returns non-nil,
// which Discord rejects as "already acknowledged" — so the user would get
// nothing either way, plus a misleading error in the log. The spin is
// already persisted before we get here, so "log and stop" is the correct
// terminal behavior. (Decision recorded rather than left asymmetric: the
// earlier draft returned the error for the first call and nil for the
// edits.)
func (c *SlotCommand) runReveal(responder slotResponder, interaction *discordgo.Interaction, channelID, userID string, result casino.SpinResult, sleep func(time.Duration)) error {
	stages := buildSlotRevealStages(result)

	if err := responder.InteractionRespond(interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: stages[0]},
	}); err != nil {
		slog.Error("discord: InteractionRespond failed at slot reveal start", "error", redactInteractionError(err))
		return nil
	}

	for _, stage := range stages[1:] {
		sleep(800 * time.Millisecond)
		content := stage
		if _, err := responder.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
			slog.Error("discord: InteractionResponseEdit failed during slot reveal", "error", redactInteractionError(err))
			return nil // 演出の失敗は払い戻しに一切影響しない(設計書)。ログのみ、呼び出し元にはエラーを伝播しない。
		}
	}

	if result.IsJackpot {
		celebration := fmt.Sprintf("🎉🎉🎉 <@%s> が %s%s%s で大当たり!! 🎉🎉🎉", userID, result.Reels[0], result.Reels[1], result.Reels[2])
		if _, err := responder.ChannelMessageSend(channelID, celebration); err != nil {
			slog.Error("discord: ChannelMessageSend failed for jackpot celebration", "error", redactInteractionError(err))
		}
	}
	return nil
}

func (c *SlotCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if msg := requireGuildContext(i); msg != "" {
		return respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: msg},
		})
	}

	var bet int64
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "bet" {
			bet = opt.IntValue()
		}
	}
	if bet < 10 || bet > 1000 { // defense in depth — Discord's MinValue/MaxValue already enforce this client-side
		return respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "❌ ベットは10〜1,000チップです"},
		})
	}

	userID := resolveUserID(i)
	if err := c.store.EnsureCasinoAccess(i.GuildID, userID, time.Now()); err != nil { // §3.8
		slog.Error("casino: EnsureCasinoAccess failed", "command", "slot", "error", redactInteractionError(err))
		return respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "❌ カジノの初期化に失敗しました。"},
		})
	}

	result, err := c.store.Spin(i.GuildID, userID, bet)
	if err != nil {
		return respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: translateSlotError(err)},
		})
	}

	return c.runReveal(s, i.Interaction, i.ChannelID, userID, result, time.Sleep)
}
