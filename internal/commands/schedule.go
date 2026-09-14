package commands

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/choseisama"
	"github.com/SioKo-Shox3/todayistodayBot/internal/store"
	"github.com/bwmarrin/discordgo"
)

func init() {
	Register(&ScheduleCommand{
		choseisamaClient: choseisama.NewClient(http.DefaultClient),
		store:            store.Default(),
	})
}

// ScheduleCommand creates a chosei-sama schedule-coordination event from
// Discord slash-command args, persists a local reference to it, and posts
// an embed with the public URL.
type ScheduleCommand struct {
	choseisamaClient *choseisama.Client
	store            *store.Store
}

// floatPtr returns a pointer to f — a small helper for discordgo's
// ApplicationCommandOption.MinValue field, which is *float64 (a pointer,
// since the zero value 0 must be distinguishable from "no minimum set").
// Shared with reminder.go's Definition() (Task 11) via this package's
// normal Go scoping — defined here since schedule.go is the first
// command to need it. Do not redeclare it in reminder.go.
func floatPtr(f float64) *float64 {
	return &f
}

func (c *ScheduleCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "schedule",
		Description: "日程調整アンケートを作成します（例: /schedule 忘年会 +1 3 19:00 → 明日から3日分、19:00で作成）",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "title", Description: "イベントのタイトル", Required: true},
			{Type: discordgo.ApplicationCommandOptionString, Name: "start", Description: "起点日（+1=明日、0=今日、-1=昨日 のような符号付き日数オフセット）", Required: true},
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "days", Description: "候補日数（1〜50）", Required: true, MinValue: floatPtr(1), MaxValue: 50},
			{Type: discordgo.ApplicationCommandOptionString, Name: "time", Description: "候補の時刻（HH:mm形式、例: 19:00）", Required: true},
		},
	}
}

// parseStartOffset parses the "start" arg — a signed day-count offset from
// "today". strconv.Atoi already accepts a leading "+" sign.
func parseStartOffset(arg string) (int, error) {
	offset, err := strconv.Atoi(arg)
	if err != nil {
		return 0, fmt.Errorf("❌ 起点日の形式が正しくありません。例: `+1`（明日）、`0`（今日）、`-1`（昨日）")
	}
	return offset, nil
}

// parseTimeArg parses the "time" arg as HH:mm.
func parseTimeArg(arg string) (hour, minute int, err error) {
	t, parseErr := time.Parse("15:04", arg)
	if parseErr != nil {
		return 0, 0, fmt.Errorf("❌ 時刻の形式が正しくありません。正しい形式: `19:00`、`09:30` など")
	}
	return t.Hour(), t.Minute(), nil
}

// validateScheduleDays enforces chosei-sama's candidatesPerEvent limit
// (chosei-sama/src/shared/schema.ts:5, LIMITS.candidatesPerEvent = 50).
// Kept even though Discord's client now also enforces this via
// MinValue/MaxValue above — defense in depth against direct API calls or
// stale client caches.
func validateScheduleDays(days int) error {
	if days < 1 || days > 50 {
		return fmt.Errorf("❌ 日数は1〜50の範囲で指定してください。")
	}
	return nil
}

var japaneseWeekdays = [...]string{"日", "月", "火", "水", "木", "金", "土"} // indexed by time.Weekday() (Sunday=0)

// buildCandidates generates `days` daily candidates starting at base's
// calendar date, each at hour:minute in base's own time.Location().
// StartsAt is formatted in UTC per chosei-sama's candidateInputSchema,
// which requires a bare (UTC-only) ISO 8601 datetime string.
func buildCandidates(base time.Time, days, hour, minute int) []choseisama.Candidate {
	candidates := make([]choseisama.Candidate, days)
	for i := 0; i < days; i++ {
		d := base.AddDate(0, 0, i)
		startsAt := time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, d.Location())
		label := fmt.Sprintf("%04d-%02d-%02d %02d:%02d (%s)",
			startsAt.Year(), int(startsAt.Month()), startsAt.Day(), startsAt.Hour(), startsAt.Minute(),
			japaneseWeekdays[int(startsAt.Weekday())])
		candidates[i] = choseisama.Candidate{
			Label:    label,
			StartsAt: startsAt.UTC().Format(time.RFC3339),
		}
	}
	return candidates
}

