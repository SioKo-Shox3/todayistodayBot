package commands

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

func init() { Register(&ExchangeCommand{store: casino.Default()}) }

// ExchangeCommand implements /exchange coin-to-chip and /exchange
// chip-to-coin at today's rate, minus the 3% fee.
type ExchangeCommand struct{ store *casino.Store }

func (c *ExchangeCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "exchange",
		Description: "コインとチップを両替します（手数料3%）",
		Contexts:    &guildOnlyContexts,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type: discordgo.ApplicationCommandOptionSubCommand, Name: "coin-to-chip", Description: "コインをチップに両替します",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "amount", Description: "両替するコインの枚数（1以上）", Required: true, MinValue: floatPtr(1)},
				},
			},
			{
				Type: discordgo.ApplicationCommandOptionSubCommand, Name: "chip-to-coin", Description: "チップをコインに両替します",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "amount", Description: "両替するチップの枚数（1以上）", Required: true, MinValue: floatPtr(1)},
				},
			},
		},
	}
}

// translateExchangeError maps internal/casino's sentinel/typed errors to
// the exact ❌ Japanese text (chip-insufficient wording matches 設計書
// verbatim; coin-insufficient is this plan's symmetric extension —
// 設計書 only gives the chip case explicitly).
func translateExchangeError(err error) string {
	var insufficientCoins *casino.ErrInsufficientCoins
	var insufficientChips *casino.ErrInsufficientChips
	switch {
	case errors.As(err, &insufficientCoins):
		return fmt.Sprintf("❌ コインが足りません(現在: %d枚)", insufficientCoins.Balance)
	case errors.As(err, &insufficientChips):
		return fmt.Sprintf("❌ チップが足りません(現在: %d枚)", insufficientChips.Balance)
	case errors.Is(err, casino.ErrInvalidAmount):
		return "❌ 両替量は1以上を指定してください。"
	case errors.Is(err, casino.ErrBelowMinimumExchange):
		return "❌ その枚数では両替後の受取量が1コイン未満になります。もう少し多い枚数を指定してください。"
	case errors.Is(err, casino.ErrAmountOverflow):
		return "❌ 指定された枚数が大きすぎます。"
	case errors.Is(err, casino.ErrChipCapExceeded):
		return "❌ 両替するとチップ残高が上限を超えます。もっと少ない枚数を指定してください。"
	case errors.Is(err, casino.ErrCoinCapExceeded):
		return "❌ 両替するとコイン残高が上限を超えます。もっと少ない枚数を指定してください。"
	default:
		slog.Error("casino: exchange failed", "error", redactInteractionError(err))
		return "❌ 両替に失敗しました。"
	}
}

func (c *ExchangeCommand) handleCoinToChip(guildID, userID string, amount int64, now time.Time) string {
	result, err := c.store.ExchangeCoinToChip(guildID, userID, amount, now)
	if err != nil {
		return translateExchangeError(err)
	}
	commentary := ""
	if history, err := c.store.RecentRates(guildID, now, 7); err == nil {
		commentary = "\n" + rateRankCommentary(history)
	}
	return fmt.Sprintf("✅ %dコインを%dチップに両替しました(レート%d)%s", result.Spent, result.Received, result.RateUsed, commentary)
}

func (c *ExchangeCommand) handleChipToCoin(guildID, userID string, amount int64, now time.Time) string {
	result, err := c.store.ExchangeChipToCoin(guildID, userID, amount, now)
	if err != nil {
		return translateExchangeError(err)
	}
	commentary := ""
	if history, err := c.store.RecentRates(guildID, now, 7); err == nil {
		commentary = "\n" + rateRankCommentary(history)
	}
	return fmt.Sprintf("✅ %dチップを%dコインに両替しました(レート%d)%s", result.Spent, result.Received, result.RateUsed, commentary)
}

func (c *ExchangeCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	content := ""
	if msg := requireGuildContext(i); msg != "" {
		content = msg
	} else {
		now, userID := time.Now(), resolveUserID(i)
		data := i.ApplicationCommandData()
		if err := c.store.EnsureCasinoAccess(i.GuildID, userID, now); err != nil { // §3.8
			slog.Error("casino: EnsureCasinoAccess failed", "command", "exchange", "error", redactInteractionError(err))
			content = "❌ カジノの初期化に失敗しました。"
		} else if len(data.Options) == 0 {
			content = "❌ サブコマンドを指定してください（coin-to-chip / chip-to-coin）。"
		} else {
			sub := data.Options[0]
			var amount int64
			for _, opt := range sub.Options {
				if opt.Name == "amount" {
					amount = opt.IntValue()
				}
			}
			switch sub.Name {
			case "coin-to-chip":
				content = c.handleCoinToChip(i.GuildID, userID, amount, now)
			case "chip-to-coin":
				content = c.handleChipToCoin(i.GuildID, userID, amount, now)
			default:
				content = "❌ 不明なサブコマンドです。"
			}
		}
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
}
