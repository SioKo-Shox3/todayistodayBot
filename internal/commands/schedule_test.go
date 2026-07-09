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

func TestScheduleCommand_Definition(t *testing.T) {
	cmd := &ScheduleCommand{}
	def := cmd.Definition()
	if def.Name != "schedule" {
		t.Fatalf("expected command name 'schedule', got %q", def.Name)
	}
	if len(def.Options) != 4 {
		t.Fatalf("expected 4 options (title, start, days, time), got %d", len(def.Options))
	}
	wantNames := []string{"title", "start", "days", "time"}
	for i, opt := range def.Options {
		if opt.Name != wantNames[i] {
			t.Fatalf("option[%d]: expected name %q, got %q", i, wantNames[i], opt.Name)
		}
		if !opt.Required {
			t.Fatalf("option %q: expected Required=true", opt.Name)
		}
	}
}

func TestParseStartOffset(t *testing.T) {
	cases := []struct {
		arg     string
		want    int
		wantErr bool
	}{
		{"+1", 1, false},
		{"0", 0, false},
		{"-3", -3, false},
		{"abc", 0, true},
		{"", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			got, err := parseStartOffset(tc.arg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tc.arg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.arg, err)
			}
			if got != tc.want {
				t.Fatalf("parseStartOffset(%q) = %d, want %d", tc.arg, got, tc.want)
			}
		})
	}
}

func TestParseTimeArg(t *testing.T) {
	cases := []struct {
		arg        string
		wantHour   int
		wantMinute int
		wantErr    bool
	}{
		{"19:00", 19, 0, false},
		{"09:30", 9, 30, false},
		{"25:00", 0, 0, true},
		{"bad", 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			hour, minute, err := parseTimeArg(tc.arg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tc.arg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.arg, err)
			}
			if hour != tc.wantHour || minute != tc.wantMinute {
				t.Fatalf("parseTimeArg(%q) = (%d,%d), want (%d,%d)", tc.arg, hour, minute, tc.wantHour, tc.wantMinute)
			}
		})
	}
}

func TestValidateScheduleDays(t *testing.T) {
	cases := []struct {
		days    int
		wantErr bool
	}{
		{0, true}, {1, false}, {50, false}, {51, true}, {-1, true},
	}
	for _, tc := range cases {
		if err := validateScheduleDays(tc.days); (err != nil) != tc.wantErr {
			t.Fatalf("validateScheduleDays(%d): error=%v, wantErr=%v", tc.days, err, tc.wantErr)
		}
	}
}

func TestBuildCandidates_LabelsAndStartsAt(t *testing.T) {
	// 2026-07-10 is a Friday.
	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	candidates := buildCandidates(base, 3, 19, 0)

	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}
	wantLabels := []string{
		"2026-07-10 19:00 (金)",
		"2026-07-11 19:00 (土)",
		"2026-07-12 19:00 (日)",
	}
	wantStartsAt := []string{
		"2026-07-10T19:00:00Z",
		"2026-07-11T19:00:00Z",
		"2026-07-12T19:00:00Z",
	}
	for i, c := range candidates {
		if c.Label != wantLabels[i] {
			t.Fatalf("candidate[%d].Label = %q, want %q", i, c.Label, wantLabels[i])
		}
		if c.StartsAt != wantStartsAt[i] {
			t.Fatalf("candidate[%d].StartsAt = %q, want %q", i, c.StartsAt, wantStartsAt[i])
		}
	}
}

func TestScheduleCommand_BuildAndCreateEvent_Success(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/events" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"event": map[string]interface{}{
				"id": "event-1", "publicSlug": "abc123", "title": "忘年会", "ownerName": "たろう", "status": "open",
				"candidates":   []interface{}{},
				"participants": []interface{}{},
				"aggregate":    []interface{}{},
				"createdAt":    "2026-07-08T00:00:00Z", "updatedAt": "2026-07-08T00:00:00Z",
			},
			"secrets": map[string]interface{}{"ownerToken": "owner-tok", "apiToken": "api-tok"},
			"urls": map[string]interface{}{
				"publicPath": "https://chosei-sama.choseisama.workers.dev/events/abc123",
				"ownerPath":  "x", "exportPath": "y",
			},
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	choseisamaClient := choseisama.NewClient(server.Client())
	choseisamaClient.BaseURL = server.URL
	cmd := &ScheduleCommand{
		choseisamaClient: choseisamaClient,
		store:            store.New(filepath.Join(dir, "events.json")),
	}

	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	embed, err := cmd.buildAndCreateEvent(context.Background(), "忘年会", "+2", 3, "19:00", "channel-1", "user-1", "たろう", now)
	if err != nil {
		t.Fatalf("buildAndCreateEvent returned error: %v", err)
	}
	if embed.Title != "📅 忘年会" {
		t.Fatalf("unexpected embed title: %q", embed.Title)
	}
	if embed.URL != "https://chosei-sama.choseisama.workers.dev/events/abc123" {
		t.Fatalf("unexpected embed URL: %q", embed.URL)
	}
	if gotBody["title"] != "忘年会" || gotBody["ownerName"] != "たろう" {
		t.Fatalf("unexpected request body: %v", gotBody)
	}
	candidates, ok := gotBody["candidates"].([]interface{})
	if !ok || len(candidates) != 3 {
		t.Fatalf("expected 3 candidates in request body, got: %v", gotBody["candidates"])
	}

	record, found, err := cmd.store.FindLatestByChannel("channel-1")
	if err != nil {
		t.Fatalf("FindLatestByChannel error: %v", err)
	}
	if !found {
		t.Fatal("expected a stored EventRecord after buildAndCreateEvent")
	}
	if record.EventID != "event-1" || record.OwnerToken != "owner-tok" || record.PublicSlug != "abc123" || record.CreatedBy != "user-1" {
		t.Fatalf("unexpected stored record: %+v", record)
	}
}

func TestScheduleCommand_BuildAndCreateEvent_InvalidDays_ReturnsErrorWithoutCallingAPI(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	dir := t.TempDir()
	choseisamaClient := choseisama.NewClient(server.Client())
	choseisamaClient.BaseURL = server.URL
	cmd := &ScheduleCommand{
		choseisamaClient: choseisamaClient,
		store:            store.New(filepath.Join(dir, "events.json")),
	}

	_, err := cmd.buildAndCreateEvent(context.Background(), "忘年会", "+1", 0, "19:00", "channel-1", "user-1", "たろう", time.Now())
	if err == nil {
		t.Fatal("expected an error for days=0")
	}
	if called {
		t.Fatal("expected validation to fail before calling the chosei-sama API")
	}
}

func TestScheduleCommand_BuildAndCreateEvent_APIFailure_ReturnsJapaneseMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	dir := t.TempDir()
	choseisamaClient := choseisama.NewClient(server.Client())
	choseisamaClient.BaseURL = server.URL
	cmd := &ScheduleCommand{
		choseisamaClient: choseisamaClient,
		store:            store.New(filepath.Join(dir, "events.json")),
	}

	_, err := cmd.buildAndCreateEvent(context.Background(), "忘年会", "+1", 3, "19:00", "channel-1", "user-1", "たろう", time.Now())
	if err == nil || err.Error() != "❌ 日程調整の作成に失敗しました。" {
		t.Fatalf("expected the standard creation-failure message, got: %v", err)
	}
}
