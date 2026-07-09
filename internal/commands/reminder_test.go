package commands

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/choseisama"
	"github.com/SioKo-Shox3/todayistodayBot/internal/store"
	"github.com/bwmarrin/discordgo"
)

func TestReminderCommand_Definition(t *testing.T) {
	cmd := &ReminderCommand{}
	def := cmd.Definition()
	if def.Name != "reminder" {
		t.Fatalf("expected command name 'reminder', got %q", def.Name)
	}
	if len(def.Options) != 3 {
		t.Fatalf("expected 3 subcommands (set, off, status), got %d", len(def.Options))
	}
	wantNames := []string{"set", "off", "status"}
	for i, opt := range def.Options {
		if opt.Type != discordgo.ApplicationCommandOptionSubCommand {
			t.Fatalf("option[%d] %q: expected Type ApplicationCommandOptionSubCommand, got %v", i, opt.Name, opt.Type)
		}
		if opt.Name != wantNames[i] {
			t.Fatalf("option[%d]: expected name %q, got %q", i, wantNames[i], opt.Name)
		}
	}
}

// fakeWebhookCreator implements discordWebhookCreator without hitting
// Discord's real REST API. This is necessary because *discordgo.Session's
// endpoint URLs are computed once at package-init time from a var block
// (discordgo@v0.29.0/endpoints.go:20-26), so there is no way to redirect
// a real *discordgo.Session's REST calls to an httptest.Server.
type fakeWebhookCreator struct {
	webhook      *discordgo.Webhook
	err          error
	gotChannelID string
}

func (f *fakeWebhookCreator) WebhookCreate(channelID, name, avatar string, options ...discordgo.RequestOption) (*discordgo.Webhook, error) {
	f.gotChannelID = channelID
	if f.err != nil {
		return nil, f.err
	}
	return f.webhook, nil
}

func TestReminderCommand_HandleSet_Success(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/events/event-1/discord-reminder" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"event": map[string]interface{}{
				"id": "event-1", "publicSlug": "abc123", "title": "忘年会", "ownerName": "たろう", "status": "open",
				"candidates": []interface{}{}, "participants": []interface{}{}, "aggregate": []interface{}{},
				"createdAt": "x", "updatedAt": "x",
			},
			"discordReminder": map[string]interface{}{
				"id": "r1", "eventId": "event-1", "urlPreview": "https://discord.com/api/webhooks/1/ab...yz",
				"allOkEnabled": true, "allOkHoursBefore": 5,
				"deadlineEnabled": true, "deadlineHoursBefore": 12,
				"mentionEveryone": true, "createdAt": "x", "updatedAt": "x",
			},
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "events.json"))
	if err := st.Save(store.EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "owner-tok", CreatedAt: time.Now(), CreatedBy: "u1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	choseisamaClient := choseisama.NewClient(server.Client())
	choseisamaClient.BaseURL = server.URL
	cmd := &ReminderCommand{choseisamaClient: choseisamaClient, store: st}

	fakeCreator := &fakeWebhookCreator{webhook: &discordgo.Webhook{ID: "999", Token: "wh-token"}}
	opts := []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "all-ok-hours", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(5)},
		{Name: "deadline-hours", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(12)},
		{Name: "mention-everyone", Type: discordgo.ApplicationCommandOptionBoolean, Value: true},
	}

	content := cmd.handleSet(context.Background(), fakeCreator, opts, "channel-1")

	if fakeCreator.gotChannelID != "channel-1" {
		t.Fatalf("expected WebhookCreate to be called with channel-1, got %q", fakeCreator.gotChannelID)
	}
	if gotBody["webhookUrl"] != "https://discord.com/api/webhooks/999/wh-token" {
		t.Fatalf("unexpected webhookUrl sent to chosei-sama: %v", gotBody["webhookUrl"])
	}
	if gotBody["allOkEnabled"] != true || gotBody["allOkHoursBefore"] != float64(5) {
		t.Fatalf("unexpected all-ok fields: %v", gotBody)
	}
	if gotBody["deadlineEnabled"] != true || gotBody["deadlineHoursBefore"] != float64(12) {
		t.Fatalf("expected deadlineEnabled=true (deadline-hours arg was supplied) with deadlineHoursBefore=12, got: %v", gotBody)
	}
	if gotBody["mentionEveryone"] != true {
		t.Fatalf("expected mentionEveryone=true, got: %v", gotBody)
	}
	// content[0:1] != "✅" would compare a single BYTE (the first byte of
	// "✅"'s 3-byte UTF-8 encoding) against the full 3-byte string, which
	// can never be equal — strings.HasPrefix is the correct rune-safe
	// check for "does content start with this emoji".
	if content == "" || !strings.HasPrefix(content, "✅") {
		t.Fatalf("expected a success message starting with ✅, got: %q", content)
	}
}

