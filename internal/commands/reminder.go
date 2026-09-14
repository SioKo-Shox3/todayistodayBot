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
	Register(&ReminderCommand{
		choseisamaClient: choseisama.NewClient(http.DefaultClient),
		store:            store.Default(),
	})
}

// ReminderCommand manages a chosei-sama event's Discord reminder setting
// (create/register a webhook, disable it, or show its current status).
type ReminderCommand struct {
	choseisamaClient *choseisama.Client
	store            *store.Store
}

func (c *ReminderCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "reminder",
		Description: "日程調整のDiscordリマインダーを管理します（/reminder set, off, status）",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "set",
				Description: "このチャンネルにリマインダーWebhookを作成し、chosei-samaに登録します",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "all-ok-hours", Description: "全員回答済み候補の何時間前に通知するか（1〜168、既定3）", Required: false, MinValue: floatPtr(1), MaxValue: 168},
					{Type: discordgo.ApplicationCommandOptionInteger, Name: "deadline-hours", Description: "回答締切の何時間前に通知するか（1〜168、既定24）。指定すると締切リマインダーが有効になります", Required: false, MinValue: floatPtr(1), MaxValue: 168},
					{Type: discordgo.ApplicationCommandOptionBoolean, Name: "mention-everyone", Description: "@everyoneで通知するか（既定false）", Required: false},
					{Type: discordgo.ApplicationCommandOptionString, Name: "event", Description: "対象イベントの参照（省略時はこのチャンネルの最新イベント）", Required: false},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "off",
				Description: "このイベントのリマインダーを無効化します",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: "event", Description: "対象イベントの参照（省略時はこのチャンネルの最新イベント）", Required: false},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "status",
				Description: "このイベントの現在のリマインダー設定を表示します",
				Options: []*discordgo.ApplicationCommandOption{
					{Type: discordgo.ApplicationCommandOptionString, Name: "event", Description: "対象イベントの参照（省略時はこのチャンネルの最新イベント）", Required: false},
				},
			},
		},
	}
}

// discordWebhookCreator abstracts *discordgo.Session's WebhookCreate
// method so handleSet's logic can be unit tested without hitting
// Discord's real REST API. *discordgo.Session satisfies this interface
// structurally; production Handle() passes s directly with no adapter.
type discordWebhookCreator interface {
	WebhookCreate(channelID, name, avatar string, options ...discordgo.RequestOption) (*discordgo.Webhook, error)
}

func enabledText(b bool) string {
	if b {
		return "有効"
	}
	return "無効"
}

// stringOptionValue returns the value of the string option named name
// within opts, or "" if absent.
func stringOptionValue(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range opts {
		if opt.Name == name {
			return opt.StringValue()
		}
	}
	return ""
}

// handleSet creates a Discord Incoming Webhook in channelID and registers
// it with chosei-sama as the target event's Discord reminder destination.
// Parameter order is (ctx, webhookCreator, opts, channelID) — matching
// handleOff/handleStatus's (ctx, opts, channelID) order plus the extra
// webhookCreator dependency in second position, for consistency across
// all three subcommand handlers.
//
// Option-to-request-field mapping (a deliberate, disclosed interpretation
// of design spec line 52-57, which lists /reminder set's optional args
// without an explicit "enable deadline reminders" toggle): AllOkEnabled is
// always true when /reminder set runs; DeadlineEnabled becomes true if and
// only if the caller supplied "deadline-hours" — its presence is read as
// intent to enable deadline reminders, matching chosei-sama's own default
// of deadlineEnabled=false when nothing is specified.
func (c *ReminderCommand) handleSet(ctx context.Context, webhookCreator discordWebhookCreator, opts []*discordgo.ApplicationCommandInteractionDataOption, channelID string) string {
	refArg := stringOptionValue(opts, "event")
	record, err := resolveTargetEventRecord(c.store, channelID, refArg)
	if err != nil {
		return err.Error()
	}

	allOkHours := 3
	deadlineHours := 24
	deadlineEnabled := false
	mentionEveryone := false
	for _, opt := range opts {
		switch opt.Name {
		case "all-ok-hours":
			allOkHours = int(opt.IntValue())
		case "deadline-hours":
			deadlineHours = int(opt.IntValue())
			deadlineEnabled = true
		case "mention-everyone":
			mentionEveryone = opt.BoolValue()
		}
	}
	if allOkHours < 1 || allOkHours > 168 {
		return "❌ all-ok-hoursは1〜168の範囲で指定してください。"
	}
	if deadlineHours < 1 || deadlineHours > 168 {
		return "❌ deadline-hoursは1〜168の範囲で指定してください。"
	}

	webhook, err := webhookCreator.WebhookCreate(channelID, "chosei-sama リマインダー", "")
	if err != nil {
		slog.Error("discord: WebhookCreate failed", "error", redactInteractionError(err))
		return "❌ Discord Webhookの作成に失敗しました。Botに「Webhookの管理」権限があるか確認してください。"
	}
	webhookURL := fmt.Sprintf("https://discord.com/api/webhooks/%s/%s", webhook.ID, webhook.Token)

	setting, err := c.choseisamaClient.SetDiscordReminder(ctx, record.EventID, record.OwnerToken, choseisama.SetDiscordReminderRequest{
		WebhookURL:          webhookURL,
		AllOkEnabled:        true,
		AllOkHoursBefore:    allOkHours,
		DeadlineEnabled:     deadlineEnabled,
		DeadlineHoursBefore: deadlineHours,
		MentionEveryone:     mentionEveryone,
	})
	if err != nil {
		slog.Error("choseisama: SetDiscordReminder failed", "error", redactInteractionError(err))
		return "❌ リマインダーの登録に失敗しました。"
	}

	return fmt.Sprintf("✅ リマインダーを設定しました。\n全員回答済み通知: %s / %d時間前\n締切通知: %s / %d時間前\n@everyone: %s",
		enabledText(setting.AllOkEnabled), setting.AllOkHoursBefore,
		enabledText(setting.DeadlineEnabled), setting.DeadlineHoursBefore,
		enabledText(setting.MentionEveryone))
}

