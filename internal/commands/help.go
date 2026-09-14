package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func init() { Register(&HelpCommand{}) }

// HelpCommand lists every registered slash command. Ported from
// TodayIsTodayBot/Commands/Handlers/HelpCommand.cs; the registry it reads
// from is this same package's All(), replacing the injected CommandService.
type HelpCommand struct{}

func (c *HelpCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "help",
		Description: "利用可能なコマンドの一覧を表示します",
	}
}

func formatHelpText(defs []*discordgo.ApplicationCommand) string {
	if len(defs) == 0 {
		return "現在、利用可能なコマンドはありません。"
	}

	sorted := make([]*discordgo.ApplicationCommand, len(defs))
	copy(sorted, defs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var b strings.Builder
	b.WriteString("**📋 利用可能なコマンド一覧**\n\n")
	for _, d := range sorted {
		fmt.Fprintf(&b, "`/%s` - %s\n", d.Name, d.Description)
	}
	return b.String()
}

func (c *HelpCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	defs := make([]*discordgo.ApplicationCommand, 0, len(All()))
	for _, cmd := range All() {
		defs = append(defs, cmd.Definition())
	}

	return respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: formatHelpText(defs),
		},
	})
}
