package commands

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func init() { Register(&DiceCommand{}) }

// DiceCommand rolls one or more dice. Ported from
// TodayIsTodayBot/Commands/Handlers/DiceCommand.cs.
type DiceCommand struct{}

func (c *DiceCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "dice",
		Description: "サイコロを振ります。使い方: /dice [面数] [回数] (例: /dice 6 3 で6面ダイスを3回振る)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "sides",
				Description: "面数(2以上、既定6)",
				Required:    false,
			},
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "rolls",
				Description: "回数(1以上100以下、既定1)",
				Required:    false,
			},
		},
	}
}

func (c *DiceCommand) validateArgs(sides, rolls int) error {
	if sides < 2 {
		return fmt.Errorf("❌ 面数は2以上の整数で指定してください。")
	}
	if rolls < 1 {
		return fmt.Errorf("❌ 回数は1以上の整数で指定してください。")
	}
	if rolls > 100 {
		return fmt.Errorf("❌ 一度に振れる回数は100回までです。")
	}
	if sides > 1000000 {
		return fmt.Errorf("❌ 面数は1,000,000以下で指定してください。")
	}
	return nil
}

// roll executes the dice rolls using randFunc(sides) to obtain each
// 1..sides result (injected for deterministic testing; production Handle
// passes rand.Intn-based randomness).
func (c *DiceCommand) roll(sides, rolls int, randFunc func(sides int) int) (string, error) {
	if err := c.validateArgs(sides, rolls); err != nil {
		return "", err
	}

	results := make([]int, rolls)
	total := 0
	for i := 0; i < rolls; i++ {
		results[i] = randFunc(sides)
		total += results[i]
	}

	if rolls == 1 {
		return fmt.Sprintf("🎲 %d面ダイスを1回振って、結果は **%d** です！", sides, total), nil
	}

	strs := make([]string, len(results))
	for i, r := range results {
		strs[i] = strconv.Itoa(r)
	}
	// Full-width parens「（%s）」— matches DiceCommand.cs:80 exactly
	// (`$"🎲 {sides}面ダイスを{rolls}回振って、結果は **{total}** （{individualResults}）です！"`).
	// Do NOT use ASCII "(%s)" here.
	return fmt.Sprintf("🎲 %d面ダイスを%d回振って、結果は **%d** （%s）です！", sides, rolls, total, strings.Join(strs, "＋")), nil
}

func (c *DiceCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	sides := 6
	rolls := 1
	for _, opt := range i.ApplicationCommandData().Options {
		switch opt.Name {
		case "sides":
			sides = int(opt.IntValue())
		case "rolls":
			rolls = int(opt.IntValue())
		}
	}

	msg, err := c.roll(sides, rolls, func(n int) int { return rand.Intn(n) + 1 })
	if err != nil {
		msg = err.Error()
	}

	return respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: msg},
	})
}
