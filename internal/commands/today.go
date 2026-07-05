package commands

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/today"
	"github.com/bwmarrin/discordgo"
)

func init() { Register(&TodayCommand{todayClient: today.NewClient(http.DefaultClient)}) }

// TodayCommand reports anniversaries/birth-flower/famous-birthdays for a
// given date. Ported from TodayIsTodayBot/Commands/Handlers/TodayCommand.cs.
type TodayCommand struct {
	todayClient *today.Client
}

func (c *TodayCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name: "today",
		// Full-width parens「（…）」— matches TodayCommand.cs:19 exactly.
		Description: "今日は何の日かを表示します（例: /today または /today 0101 で1月1日の情報）",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type: discordgo.ApplicationCommandOptionString,
				Name: "date",
				// No direct .NET source string for this option's description
				// (new Discord slash-command UI copy) — full-width parens
				// chosen for internal consistency, not a ported string.
				Description: "MMdd形式の日付（例: 0101 = 1月1日）。省略時は今日",
				Required:    false,
			},
		},
	}
}

// dateParseErrorKind distinguishes the two invalid-input cases that
// TodayCommand.cs handles with two DIFFERENT user-facing messages —
// TodayCommand.cs:46-49 (malformed input: not exactly 4 digits, or
// non-numeric) vs. TodayCommand.cs:40-44 (well-formed 4 digits, but not a
// valid calendar date, e.g. "0230" = Feb 30). Do not collapse these into one
// generic error — the message text differs between the two cases.
type dateParseErrorKind int

const (
	dateParseErrorMalformed dateParseErrorKind = iota
	dateParseErrorInvalidCalendarDate
)

type dateParseError struct {
	kind dateParseErrorKind
}

func (e *dateParseError) Error() string {
	switch e.kind {
	case dateParseErrorMalformed:
		return "malformed MMdd date argument"
	case dateParseErrorInvalidCalendarDate:
		return "well-formed but invalid calendar date"
	default:
		return "date parse error"
	}
}

// parseDateArg mirrors TodayCommand.cs's MMdd parsing: exactly 4 numeric
// digits, first 2 = month, last 2 = day, validated as a real calendar date
// in the current year. Returns a *dateParseError so the caller (Handle) can
// tell the two invalid-input cases apart and pick the matching .NET-parity
// message for each.
func (c *TodayCommand) parseDateArg(arg string) (time.Time, error) {
	if len(arg) != 4 {
		return time.Time{}, &dateParseError{kind: dateParseErrorMalformed}
	}
	if _, err := strconv.Atoi(arg); err != nil {
		return time.Time{}, &dateParseError{kind: dateParseErrorMalformed}
	}
	month, _ := strconv.Atoi(arg[0:2])
	day, _ := strconv.Atoi(arg[2:4])

	year := time.Now().Year()
	d := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
	// time.Date silently normalizes out-of-range values (e.g. Feb 30 rolls
	// into March); detect that here to match .NET's ArgumentOutOfRangeException
	// rejecting invalid calendar dates (TodayCommand.cs:40-44).
	if int(d.Month()) != month || d.Day() != day {
		return time.Time{}, &dateParseError{kind: dateParseErrorInvalidCalendarDate}
	}
	return d, nil
}

func (c *TodayCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	var dateArg string
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "date" {
			dateArg = opt.StringValue()
		}
	}

	targetDate := time.Now()
	if dateArg != "" {
		parsed, err := c.parseDateArg(dateArg)
		if err != nil {
			content := todayDateErrorMessage(err)
			return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{Content: content},
			})
		}
		targetDate = parsed
	}

	result := c.todayClient.GetTodayInfo(context.Background(), targetDate)

	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: result},
	})
}

// todayDateErrorMessage maps a parseDateArg error to the exact .NET-parity
// user-facing message for its kind. Falls back to the malformed-input
// message for any error type it doesn't recognize (defensive default; in
// practice parseDateArg only ever returns *dateParseError).
func todayDateErrorMessage(err error) string {
	if dpe, ok := err.(*dateParseError); ok && dpe.kind == dateParseErrorInvalidCalendarDate {
		// Matches TodayCommand.cs:42 exactly.
		return "❌ 無効な日付です。MMdd形式で正しい日付を入力してください（例: 0101 = 1月1日）"
	}
	// Matches TodayCommand.cs:48 exactly.
	return "❌ 日付はMMdd形式で入力してください（例: 0101 = 1月1日、1225 = 12月25日）"
}