// resolveDisplayName returns the invoking user's Discord display name. A
// slash-command Interaction populates exactly one of Member (guild) or
// User (DM).
func resolveDisplayName(i *discordgo.InteractionCreate) string {
	if i.Member != nil {
		return i.Member.DisplayName()
	}
	if i.User != nil {
		return i.User.DisplayName()
	}
	return "unknown"
}

// resolveUserID returns the invoking user's Discord user ID (for
// EventRecord.CreatedBy).
func resolveUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// buildAndCreateEvent validates args, generates candidates, calls
// CreateEvent, persists the resulting EventRecord, and returns the embed
// to post — or a Go error whose Error() text IS the exact Japanese
// user-facing message to show instead. now is injected for deterministic
// tests; production Handle() passes time.Now().
func (c *ScheduleCommand) buildAndCreateEvent(ctx context.Context, title, startArg string, days int, timeArg, channelID, userID, displayName string, now time.Time) (*discordgo.MessageEmbed, error) {
	if err := validateScheduleDays(days); err != nil {
		return nil, err
	}
	offset, err := parseStartOffset(startArg)
	if err != nil {
		return nil, err
	}
	hour, minute, err := parseTimeArg(timeArg)
	if err != nil {
		return nil, err
	}

	base := now.AddDate(0, 0, offset)
	candidates := buildCandidates(base, days, hour, minute)

	resp, err := c.choseisamaClient.CreateEvent(ctx, choseisama.CreateEventRequest{
		Title:      title,
		OwnerName:  displayName,
		Candidates: candidates,
	})
	if err != nil {
		slog.Error("choseisama: CreateEvent failed", "error", redactInteractionError(err))
		return nil, fmt.Errorf("❌ 日程調整の作成に失敗しました。")
	}

	if err := c.store.Save(store.EventRecord{
		ChannelID:  channelID,
		EventID:    resp.Event.ID,
		PublicSlug: resp.Event.PublicSlug,
		OwnerToken: resp.Secrets.OwnerToken,
		CreatedAt:  now,
		CreatedBy:  userID,
	}); err != nil {
		slog.Error("store: Save failed", "error", redactInteractionError(err))
		return nil, fmt.Errorf("❌ 日程調整の保存に失敗しました。")
	}

	return buildScheduleEmbed(resp, displayName), nil
}

func buildScheduleEmbed(resp *choseisama.CreateEventResponse, displayName string) *discordgo.MessageEmbed {
	fields := make([]*discordgo.MessageEmbedField, 0, len(resp.Event.Candidates))
	for i, cand := range resp.Event.Candidates {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("候補%d", i+1),
			Value:  cand.Label,
			Inline: false,
		})
	}
	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("📅 %s", resp.Event.Title),
		URL:         resp.URLs.PublicPath,
		Description: "参加可能な日程を選んで回答してください！\n" + resp.URLs.PublicPath,
		Color:       0x3498db,
		Footer:      &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("作成者: %s", displayName)},
		Fields:      fields,
	}
}

func (c *ScheduleCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	data := i.ApplicationCommandData()
	var title, startArg, timeArg string
	var days int
	for _, opt := range data.Options {
		switch opt.Name {
		case "title":
			title = opt.StringValue()
		case "start":
			startArg = opt.StringValue()
		case "days":
			days = int(opt.IntValue())
		case "time":
			timeArg = opt.StringValue()
		}
	}

	embed, err := c.buildAndCreateEvent(context.Background(), title, startArg, days, timeArg, i.ChannelID, resolveUserID(i), resolveDisplayName(i), time.Now())

	respData := &discordgo.InteractionResponseData{}
	if err != nil {
		respData.Content = err.Error()
	} else {
		respData.Embeds = []*discordgo.MessageEmbed{embed}
	}

	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: respData,
	})
}
