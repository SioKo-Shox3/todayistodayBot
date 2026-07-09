package choseisama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// defaultBaseURL is chosei-sama's production origin (Cloudflare Workers
// deployment) — see C:\Users\<user>\Documents\chosei-sama\wrangler.jsonc's
// vars.PUBLIC_APP_URL, which is the same origin the API is served from.
const defaultBaseURL = "https://chosei-sama.choseisama.workers.dev"

// Client is a thin HTTP client for chosei-sama's public API. It has no
// dependency on discordgo or any other internal package (see
// Docs/agent-guide/architecture.md's dependency-direction rule) — it only
// knows chosei-sama's HTTP request/response shapes.
type Client struct {
	httpClient *http.Client
	// BaseURL is exported (unlike internal/weather.Client's private
	// baseURL) because *choseisama.Client is constructed and tested from
	// internal/commands (a different package); commands' own tests need
	// to point this at an httptest.Server, which an unexported field in
	// another package cannot do.
	BaseURL string
}

// NewClient returns a Client pointed at chosei-sama's production API.
func NewClient(httpClient *http.Client) *Client {
	return &Client{httpClient: httpClient, BaseURL: defaultBaseURL}
}

// APIError represents a non-2xx response from chosei-sama.
type APIError struct {
	StatusCode int
	// Code is chosei-sama's {"error": "..."} value (e.g. "not_found",
	// "unauthorized", "discord_webhook_url_required", "validation_error"),
	// or "" if the body wasn't that JSON shape.
	Code string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("choseisama: HTTP %d (%s)", e.StatusCode, e.Code)
	}
	return fmt.Sprintf("choseisama: HTTP %d", e.StatusCode)
}

// Candidate is one schedule candidate in a CreateEventRequest — mirrors
// chosei-sama's candidateInputSchema (chosei-sama/src/shared/schema.ts:16-20).
// ID is left empty for bot-created candidates; chosei-sama assigns one.
type Candidate struct {
	ID       string `json:"id,omitempty"`
	Label    string `json:"label"`
	StartsAt string `json:"startsAt,omitempty"`
}

// CreateEventRequest mirrors chosei-sama's createEventSchema
// (chosei-sama/src/shared/schema.ts:22-28).
type CreateEventRequest struct {
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	OwnerName   string      `json:"ownerName"`
	DeadlineAt  string      `json:"deadlineAt,omitempty"`
	Candidates  []Candidate `json:"candidates"`
}

// CandidateSnapshot mirrors chosei-sama's Candidate type
// (chosei-sama/src/shared/types.ts:10-15) as returned in an EventSnapshot.
type CandidateSnapshot struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	StartsAt  string `json:"startsAt,omitempty"`
	SortOrder int    `json:"sortOrder"`
}

// Participant mirrors chosei-sama's ParticipantWithAvailability type
// (chosei-sama/src/shared/types.ts:26-33).
type Participant struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Comment      string            `json:"comment,omitempty"`
	Availability map[string]string `json:"availability"`
	CreatedAt    string            `json:"createdAt"`
	UpdatedAt    string            `json:"updatedAt"`
}

// AvailabilitySummary mirrors chosei-sama's AvailabilitySummary type
// (chosei-sama/src/shared/types.ts:17-24).
type AvailabilitySummary struct {
	CandidateID string `json:"candidateId"`
	OK          int    `json:"ok"`
	Maybe       int    `json:"maybe"`
	NG          int    `json:"ng"`
	Score       int    `json:"score"`
	Rank        int    `json:"rank"`
}

// EventSnapshot mirrors chosei-sama's EventSnapshot type
// (chosei-sama/src/shared/types.ts:35-48).
type EventSnapshot struct {
	ID           string                `json:"id"`
	PublicSlug   string                `json:"publicSlug"`
	Title        string                `json:"title"`
	Description  string                `json:"description,omitempty"`
	OwnerName    string                `json:"ownerName"`
	Status       string                `json:"status"`
	DeadlineAt   string                `json:"deadlineAt,omitempty"`
	Candidates   []CandidateSnapshot   `json:"candidates"`
	Participants []Participant         `json:"participants"`
	Aggregate    []AvailabilitySummary `json:"aggregate"`
	CreatedAt    string                `json:"createdAt"`
	UpdatedAt    string                `json:"updatedAt"`
}

// EventURLs mirrors chosei-sama's CreateEventResult.urls
// (chosei-sama/src/shared/types.ts:90-94) — already-absolute URLs.
type EventURLs struct {
	PublicPath string `json:"publicPath"`
	OwnerPath  string `json:"ownerPath"`
	ExportPath string `json:"exportPath"`
}

// CreateEventResponse mirrors chosei-sama's CreateEventResult type
// (chosei-sama/src/shared/types.ts:84-95) — the response body of
// POST /api/v1/events (201).
type CreateEventResponse struct {
	Event   EventSnapshot `json:"event"`
	Secrets struct {
		OwnerToken string `json:"ownerToken"`
		APIToken   string `json:"apiToken"`
	} `json:"secrets"`
	URLs EventURLs `json:"urls"`
}

