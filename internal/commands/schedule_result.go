package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/SioKo-Shox3/todayistodayBot/internal/choseisama"
	"github.com/SioKo-Shox3/todayistodayBot/internal/store"
	"github.com/bwmarrin/discordgo"
)

func init() {
	Register(&ScheduleResultCommand{
		choseisamaClient: choseisama.NewClient(http.DefaultClient),
		store:            store.Default(),
	})
}

// ScheduleResultCommand shows a chosei-sama event's current
// participant-availability aggregate as a Discord embed.
type ScheduleResultCommand struct {
	choseisamaClient *choseisama.Client
	store            *store.Store
}

func (c *ScheduleResultCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "schedule-result",
		Description: "日程調整アンケートの結果を表示します（省略時はこのチャンネルの最新イベント）",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "event", Description: "対象イベントの参照（省略時はこのチャンネルの最新イベント）", Required: false},
		},
	}
}

// errNoStoredEvent is the exact Japanese message shown when channelID (or
// the given ref within it) has no matching stored EventRecord.
var errNoStoredEvent = errors.New("❌ このチャンネルではまだ日程調整イベントが作成されていません")

// resolveTargetEventRecord looks up the EventRecord for channelID: an
// exact channel+ref match if ref is non-empty, otherwise the channel's
// most recently created event. Shared by schedule_result.go and
// reminder.go (both need "target event for this channel" resolution).
func resolveTargetEventRecord(st *store.Store, channelID, ref string) (store.EventRecord, error) {
	if ref != "" {
		record, ok, err := st.FindByChannelAndRef(channelID, ref)
		if err != nil {
			return store.EventRecord{}, fmt.Errorf("❌ イベント情報の読み込みに失敗しました。")
		}
		if !ok {
			return store.EventRecord{}, errNoStoredEvent
		}
		return record, nil
	}

	record, ok, err := st.FindLatestByChannel(channelID)
	if err != nil {
		return store.EventRecord{}, fmt.Errorf("❌ イベント情報の読み込みに失敗しました。")
	}
	if !ok {
		return store.EventRecord{}, errNoStoredEvent
	}
	return record, nil
}

// buildResultEmbed fetches ref's snapshot via GetEvent (chosei-sama's
// public read endpoint — no auth) and formats participant availability as
// a Discord embed.
func (c *ScheduleResultCommand) buildResultEmbed(ctx context.Context, channelID, refArg string) (*discordgo.MessageEmbed, error) {
	record, err := resolveTargetEventRecord(c.store, channelID, refArg)
	if err != nil {
		return nil, err
	}

	snapshot, err := c.choseisamaClient.GetEvent(ctx, record.EventID)
	if err != nil {
		var apiErr *choseisama.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("❌ このイベントはchosei-sama側で見つかりませんでした。")
		}
		slog.Error("choseisama: GetEvent failed", "error", redactInteractionError(err))
		return nil, fmt.Errorf("❌ 日程調整結果の取得に失敗しました。")
	}

	return formatResultEmbed(snapshot, c.choseisamaClient.PublicEventURL(record.PublicSlug)), nil
}

func formatResultEmbed(snapshot *choseisama.EventSnapshot, publicURL string) *discordgo.MessageEmbed {
	summaryByID := make(map[string]choseisama.AvailabilitySummary, len(snapshot.Aggregate))
	for _, s := range snapshot.Aggregate {
		summaryByID[s.CandidateID] = s
	}

	fields := make([]*discordgo.MessageEmbedField, 0, len(snapshot.Candidates))
	for _, cand := range snapshot.Candidates {
		s := summaryByID[cand.ID]
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   cand.Label,
			Value:  fmt.Sprintf("✅ %d　🤔 %d　❌ %d", s.OK, s.Maybe, s.NG),
			Inline: false,
		})
	}

	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("📊 %s の回答状況", snapshot.Title),
		URL:         publicURL,
		Description: fmt.Sprintf("参加者数: %d人", len(snapshot.Participants)),
		Color:       0x3498db,
		Fields:      fields,
	}
}

func (c *ScheduleResultCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	var refArg string
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "event" {
			refArg = opt.StringValue()
		}
	}

	embed, err := c.buildResultEmbed(context.Background(), i.ChannelID, refArg)

	respData := &discordgo.InteractionResponseData{}
	if err != nil {
		respData.Content = err.Error()
	} else {
		respData.Embeds = []*discordgo.MessageEmbed{embed}
	}

	return respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: respData,
	})
}