func TestReminderCommand_HandleSet_DefaultsWhenOptionsOmitted(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"event":           map[string]interface{}{"id": "event-1", "publicSlug": "abc123", "title": "x", "ownerName": "x", "status": "open", "candidates": []interface{}{}, "participants": []interface{}{}, "aggregate": []interface{}{}, "createdAt": "x", "updatedAt": "x"},
			"discordReminder": map[string]interface{}{"id": "r1", "eventId": "event-1", "urlPreview": "x", "allOkEnabled": true, "allOkHoursBefore": 3, "deadlineEnabled": false, "deadlineHoursBefore": 24, "mentionEveryone": false, "createdAt": "x", "updatedAt": "x"},
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "events.json"))
	if err := st.Save(store.EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "owner-tok", CreatedAt: time.Now(), CreatedBy: "u1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	choseisamaClient := choseisama.NewClient(server.Client())
	choseisamaClient.BaseURL = server.URL
	cmd := &ReminderCommand{choseisamaClient: choseisamaClient, store: st}
	fakeCreator := &fakeWebhookCreator{webhook: &discordgo.Webhook{ID: "1", Token: "tok"}}

	cmd.handleSet(context.Background(), fakeCreator, nil, "channel-1")

	if gotBody["allOkEnabled"] != true || gotBody["allOkHoursBefore"] != float64(3) {
		t.Fatalf("expected chosei-sama's own defaults (allOkEnabled=true, allOkHoursBefore=3) when omitted, got: %v", gotBody)
	}
	if gotBody["deadlineEnabled"] != false || gotBody["deadlineHoursBefore"] != float64(24) {
		t.Fatalf("expected deadlineEnabled=false (no deadline-hours arg supplied) with the schema default deadlineHoursBefore=24, got: %v", gotBody)
	}
	if gotBody["mentionEveryone"] != false {
		t.Fatalf("expected mentionEveryone=false by default, got: %v", gotBody)
	}
}

func TestReminderCommand_HandleSet_WebhookCreateFailure_ReturnsJapaneseMessage(t *testing.T) {
	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "events.json"))
	if err := st.Save(store.EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "owner-tok", CreatedAt: time.Now(), CreatedBy: "u1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cmd := &ReminderCommand{choseisamaClient: choseisama.NewClient(http.DefaultClient), store: st}
	fakeCreator := &fakeWebhookCreator{err: errors.New("missing permissions")}

	content := cmd.handleSet(context.Background(), fakeCreator, nil, "channel-1")
	if content == "" || !strings.HasPrefix(content, "❌") {
		t.Fatalf("expected a ❌ error message, got: %q", content)
	}
}

