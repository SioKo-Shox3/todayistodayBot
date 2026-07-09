package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/choseisama"
	"github.com/SioKo-Shox3/todayistodayBot/internal/store"
)

func TestScheduleResultCommand_Definition(t *testing.T) {
	cmd := &ScheduleResultCommand{}
	def := cmd.Definition()
	if def.Name != "schedule-result" {
		t.Fatalf("expected command name 'schedule-result', got %q", def.Name)
	}
	if len(def.Options) != 1 || def.Options[0].Name != "event" || def.Options[0].Required {
		t.Fatalf("expected exactly 1 optional 'event' option, got: %+v", def.Options)
	}
}

func TestResolveTargetEventRecord_EmptyRef_ReturnsLatestInChannel(t *testing.T) {
	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "events.json"))
	older := store.EventRecord{ChannelID: "channel-1", EventID: "event-old", PublicSlug: "old", OwnerToken: "t1", CreatedAt: time.Now().Add(-time.Hour), CreatedBy: "u1"}
	newer := store.EventRecord{ChannelID: "channel-1", EventID: "event-new", PublicSlug: "new", OwnerToken: "t2", CreatedAt: time.Now(), CreatedBy: "u1"}
	if err := st.Save(older); err != nil {
		t.Fatalf("Save older: %v", err)
	}
	if err := st.Save(newer); err != nil {
		t.Fatalf("Save newer: %v", err)
	}

	record, err := resolveTargetEventRecord(st, "channel-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.EventID != "event-new" {
		t.Fatalf("expected the most recently created event, got %q", record.EventID)
	}
}

func TestResolveTargetEventRecord_ExplicitRef_ReturnsMatch(t *testing.T) {
	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "events.json"))
	if err := st.Save(store.EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "t1", CreatedAt: time.Now(), CreatedBy: "u1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	record, err := resolveTargetEventRecord(st, "channel-1", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.EventID != "event-1" {
		t.Fatalf("expected event-1, got %q", record.EventID)
	}
}

func TestResolveTargetEventRecord_NoStoredEvent_ReturnsJapaneseError(t *testing.T) {
	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "events.json"))

	_, err := resolveTargetEventRecord(st, "channel-1", "")
	if err == nil || err.Error() != "❌ このチャンネルではまだ日程調整イベントが作成されていません" {
		t.Fatalf("expected the no-stored-event message, got: %v", err)
	}
}

func TestScheduleResultCommand_BuildResultEmbed_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/events/event-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "event-1", "publicSlug": "abc123", "title": "忘年会", "ownerName": "たろう", "status": "open",
			"candidates": []interface{}{
				map[string]interface{}{"id": "c1", "label": "2026-07-10 19:00 (金)", "startsAt": "2026-07-10T19:00:00Z", "sortOrder": 0},
			},
			"participants": []interface{}{
				map[string]interface{}{"id": "p1", "name": "はなこ", "availability": map[string]interface{}{"c1": "ok"}, "createdAt": "x", "updatedAt": "x"},
			},
			"aggregate": []interface{}{
				map[string]interface{}{"candidateId": "c1", "ok": 1, "maybe": 0, "ng": 0, "score": 1, "rank": 1},
			},
			"createdAt": "2026-07-08T00:00:00Z", "updatedAt": "2026-07-08T00:00:00Z",
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "events.json"))
	if err := st.Save(store.EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "t1", CreatedAt: time.Now(), CreatedBy: "u1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	choseisamaClient := choseisama.NewClient(server.Client())
	choseisamaClient.BaseURL = server.URL
	cmd := &ScheduleResultCommand{choseisamaClient: choseisamaClient, store: st}

	embed, err := cmd.buildResultEmbed(context.Background(), "channel-1", "")
	if err != nil {
		t.Fatalf("buildResultEmbed returned error: %v", err)
	}
	// PublicEventURL is computed from choseisamaClient.BaseURL (which this
	// test overrode to server.URL above), not from the hardcoded production
	// origin — so the expected value must be computed the same way, not
	// hardcoded, or this assertion would fail against the mock server.
	wantURL := choseisamaClient.PublicEventURL("abc123")
	if embed.URL != wantURL {
		t.Fatalf("unexpected embed URL: got %q, want %q", embed.URL, wantURL)
	}
	if len(embed.Fields) != 1 || embed.Fields[0].Name != "2026-07-10 19:00 (金)" {
		t.Fatalf("unexpected fields: %+v", embed.Fields)
	}
	if embed.Fields[0].Value != "✅ 1　🤔 0　❌ 0" {
		t.Fatalf("unexpected field value: %q", embed.Fields[0].Value)
	}
}

func TestScheduleResultCommand_BuildResultEmbed_NoStoredEvent(t *testing.T) {
	dir := t.TempDir()
	cmd := &ScheduleResultCommand{
		choseisamaClient: choseisama.NewClient(http.DefaultClient),
		store:            store.New(filepath.Join(dir, "events.json")),
	}

	_, err := cmd.buildResultEmbed(context.Background(), "channel-1", "")
	if err == nil || err.Error() != "❌ このチャンネルではまだ日程調整イベントが作成されていません" {
		t.Fatalf("expected the no-stored-event message, got: %v", err)
	}
}
