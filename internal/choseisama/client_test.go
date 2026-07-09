package choseisama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateEvent_Success(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"event": map[string]interface{}{
				"id": "event-1", "publicSlug": "abc123", "title": "忘年会",
				"ownerName": "たろう", "status": "open",
				"candidates":   []interface{}{},
				"participants": []interface{}{},
				"aggregate":    []interface{}{},
				"createdAt":    "2026-07-08T00:00:00Z", "updatedAt": "2026-07-08T00:00:00Z",
			},
			"secrets": map[string]interface{}{"ownerToken": "owner-tok", "apiToken": "api-tok"},
			"urls": map[string]interface{}{
				"publicPath": "https://chosei-sama.choseisama.workers.dev/events/abc123",
				"ownerPath":  "https://chosei-sama.choseisama.workers.dev/owner/abc123/owner-tok",
				"exportPath": "https://chosei-sama.choseisama.workers.dev/api/v1/events/abc123/export",
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL

	resp, err := client.CreateEvent(context.Background(), CreateEventRequest{
		Title:     "忘年会",
		OwnerName: "たろう",
		Candidates: []Candidate{
			{Label: "2026-07-10 19:00 (金)", StartsAt: "2026-07-10T19:00:00Z"},
		},
	})
	if err != nil {
		t.Fatalf("CreateEvent returned error: %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/api/v1/events" {
		t.Fatalf("expected POST /api/v1/events, got %s %s", gotMethod, gotPath)
	}
	if gotBody["title"] != "忘年会" || gotBody["ownerName"] != "たろう" {
		t.Fatalf("unexpected request body: %v", gotBody)
	}

	if resp.Event.ID != "event-1" || resp.Event.PublicSlug != "abc123" {
		t.Fatalf("unexpected event in response: %+v", resp.Event)
	}
	if resp.Secrets.OwnerToken != "owner-tok" {
		t.Fatalf("expected ownerToken 'owner-tok', got %q", resp.Secrets.OwnerToken)
	}
	if resp.URLs.PublicPath != "https://chosei-sama.choseisama.workers.dev/events/abc123" {
		t.Fatalf("unexpected publicPath: %q", resp.URLs.PublicPath)
	}
}

func TestCreateEvent_ValidationError_ReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "validation_error"})
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL

	_, err := client.CreateEvent(context.Background(), CreateEventRequest{})
	if err == nil {
		t.Fatal("expected an error for a 400 response")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusBadRequest || apiErr.Code != "validation_error" {
		t.Fatalf("unexpected APIError: %+v", apiErr)
	}
}

func TestGetEvent_Success(t *testing.T) {
	var gotMethod, gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "event-1", "publicSlug": "abc123", "title": "忘年会",
			"ownerName": "たろう", "status": "open",
			"candidates": []interface{}{
				map[string]interface{}{"id": "c1", "label": "2026-07-10 19:00 (金)", "startsAt": "2026-07-10T19:00:00Z", "sortOrder": 0},
			},
			"participants": []interface{}{
				map[string]interface{}{
					"id": "p1", "name": "はなこ", "comment": "",
					"availability": map[string]interface{}{"c1": "ok"},
					"createdAt":    "2026-07-08T00:00:00Z", "updatedAt": "2026-07-08T00:00:00Z",
				},
			},
			"aggregate": []interface{}{
				map[string]interface{}{"candidateId": "c1", "ok": 1, "maybe": 0, "ng": 0, "score": 1, "rank": 1},
			},
			"createdAt": "2026-07-08T00:00:00Z", "updatedAt": "2026-07-08T00:00:00Z",
		})
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL

	snapshot, err := client.GetEvent(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("GetEvent returned error: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/v1/events/abc123" {
		t.Fatalf("expected GET /api/v1/events/abc123, got %s %s", gotMethod, gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("expected no Authorization header on public GetEvent, got %q", gotAuth)
	}
	if len(snapshot.Candidates) != 1 || snapshot.Candidates[0].Label != "2026-07-10 19:00 (金)" {
		t.Fatalf("unexpected candidates: %+v", snapshot.Candidates)
	}
	if len(snapshot.Aggregate) != 1 || snapshot.Aggregate[0].OK != 1 {
		t.Fatalf("unexpected aggregate: %+v", snapshot.Aggregate)
	}
}

func TestGetEvent_NotFound_ReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "not_found"})
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL

	_, err := client.GetEvent(context.Background(), "missing")
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound || apiErr.Code != "not_found" {
		t.Fatalf("unexpected APIError: %+v", apiErr)
	}
}

