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

func init() { Register(&LotteryCommand{store: casino.Default()}) }

// LotteryCommand implements /lottery buy and /lottery status (設計書 C-3a
// §4). Both replies are PUBLIC: the pot is a shared one, so who is in it and
// what it pays is exactly the information that makes the next person buy.
type LotteryCommand struct{ store *casino.Store }

// lotteryMinTickets is the smallest purchase; the largest is the per-draw cap
// itself (casino.LotteryMaxTicketsPerDraw), since a buyer holding nothing may
// take the whole allowance in one call.
const lotteryMinTickets = 1

// jstZone is the zone every user-facing lottery time is rendered in. Fixed
// +09:00 rather than time.Local (the host may run anywhere — Linux included)
// and rather than time.LoadLocation, which needs a tzdata file a minimal
// container may not carry.
var jstZone = time.FixedZone("JST", 9*3600)

func (c *LotteryCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "lottery",
		Description: "宝くじを買います（1枚50チップ、毎日9時に抽選）",
		Contexts:    &guildOnlyContexts,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type: discordgo.ApplicationCommandOptionSubCommand, Name: "buy", Description: "宝くじを買います（1〜10枚）",
				Options: []*discordgo.ApplicationCommandOption{
					// MaxValue is a plain float64 in discordgo v0.29.0 (NOT a pointer — unlike MinValue).
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "count", Description: "購入する枚数（1〜10）", Required: true,
						MinValue: floatPtr(lotteryMinTickets), MaxValue: float64(casino.LotteryMaxTicketsPerDraw)},
				},
			},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "status", Description: "次回の賞金・売れた枚数・前回の結果を表示します"},
		},
	}
}

// validateLotteryCount bounds the ticket count and returns the ❌ text for a
// count outside 1〜10 (設計書 C-3a §5). Discord's MinValue/MaxValue already
// refuse those client-side; this is defense in depth against a direct API
// call, exactly as /slot re-checks its bet range.
func validateLotteryCount(count int64) (int, string) {
	if count < lotteryMinTickets || count > int64(casino.LotteryMaxTicketsPerDraw) {
		return 0, fmt.Sprintf("❌ 枚数は%d〜%dです", lotteryMinTickets, casino.LotteryMaxTicketsPerDraw)
	}
	return int(count), ""
}

// translateLotteryError maps internal/casino's typed errors to the ❌ text of
// 設計書 C-3a §3・§5. The chip-shortage wording is the same sentence every
// other casino command uses, so a player reads one message for one situation.
func translateLotteryError(err error) string {
	var limit *casino.ErrLotteryLimit
	var insufficientChips *casino.ErrInsufficientChips
	switch {
	case errors.As(err, &limit):
		return fmt.Sprintf("❌ 1回の抽選で買えるのは%d枚までです(あと%d枚)", casino.LotteryMaxTicketsPerDraw, limit.Remaining)
	case errors.As(err, &insufficientChips):
		return fmt.Sprintf("❌ チップが足りません(現在: %d枚)", insufficientChips.Balance)
	case errors.Is(err, casino.ErrInvalidAmount):
		return fmt.Sprintf("❌ 枚数は%d〜%dです", lotteryMinTickets, casino.LotteryMaxTicketsPerDraw)
	default:
		slog.Error("casino: lottery purchase failed", "error", redactInteractionError(err))
		return "❌ 宝くじの購入に失敗しました。"
	}
}

// formatDrawTime renders a draw instant for a Japanese reader, always in JST.
func formatDrawTime(at time.Time) string { return at.In(jstZone).Format("1月2日 15:04") }

// lotteryPotLines renders the two lines both replies share — what the next
// draw pays and who is in it — so the confirmation and the status can never
// disagree about the same pot.
func lotteryPotLines(prize int64, sold, buyers, userTickets int, nextDrawAt time.Time) string {
	return fmt.Sprintf("💰 次回の賞金: %dチップ\n🎫 売れた枚数: %d枚(%d人) / あなたの枚数: %d枚\n⏰ 次回の抽選: %s",
		prize, sold, buyers, userTickets, formatDrawTime(nextDrawAt))
}

// lotteryPurchaseMessage renders a successful /lottery buy. The balance is
// echoed for the same reason /slot echoes it: the debit is the part a buyer
// most wants confirmed.
func lotteryPurchaseMessage(p casino.LotteryPurchase) string {
	return fmt.Sprintf("🎟️ 宝くじを%d枚購入しました(-%dチップ / 残高: %d枚)\n%s",
		p.Count, p.Cost, p.Balance, lotteryPotLines(p.Prize, p.TicketsSold, p.Buyers, p.UserTickets, p.NextDrawAt))
}

// lotteryLastDrawLine renders the previous result. The winner is mentioned
// with <@id> exactly as the 9am announcement does (設計書 C-3a §3), and a
// lottery that has never produced a winner says so rather than printing an
// empty mention.
func lotteryLastDrawLine(last *casino.LotteryDraw) string {
	if last == nil {
		return "🏆 前回の抽選: まだありません"
	}
	return fmt.Sprintf("🏆 前回(%s): <@%s> が %dチップ 獲得(%d枚 / %d人)",
		last.Date, last.WinnerID, last.Prize, last.TicketsSold, last.Buyers)
}

// lotteryStatusMessage renders /lottery status (設計書 C-3a §3): the pot
// being sold into now, the asking user's own holding, the next 09:00 JST,
// and the last result.
func lotteryStatusMessage(v casino.LotteryView) string {
	var b strings.Builder
	b.WriteString("🎟️ **宝くじ**\n")
	b.WriteString(lotteryPotLines(v.Prize, v.TicketsSold, v.Buyers, v.UserTickets, v.NextDrawAt))
	b.WriteString("\n")
	b.WriteString(lotteryLastDrawLine(v.LastDraw))
	return b.String()
}

func (c *LotteryCommand) handleBuy(guildID, userID string, count int64, now time.Time) string {
	tickets, invalid := validateLotteryCount(count)
	if invalid != "" {
		return invalid
	}
	purchase, err := c.store.BuyLotteryTickets(guildID, userID, tickets, now)
	if err != nil {
		return translateLotteryError(err)
	}
	return lotteryPurchaseMessage(purchase)
}

func (c *LotteryCommand) handleStatus(guildID, userID string, now time.Time) string {
	view, err := c.store.LotteryStatus(guildID, userID, now)
	if err != nil {
		slog.Error("casino: lottery status failed", "error", redactInteractionError(err))
		return "❌ 宝くじの情報取得に失敗しました。"
	}
	return lotteryStatusMessage(view)
}

func (c *LotteryCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	content := ""
	if msg := requireGuildContext(i); msg != "" {
		content = msg
	} else {
		now, userID := time.Now(), resolveUserID(i)
		data := i.ApplicationCommandData()
		if err := c.store.EnsureCasinoAccess(i.GuildID, userID, now); err != nil {
			slog.Error("casino: EnsureCasinoAccess failed", "command", "lottery", "error", redactInteractionError(err))
			content = "❌ カジノの初期化に失敗しました。"
		} else if len(data.Options) == 0 {
			content = "❌ サブコマンドを指定してください（buy / status）。"
		} else {
			sub := data.Options[0]
			switch sub.Name {
			case "buy":
				var count int64
				for _, opt := range sub.Options {
					if opt.Name == "count" {
						count = opt.IntValue()
					}
				}
				content = c.handleBuy(i.GuildID, userID, count, now)
			case "status":
				content = c.handleStatus(i.GuildID, userID, now)
			default:
				content = "❌ 不明なサブコマンドです。"
			}
		}
	}
	return respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
}
