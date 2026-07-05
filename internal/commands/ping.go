package commands

import "github.com/bwmarrin/discordgo"

func init() { Register(&PingCommand{}) }

// PingCommand replies with a fixed pong message to confirm the bot is
// responding. Ported from TodayIsTodayBot/Commands/Handlers/PingCommand.cs.
type PingCommand struct{}

func (c *PingCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "ping",
		Description: "ボットが応答しているか確認します",
	}
}

func (c *PingCommand) responseText() string {
	return "🏓 Pong!"
}

func (c *PingCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: c.responseText(),
		},
	})
}