func TestSetDiscordReminder_Success(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"event": map[string]interface{}{
				"id": "event-1", "publicSlug": "abc123", "title": "忘年会", "ownerName": "たろう", "status": "open",
				"candidates": []interface{}{}, "participants": []interface{}{}, "aggregate": []interface{}{},
				"createdAt": "2026-07-08T00:00:00Z", "updatedAt": "2026-07-08T00:00:00Z",
			},
			"discordReminder": map[string]interface{}{
				"id": "reminder-1", "eventId": "event-1",
				"urlPreview":   "https://discord.com/api/webhooks/1/abcd...wxyz",
				"allOkEnabled": true, "allOkHoursBefore": 3,
				"deadlineEnabled": false, "deadlineHoursBefore": 24,
				"mentionEveryone": false,
				"createdAt":       "2026-07-08T00:00:00Z", "updatedAt": "2026-07-08T00:00:00Z",
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL

	setting, err := client.SetDiscordReminder(context.Background(), "abc123", "owner-tok", SetDiscordReminderRequest{
		WebhookURL:          "https://discord.com/api/webhooks/1/abcd",
		AllOkEnabled:        true,
		AllOkHoursBefore:    3,
		DeadlineEnabled:     false,
		DeadlineHoursBefore: 24,
		MentionEveryone:     false,
	})
	if err != nil {
		t.Fatalf("SetDiscordReminder returned error: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v1/events/abc123/discord-reminder" {
		t.Fatalf("expected PUT /api/v1/events/abc123/discord-reminder, got %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer owner-tok" {
		t.Fatalf("expected Bearer owner-tok, got %q", gotAuth)
	}
	if gotBody["allOkEnabled"] != true || gotBody["deadlineEnabled"] != false {
		t.Fatalf("expected explicit allOkEnabled/deadlineEnabled in body, got: %v", gotBody)
	}
	if setting.AllOkHoursBefore != 3 || setting.URLPreview == "" {
		t.Fatalf("unexpected setting: %+v", setting)
	}
}

func TestSetDiscordReminder_Unauthorized_ReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "unauthorized"})
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL

	_, err := client.SetDiscordReminder(context.Background(), "abc123", "bad-token", SetDiscordReminderRequest{})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized || apiErr.Code != "unauthorized" {
		t.Fatalf("unexpected APIError: %+v", apiErr)
	}
}

func TestSetDiscordReminder_WebhookURLRequired_ReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "discord_webhook_url_required"})
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL

	_, err := client.SetDiscordReminder(context.Background(), "abc123", "owner-tok", SetDiscordReminderRequest{})
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "discord_webhook_url_required" {
		t.Fatalf("expected APIError with Code discord_webhook_url_required, got %v", err)
	}
}

func TestGetOwnerView_Success_NoReminderConfigured(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"event": map[string]interface{}{
				"id": "event-1", "publicSlug": "abc123", "title": "忘年会", "ownerName": "たろう", "status": "open",
				"candidates": []interface{}{}, "participants": []interface{}{}, "aggregate": []interface{}{},
				"createdAt": "2026-07-08T00:00:00Z", "updatedAt": "2026-07-08T00:00:00Z",
			},
			"webhooks":        []interface{}{},
			"discordReminder": nil,
			"urls":            map[string]interface{}{"publicPath": "x", "ownerPath": "y", "exportPath": "z"},
		})
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL

	view, err := client.GetOwnerView(context.Background(), "abc123", "owner-tok")
	if err != nil {
		t.Fatalf("GetOwnerView returned error: %v", err)
	}
	if gotAuth != "Bearer owner-tok" {
		t.Fatalf("expected Bearer owner-tok, got %q", gotAuth)
	}
	if view.DiscordReminder != nil {
		t.Fatalf("expected nil DiscordReminder when not configured, got %+v", view.DiscordReminder)
	}
	if view.Event.Title != "忘年会" {
		t.Fatalf("unexpected event title: %q", view.Event.Title)
	}
}

func TestGetOwnerView_Unauthorized_ReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "unauthorized"})
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.BaseURL = server.URL

	_, err := client.GetOwnerView(context.Background(), "abc123", "bad-token")
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 *APIError, got %v", err)
	}
}

func TestPublicEventURL(t *testing.T) {
	client := NewClient(http.DefaultClient)
	got := client.PublicEventURL("abc123")
	want := "https://chosei-sama.choseisama.workers.dev/events/abc123"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNewClient_DefaultsToProductionBaseURL(t *testing.T) {
	t.Setenv("CHOSEI_SAMA_BASE_URL", "") // Setenv restores the original value/absence via t.Cleanup, so this is safe regardless of host env state; an empty string is indistinguishable from "unset" to os.Getenv.
	client := NewClient(http.DefaultClient)
	if client.BaseURL != defaultBaseURL {
		t.Fatalf("expected default base URL %q, got %q", defaultBaseURL, client.BaseURL)
	}
}

func TestNewClient_ChoseiSamaBaseURLEnvOverride(t *testing.T) {
	t.Setenv("CHOSEI_SAMA_BASE_URL", "http://localhost:8787")
	client := NewClient(http.DefaultClient)
	if client.BaseURL != "http://localhost:8787" {
		t.Fatalf("expected env override %q, got %q", "http://localhost:8787", client.BaseURL)
	}
}