func TestReminderCommand_HandleOff_Success(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"event":           map[string]interface{}{"id": "event-1", "publicSlug": "abc123", "title": "x", "ownerName": "x", "status": "open", "candidates": []interface{}{}, "participants": []interface{}{}, "aggregate": []interface{}{}, "createdAt": "x", "updatedAt": "x"},
			"discordReminder": map[string]interface{}{"id": "r1", "eventId": "event-1", "urlPreview": "x", "allOkEnabled": false, "allOkHoursBefore": 3, "deadlineEnabled": false, "deadlineHoursBefore": 24, "mentionEveryone": false, "createdAt": "x", "updatedAt": "x"},
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "events.json"))
	if err := st.Save(store.EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "owner-tok", CreatedAt: time.Now(), CreatedBy: "u1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	choseisamaClient := choseisama.NewClient(server.Client())
	choseisamaClient.BaseURL = server.URL
	cmd := &ReminderCommand{choseisamaClient: choseisamaClient, store: st}

	content := cmd.handleOff(context.Background(), nil, "channel-1")
	if content != "✅ リマインダーを無効化しました。" {
		t.Fatalf("unexpected content: %q", content)
	}
	if gotBody["allOkEnabled"] != false || gotBody["deadlineEnabled"] != false {
		t.Fatalf("expected both enabled flags false, got: %v", gotBody)
	}
}

func TestReminderCommand_HandleOff_NeverConfigured_ReturnsFriendlyMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "discord_webhook_url_required"})
	}))
	defer server.Close()

	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "events.json"))
	if err := st.Save(store.EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "owner-tok", CreatedAt: time.Now(), CreatedBy: "u1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	choseisamaClient := choseisama.NewClient(server.Client())
	choseisamaClient.BaseURL = server.URL
	cmd := &ReminderCommand{choseisamaClient: choseisamaClient, store: st}

	content := cmd.handleOff(context.Background(), nil, "channel-1")
	if content != "❌ まだリマインダーが設定されていません。先に `/reminder set` を実行してください。" {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestReminderCommand_HandleStatus_NotYetConfigured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"event":           map[string]interface{}{"id": "event-1", "publicSlug": "abc123", "title": "忘年会", "ownerName": "x", "status": "open", "candidates": []interface{}{}, "participants": []interface{}{}, "aggregate": []interface{}{}, "createdAt": "x", "updatedAt": "x"},
			"webhooks":        []interface{}{},
			"discordReminder": nil,
			"urls":            map[string]interface{}{"publicPath": "x", "ownerPath": "y", "exportPath": "z"},
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "events.json"))
	if err := st.Save(store.EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "owner-tok", CreatedAt: time.Now(), CreatedBy: "u1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	choseisamaClient := choseisama.NewClient(server.Client())
	choseisamaClient.BaseURL = server.URL
	cmd := &ReminderCommand{choseisamaClient: choseisamaClient, store: st}

	content := cmd.handleStatus(context.Background(), nil, "channel-1")
	if content != "📋 忘年会 のリマインダーはまだ設定されていません。`/reminder set` で設定できます。" {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestReminderCommand_HandleStatus_Configured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"event":    map[string]interface{}{"id": "event-1", "publicSlug": "abc123", "title": "忘年会", "ownerName": "x", "status": "open", "candidates": []interface{}{}, "participants": []interface{}{}, "aggregate": []interface{}{}, "createdAt": "x", "updatedAt": "x"},
			"webhooks": []interface{}{},
			"discordReminder": map[string]interface{}{
				"id": "r1", "eventId": "event-1", "urlPreview": "https://discord.com/api/webhooks/1/ab...yz",
				"allOkEnabled": true, "allOkHoursBefore": 3, "deadlineEnabled": false, "deadlineHoursBefore": 24,
				"mentionEveryone": false, "createdAt": "x", "updatedAt": "x",
			},
			"urls": map[string]interface{}{"publicPath": "x", "ownerPath": "y", "exportPath": "z"},
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "events.json"))
	if err := st.Save(store.EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "owner-tok", CreatedAt: time.Now(), CreatedBy: "u1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	choseisamaClient := choseisama.NewClient(server.Client())
	choseisamaClient.BaseURL = server.URL
	cmd := &ReminderCommand{choseisamaClient: choseisamaClient, store: st}

	content := cmd.handleStatus(context.Background(), nil, "channel-1")
	want := "📋 忘年会 のリマインダー設定\n全員回答済み通知: 有効 / 3時間前\n締切通知: 無効 / 24時間前\n@everyone: 無効\nWebhook: https://discord.com/api/webhooks/1/ab...yz"
	if content != want {
		t.Fatalf("unexpected content:\n got: %q\nwant: %q", content, want)
	}
}
