package commands

import (
	"context"
	"fmt"
	"net/http"

	"github.com/SioKo-Shox3/todayistodayBot/internal/weather"
	"github.com/bwmarrin/discordgo"
)

func init() { Register(&WeatherCommand{weatherClient: weather.NewClient(http.DefaultClient)}) }

// WeatherCommand fetches weather via Open-Meteo. Ported from
// TodayIsTodayBot/Commands/Handlers/WeatherCommand.cs; the city option
// replaces the old space-joined free-text args.
type WeatherCommand struct {
	weatherClient *weather.Client
}

func (c *WeatherCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name: "weather",
		// Full-width parens「（…）」— matches WeatherCommand.cs:19 exactly.
		Description: "指定した地域の天気情報を取得します（日本語・英語対応、例: /weather 東京 または /weather Tokyo）",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type: discordgo.ApplicationCommandOptionString,
				Name: "city",
				// No direct .NET source string for this option's description
				// (new Discord slash-command UI copy) — full-width parens
				// chosen for internal consistency with the rest of the
				// command's user-facing text, not a ported string.
				Description: "都市名（日本語または英語、例: 東京、Tokyo）",
				Required:    false,
			},
		},
	}
}

func (c *WeatherCommand) helpText() string {
	return "**🌤️ 天気コマンドの使い方**\n\n" +
		"**使用例**:\n" +
		"`/weather 東京` - 東京の天気を取得\n" +
		"`/weather 大阪` - 大阪の天気を取得\n" +
		"`/weather Tokyo` - 英語の都市名でも可\n\n" +
		weather.AvailableCities()
}

// buildResponseContent returns the message text Handle sends back for the
// given city argument (empty string = no city option supplied). Extracted
// as a pure function — decoupled from *discordgo.Session/InteractionCreate —
// so tests can assert on the exact response content without needing to
// intercept discordgo's InteractionRespond HTTP call.
func (c *WeatherCommand) buildResponseContent(cityName string) string {
	if cityName == "" {
		return c.helpText()
	}

	result, err := c.weatherClient.GetWeather(context.Background(), cityName)
	if err != nil {
		return fmt.Sprintf("❌ 天気情報の取得中にエラーが発生しました: %s", err.Error())
	}
	return result
}

func (c *WeatherCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	var cityName string
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "city" {
			cityName = opt.StringValue()
		}
	}

	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: c.buildResponseContent(cityName)},
	})
}