// CreateEvent calls POST /api/v1/events.
func (c *Client) CreateEvent(ctx context.Context, req CreateEventRequest) (*CreateEventResponse, error) {
	var out CreateEventResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/events", "", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// doJSON performs an HTTP request against BaseURL+path, optionally with a
// Bearer token and/or a JSON-encoded reqBody, and decodes a JSON response
// into out (skipped if out is nil). A non-2xx response is returned as
// *APIError instead of decoding into out.
func (c *Client) doJSON(ctx context.Context, method, path, bearerToken string, reqBody, out interface{}) error {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("choseisama: encoding request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("choseisama: building request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("choseisama: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body struct {
			Error string `json:"error"`
		}
		data, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(data, &body) // best-effort; body may not be JSON
		return &APIError{StatusCode: resp.StatusCode, Code: body.Error}
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("choseisama: decoding response body: %w", err)
	}
	return nil
}

// DiscordReminderSetting mirrors chosei-sama's DiscordReminderSetting type
// (chosei-sama/src/shared/types.ts:71-82).
type DiscordReminderSetting struct {
	ID                  string `json:"id"`
	EventID             string `json:"eventId"`
	URLPreview          string `json:"urlPreview"`
	AllOkEnabled        bool   `json:"allOkEnabled"`
	AllOkHoursBefore    int    `json:"allOkHoursBefore"`
	DeadlineEnabled     bool   `json:"deadlineEnabled"`
	DeadlineHoursBefore int    `json:"deadlineHoursBefore"`
	MentionEveryone     bool   `json:"mentionEveryone"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
}

// SetDiscordReminderRequest mirrors chosei-sama's discordReminderInputSchema
// (chosei-sama/src/shared/schema.ts:66-73). Unlike Candidate/CreateEventRequest,
// the bool/int fields deliberately have NO `omitempty`: chosei-sama's zod
// `.default(...)` only applies when a key is entirely absent from the JSON
// body, and every caller of SetDiscordReminder (ReminderCommand's set/off
// subcommands) always wants to send explicit, deterministic values.
type SetDiscordReminderRequest struct {
	WebhookURL          string `json:"webhookUrl,omitempty"`
	AllOkEnabled        bool   `json:"allOkEnabled"`
	AllOkHoursBefore    int    `json:"allOkHoursBefore"`
	DeadlineEnabled     bool   `json:"deadlineEnabled"`
	DeadlineHoursBefore int    `json:"deadlineHoursBefore"`
	MentionEveryone     bool   `json:"mentionEveryone"`
}

// SetDiscordReminder calls PUT /api/v1/events/:ref/discord-reminder with
// Bearer ownerToken auth.
func (c *Client) SetDiscordReminder(ctx context.Context, ref, ownerToken string, req SetDiscordReminderRequest) (*DiscordReminderSetting, error) {
	var out struct {
		Event           EventSnapshot          `json:"event"`
		DiscordReminder DiscordReminderSetting `json:"discordReminder"`
	}
	path := "/api/v1/events/" + url.PathEscape(ref) + "/discord-reminder"
	if err := c.doJSON(ctx, http.MethodPut, path, ownerToken, req, &out); err != nil {
		return nil, err
	}
	return &out.DiscordReminder, nil
}

// GetEvent calls GET /api/v1/events/:ref — chosei-sama's public read
// endpoint (no auth). ref may be either the event's id or its publicSlug;
// chosei-sama resolves both (chosei-sama/src/worker/repository.ts's
// findEvent: `WHERE id = ? OR public_slug = ?`).
func (c *Client) GetEvent(ctx context.Context, ref string) (*EventSnapshot, error) {
	var out EventSnapshot
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/events/"+url.PathEscape(ref), "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// OwnerView mirrors the response body of GET /api/v1/events/:ref/owner
// (chosei-sama/src/worker/app.ts:87-93) — DiscordReminder is nil when no
// reminder has ever been configured (chosei-sama returns
// discordReminder: null in that case).
type OwnerView struct {
	Event           EventSnapshot           `json:"event"`
	DiscordReminder *DiscordReminderSetting `json:"discordReminder"`
}

// GetOwnerView calls GET /api/v1/events/:ref/owner with Bearer ownerToken
// auth.
func (c *Client) GetOwnerView(ctx context.Context, ref, ownerToken string) (*OwnerView, error) {
	var out OwnerView
	path := "/api/v1/events/" + url.PathEscape(ref) + "/owner"
	if err := c.doJSON(ctx, http.MethodGet, path, ownerToken, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PublicEventURL builds the public event page URL for publicSlug.
// GetEvent's response (unlike CreateEvent's/GetOwnerView's) has no "urls"
// field, so callers that only have a publicSlug from the local store
// (ScheduleResultCommand — see internal/commands/schedule_result.go)
// reconstruct it from the same origin the API itself is on.
func (c *Client) PublicEventURL(publicSlug string) string {
	return c.BaseURL + "/events/" + publicSlug
}