// handleOff disables both reminder kinds for the target event by
// re-calling SetDiscordReminder with both enabled flags false — chosei-sama
// has no separate "delete Discord reminder" endpoint. Hours values are
// irrelevant once both flags are false; 3/24 (chosei-sama's own schema
// defaults) are sent for a well-formed, in-range request body.
func (c *ReminderCommand) handleOff(ctx context.Context, opts []*discordgo.ApplicationCommandInteractionDataOption, channelID string) string {
	refArg := stringOptionValue(opts, "event")
	record, err := resolveTargetEventRecord(c.store, channelID, refArg)
	if err != nil {
		return err.Error()
	}

	_, err = c.choseisamaClient.SetDiscordReminder(ctx, record.EventID, record.OwnerToken, choseisama.SetDiscordReminderRequest{
		AllOkEnabled:        false,
		AllOkHoursBefore:    3,
		DeadlineEnabled:     false,
		DeadlineHoursBefore: 24,
		MentionEveryone:     false,
	})
	if err != nil {
		var apiErr *choseisama.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "discord_webhook_url_required" {
			return "❌ まだリマインダーが設定されていません。先に `/reminder set` を実行してください。"
		}
		slog.Error("choseisama: SetDiscordReminder (off) failed", "error", redactInteractionError(err))
		return "❌ リマインダーの無効化に失敗しました。"
	}
	return "✅ リマインダーを無効化しました。"
}

// handleStatus shows the target event's current Discord reminder
// configuration via GET /api/v1/events/:ref/owner (owner-token auth).
func (c *ReminderCommand) handleStatus(ctx context.Context, opts []*discordgo.ApplicationCommandInteractionDataOption, channelID string) string {
	refArg := stringOptionValue(opts, "event")
	record, err := resolveTargetEventRecord(c.store, channelID, refArg)
	if err != nil {
		return err.Error()
	}

	owner, err := c.choseisamaClient.GetOwnerView(ctx, record.EventID, record.OwnerToken)
	if err != nil {
		var apiErr *choseisama.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
			return "❌ このイベントの操作権限が確認できませんでした。"
		}
		slog.Error("choseisama: GetOwnerView failed", "error", redactInteractionError(err))
		return "❌ リマインダー設定の取得に失敗しました。"
	}

	if owner.DiscordReminder == nil {
		return fmt.Sprintf("📋 %s のリマインダーはまだ設定されていません。`/reminder set` で設定できます。", owner.Event.Title)
	}

	setting := owner.DiscordReminder
	return fmt.Sprintf("📋 %s のリマインダー設定\n全員回答済み通知: %s / %d時間前\n締切通知: %s / %d時間前\n@everyone: %s\nWebhook: %s",
		owner.Event.Title,
		enabledText(setting.AllOkEnabled), setting.AllOkHoursBefore,
		enabledText(setting.DeadlineEnabled), setting.DeadlineHoursBefore,
		enabledText(setting.MentionEveryone),
		setting.URLPreview)
}

func (c *ReminderCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	data := i.ApplicationCommandData()
	var content string
	if len(data.Options) == 0 {
		content = "❌ サブコマンドを指定してください（set / off / status）。"
	} else {
		sub := data.Options[0]
		switch sub.Name {
		case "set":
			content = c.handleSet(context.Background(), s, sub.Options, i.ChannelID)
		case "off":
			content = c.handleOff(context.Background(), sub.Options, i.ChannelID)
		case "status":
			content = c.handleStatus(context.Background(), sub.Options, i.ChannelID)
		default:
			content = "❌ 不明なサブコマンドです。"
		}
	}

	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
}
