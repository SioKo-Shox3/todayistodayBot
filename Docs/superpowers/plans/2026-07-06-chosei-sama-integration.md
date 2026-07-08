# Chosei-sama Integration (Phase B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three new native Discord slash commands (`/schedule`, `/schedule-result`, `/reminder` with `set`/`off`/`status` subcommands) that delegate all schedule-coordination and reminder logic to the external chosei-sama service (`https://chosei-sama.choseisama.workers.dev`), giving the bot its first local persistence layer (`data/chosei-events.json`, mutex-guarded single writer) to remember which chosei-sama event belongs to which Discord channel.
**Architecture:** A new dependency-free HTTP client package `internal/choseisama` wraps chosei-sama's public API (`POST /api/v1/events`, `GET /api/v1/events/:ref`, `PUT /api/v1/events/:ref/discord-reminder`, `GET /api/v1/events/:ref/owner`). A new `internal/store` package persists a small `EventRecord` (channel → chosei-sama event id/publicSlug/ownerToken) to `data/chosei-events.json` through a single process-wide `*store.Store` singleton (`store.Default()`) so every command shares one `sync.Mutex`. Three new `internal/commands/*.go` files (self-registering via `init()`, per the existing Phase A pattern) glue Discord interactions to these two packages; `cmd/bot/main.go` needs no changes because it already dispatches via `commands.All()`.
**Tech Stack:** Go 1.26.4 (per `go.mod`), `github.com/bwmarrin/discordgo` v0.29.0, standard library `net/http`/`encoding/json`/`sync`/`os`/`log/slog`, `net/http/httptest` for client/command tests, `t.TempDir()` for store tests.

---

## Investigation notes the implementer must know before starting

These are non-obvious findings from reading the actual chosei-sama source and the actual `discordgo@v0.29.0` module cache — getting any of them wrong will produce code that compiles but silently fails against the real API or the real Discord REST endpoint.

1. **chosei-sama's exact contract** (read directly from `C:\Users\<user>\Documents\chosei-sama\src\shared\schema.ts`, `src\shared\types.ts`, `src\worker\app.ts`, `src\worker\repository.ts`, `wrangler.jsonc`):
   - Production base URL: `https://chosei-sama.choseisama.workers.dev` (`wrangler.jsonc:16`, `vars.PUBLIC_APP_URL`).
   - `POST /api/v1/events` — body `{title, description?, ownerName, deadlineAt?, candidates:[{id?, label, startsAt?}]}` (`schema.ts:22-28`), 201 response `{event: EventSnapshot, secrets:{ownerToken, apiToken}, urls:{publicPath, ownerPath, exportPath}}` (`app.ts:65-72`, `types.ts:84-95`). `candidates` max length 50 (`LIMITS.candidatesPerEvent`, `schema.ts:5`) — matches the design spec's `days` 1-50 range.
   - `GET /api/v1/events/:ref` — **no auth**, `ref` matches either the event's `id` OR its `publicSlug` (`repository.ts:851-856`, `WHERE id = ? OR public_slug = ?`). 200 body is the bare `EventSnapshot` (not wrapped); 404 body `{"error":"not_found"}` (`app.ts:75-79`).
   - `PUT /api/v1/events/:ref/discord-reminder` — requires `Authorization: Bearer <ownerToken>`. Body `discordReminderInputSchema` (`schema.ts:66-73`): `webhookUrl?` (optional — omitting it keeps the existing encrypted URL server-side), `allOkEnabled` (default `true`), `allOkHoursBefore` (default `3`, int 1-168), `deadlineEnabled` (default `false`), `deadlineHoursBefore` (default `24`, int 1-168), `mentionEveryone` (default `false`). **These zod `.default()`s only apply when the JSON key is entirely absent** — since our bot always wants deterministic values, the request struct must send `allOkEnabled`/`allOkHoursBefore`/`deadlineEnabled`/`deadlineHoursBefore`/`mentionEveryone` **without `omitempty`**. 200 response `{event, discordReminder}`; 400 `{"error":"discord_webhook_url_required"}` if no webhook URL was ever registered and none is supplied now (`app.ts:186`, `repository.ts:406`); 401 `{"error":"unauthorized"}`.
   - `GET /api/v1/events/:ref/owner` — `Authorization: Bearer <ownerToken>`. 200 body `{event, webhooks, discordReminder, urls}` where `discordReminder` is `null` if never configured (`app.ts:87-93`); 401 `{"error":"unauthorized"}`.
   - `EventSnapshot` (`types.ts:35-48`): `id, publicSlug, title, description?, ownerName, status('open'|'closed'), deadlineAt?, candidates:Candidate[], participants:ParticipantWithAvailability[], aggregate:AvailabilitySummary[], createdAt, updatedAt`. `Candidate` (`types.ts:10-15`): `id, label, startsAt?, sortOrder`. `AvailabilitySummary` (`types.ts:17-24`): `candidateId, ok, maybe, ng, score, rank`. `ParticipantWithAvailability` (`types.ts:26-33`): `id, name, comment?, availability:Record<string,'ok'|'maybe'|'ng'>, createdAt, updatedAt`. `DiscordReminderSetting` (`types.ts:71-82`): `id, eventId, urlPreview, allOkEnabled, allOkHoursBefore, deadlineEnabled, deadlineHoursBefore, mentionEveryone, createdAt, updatedAt`.
   - `GetEvent`'s response has **no `urls` field** — only `POST /api/v1/events` and `GET .../owner` return `urls`. `/schedule-result` (Task 10) must reconstruct the public URL itself from the locally-stored `publicSlug` and the configured base URL.
   - zod's bare `.datetime()` (no `{offset:true}`) requires a **UTC-only** ISO 8601 string ending in `Z` (`schema.ts:19` `startsAt: z.string().datetime().nullish()`). Go's `t.UTC().Format(time.RFC3339)` produces exactly this (`time.RFC3339`'s `Z07:00` verb renders literal `Z` for a zero UTC offset).

2. **discordgo v0.29.0's actual API** (read directly from `C:\Users\<user>\go\pkg\mod\github.com\bwmarrin\discordgo@v0.29.0\{restapi.go,webhook.go,interactions.go}`):
   - The method is **`(s *Session) WebhookCreate(channelID, name, avatar string, options ...RequestOption) (*Webhook, error)`** (`restapi.go:2271`) — **not** `ChannelWebhookCreate` (that name does not exist in this module version).
   - `Webhook` struct (`webhook.go:4-16`) has fields `ID, Type, GuildID, ChannelID, User, Name, Avatar, Token, ApplicationID` — **no `.URL` field**. The webhook URL must be assembled manually as `"https://discord.com/api/webhooks/" + webhook.ID + "/" + webhook.Token"`, which also happens to be exactly the format chosei-sama's own `discordWebhookUrlSchema` requires (`chosei-sama/src/shared/schema.ts:56-64`: host `discord.com`/`discordapp.com`, path prefix `/api/webhooks/`).
   - **`*discordgo.Session`'s REST calls cannot be redirected to an `httptest.Server`.** `endpoints.go:20-26` defines `EndpointDiscord` etc. as a `var` block, but `EndpointAPI`, `EndpointChannelWebhooks`, and everything built from them are computed **once at package-init time** by string concatenation — reassigning `discordgo.EndpointDiscord` in a test does not retroactively change those already-frozen strings. Consequence: `ReminderCommand`'s `/reminder set` handler (Task 11) must take a small interface (`discordWebhookCreator`) instead of depending on `*discordgo.WebhookCreate` directly, so tests can inject a fake. `*discordgo.Session` still satisfies this interface structurally in production — no adapter needed.
   - Subcommands: a slash command with subcommands has one `discordgo.ApplicationCommandOption` per subcommand at the top level with `Type: discordgo.ApplicationCommandOptionSubCommand` (`interactions.go:64`) and its own `Options` (the subcommand's arguments) nested inside. At interaction time, `i.ApplicationCommandData().Options[0]` **is** the chosen subcommand option (`.Name` is `"set"`/`"off"`/`"status"`), and `.Options[0].Options` is the flat list of that subcommand's arguments (`interactions.go:340-363`, `436-445`).
   - `ApplicationCommandInteractionDataOption.Value` is `interface{}` (`interactions.go:440`) and `IntValue()`/`BoolValue()`/`StringValue()` do raw, panicking type assertions (`o.Value.(float64)` / `.(bool)` / `.(string)`, `interactions.go:459-489`). **Any test that hand-builds this struct for an Integer option must use `Value: float64(N)`, never `int(N)`** — this mirrors how the real Gateway JSON always decodes numbers as `float64`.
   - `Member.DisplayName()` (`structs.go:1642-1647`) returns `Nick` if set else `User.DisplayName()`; `User.DisplayName()` (`user.go:157-162`) returns `GlobalName` if set else `Username`. A slash-command `Interaction` populates exactly one of `.Member` (guild) or `.User` (DM) (`interactions.go:236-245`).
   - `MessageEmbed`/`MessageEmbedField`/`MessageEmbedFooter` field names: `Title, Description, URL, Color, Footer, Fields` / `Name, Value, Inline` / `Text` (`message.go:378-443`).

3. **Deliberate deviations from the `internal/weather` client pattern** (disclosed here for the plan reviewer, not hidden):
   - `internal/weather.Client.baseURL` is unexported because `weather_test.go` lives in `package weather` (same-package override). `internal/choseisama.Client` is constructed by **three different command files in a different package** (`internal/commands`), and those commands' own tests need to point it at an `httptest.Server`. So `choseisama.Client`'s base-URL field is **exported** as `BaseURL` (not the weather-style private `baseURL`). This is the one place this plan intentionally departs from the literal weather.go pattern; everything else (constructor shape, `NewClient(*http.Client)`, no DI container) is copied as-is.
   - `internal/weather.Client.GetWeather` returns already-Japanese-formatted strings for every expected failure mode (never a Go `error` for those paths), because it has exactly one caller. `internal/choseisama.Client` has **three** callers (`schedule.go`, `schedule_result.go`, `reminder.go`) that each need *different* Japanese wording for the same HTTP status. So `internal/choseisama` returns a structured `*APIError{StatusCode, Code}` Go error, and each command's own pure helper function does the Japanese-message translation — matching `internal/commands/today.go`'s existing `dateParseError` + `todayDateErrorMessage` precedent more than `weather.go`'s single-caller shortcut.

---

## File structure

**Created:**
```
internal/choseisama/client.go
internal/choseisama/client_test.go
internal/store/events.go
internal/store/events_test.go
internal/commands/schedule.go
internal/commands/schedule_test.go
internal/commands/schedule_result.go
internal/commands/schedule_result_test.go
internal/commands/reminder.go
internal/commands/reminder_test.go
```

**Modified:**
```
.gitignore
README.md
Docs/agent-guide/architecture.md
Docs/agent-guide/build-and-verify.md
```

**Not modified:** `internal/config/config.go` and `internal/config/config_test.go` stay exactly as Phase A left them — the base-URL override lives entirely inside `internal/choseisama` (Task 7), not in `internal/config`. See the "Correction record" note at the start of Task 7 for why.

**Not modified (explicitly verified, not just assumed — see Task 14):**
```
cmd/bot/main.go
cmd/bot/main_test.go
internal/commands/help.go     (formatHelpText already iterates commands.All() dynamically — internal/commands/help.go:42-46 — new commands appear in /help automatically)
internal/commands/registry.go
```

---

## Tasks

### Task 1 — `internal/choseisama`: package skeleton + `CreateEvent`

- [ ] Write the failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\choseisama\client_test.go`:
  ```go
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
  ```
- [ ] Run `cd C:\Users\<user>\Documents\todayistodayBot && go test ./internal/choseisama/...` — expect a build failure (package `choseisama` and its types don't exist yet). This confirms the test is actually exercising not-yet-written code.
- [ ] Implement minimally. Create `C:\Users\<user>\Documents\todayistodayBot\internal\choseisama\client.go`:
  ```go
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

  var _ = url.PathEscape // used by GetEvent/SetDiscordReminder/GetOwnerView added in Tasks 2-4
  ```
  (The `var _ = url.PathEscape` line is a deliberate temporary placeholder to keep the `net/url` import used until Task 2 adds `GetEvent` — remove it in Task 2 when `url.PathEscape` gets a real call site. If `go vet` flags an unused-import instead, remove the `"net/url"` import here and re-add it in Task 2.)
- [ ] Run `go test ./internal/choseisama/...` — expect both tests to pass (`ok`).
- [ ] Run `go vet ./internal/choseisama/...` — expect no output.
- [ ] Commit boundary: not independently meaningful alone (no caller yet) but is a valid, tested unit — may be committed standalone or bundled with Task 2; see "Commit boundaries" below.

### Task 2 — `internal/choseisama`: `GetEvent`

- [ ] Add to `client_test.go`:
  ```go
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
  					"createdAt": "2026-07-08T00:00:00Z", "updatedAt": "2026-07-08T00:00:00Z",
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
  ```
- [ ] Run `go test ./internal/choseisama/...` — expect a compile failure (`GetEvent` undefined). Confirms the tests are red.
- [ ] In `client.go`: remove the temporary `var _ = url.PathEscape` line from Task 1 and add:
  ```go
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
  ```
- [ ] Run `go test ./internal/choseisama/... && go vet ./internal/choseisama/...` — expect `ok` and no vet output.
- [ ] Commit boundary: independently committable (adds one tested method to an existing, already-tested client).

### Task 3 — `internal/choseisama`: `SetDiscordReminder`

- [ ] Add to `client_test.go`:
  ```go
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
  				"urlPreview": "https://discord.com/api/webhooks/1/abcd...wxyz",
  				"allOkEnabled": true, "allOkHoursBefore": 3,
  				"deadlineEnabled": false, "deadlineHoursBefore": 24,
  				"mentionEveryone": false,
  				"createdAt": "2026-07-08T00:00:00Z", "updatedAt": "2026-07-08T00:00:00Z",
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
  ```
- [ ] Run `go test ./internal/choseisama/...` — expect compile failure (`SetDiscordReminder`/`SetDiscordReminderRequest`/`DiscordReminderSetting` undefined).
- [ ] Add to `client.go`:
  ```go
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
  		Event           EventSnapshot           `json:"event"`
  		DiscordReminder DiscordReminderSetting `json:"discordReminder"`
  	}
  	path := "/api/v1/events/" + url.PathEscape(ref) + "/discord-reminder"
  	if err := c.doJSON(ctx, http.MethodPut, path, ownerToken, req, &out); err != nil {
  		return nil, err
  	}
  	return &out.DiscordReminder, nil
  }
  ```
- [ ] Run `go test ./internal/choseisama/... && go vet ./internal/choseisama/...` — expect `ok`, no vet output.
- [ ] Commit boundary: independently committable.

### Task 4 — `internal/choseisama`: `GetOwnerView` + `PublicEventURL`

- [ ] Add to `client_test.go`:
  ```go
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
  ```
- [ ] Run `go test ./internal/choseisama/...` — expect compile failure (`GetOwnerView`/`OwnerView`/`PublicEventURL` undefined).
- [ ] Add to `client.go`:
  ```go
  // OwnerView mirrors the response body of GET /api/v1/events/:ref/owner
  // (chosei-sama/src/worker/app.ts:87-93) — DiscordReminder is nil when no
  // reminder has ever been configured (chosei-sama returns
  // discordReminder: null in that case).
  type OwnerView struct {
  	Event           EventSnapshot            `json:"event"`
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
  ```
- [ ] Run `go test ./internal/choseisama/... && go vet ./internal/choseisama/...` — expect `ok`, no vet output. This completes `internal/choseisama` — all 8 test functions across the 4 tasks should pass.
- [ ] Commit boundary: independently committable. Natural point to commit Tasks 1-4 together as "chosei-sama APIクライアントを追加" if not committed incrementally, since the package is now feature-complete and has no caller yet.

### Task 5 — `internal/store`: `EventRecord` + `Save` + `New`/`Default`

- [ ] Write the failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\store\events_test.go`:
  ```go
  package store

  import (
  	"encoding/json"
  	"os"
  	"path/filepath"
  	"testing"
  	"time"
  )

  func TestStore_Save_PersistsAndIsReadableByFreshInstance(t *testing.T) {
  	dir := t.TempDir()
  	path := filepath.Join(dir, "chosei-events.json")

  	record := EventRecord{
  		ChannelID:  "channel-1",
  		EventID:    "event-1",
  		PublicSlug: "abc123",
  		OwnerToken: "owner-tok",
  		CreatedAt:  time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC),
  		CreatedBy:  "user-1",
  	}

  	if err := New(path).Save(record); err != nil {
  		t.Fatalf("Save returned error: %v", err)
  	}

  	data, err := os.ReadFile(path)
  	if err != nil {
  		t.Fatalf("expected file to exist after Save: %v", err)
  	}
  	var records []EventRecord
  	if err := json.Unmarshal(data, &records); err != nil {
  		t.Fatalf("file is not valid JSON: %v", err)
  	}
  	if len(records) != 1 || records[0].EventID != "event-1" || records[0].OwnerToken != "owner-tok" {
  		t.Fatalf("unexpected persisted records: %+v", records)
  	}

  	fresh, found, err := New(path).FindLatestByChannel("channel-1")
  	if err != nil {
  		t.Fatalf("FindLatestByChannel error: %v", err)
  	}
  	if !found || fresh.EventID != "event-1" {
  		t.Fatalf("expected a fresh Store instance to read back the saved record, got found=%v record=%+v", found, fresh)
  	}
  }

  func TestStore_Save_ReplacesExistingRecordWithSameEventID(t *testing.T) {
  	dir := t.TempDir()
  	st := New(filepath.Join(dir, "chosei-events.json"))

  	first := EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "old-tok", CreatedAt: time.Now(), CreatedBy: "user-1"}
  	if err := st.Save(first); err != nil {
  		t.Fatalf("first Save: %v", err)
  	}
  	second := first
  	second.OwnerToken = "new-tok"
  	if err := st.Save(second); err != nil {
  		t.Fatalf("second Save: %v", err)
  	}

  	record, found, err := st.FindLatestByChannel("channel-1")
  	if err != nil || !found {
  		t.Fatalf("FindLatestByChannel: found=%v err=%v", found, err)
  	}
  	if record.OwnerToken != "new-tok" {
  		t.Fatalf("expected replaced record with new-tok, got %q", record.OwnerToken)
  	}

  	data, _ := os.ReadFile(filepath.Join(dir, "chosei-events.json"))
  	var records []EventRecord
  	_ = json.Unmarshal(data, &records)
  	if len(records) != 1 {
  		t.Fatalf("expected exactly 1 record after re-saving the same EventID, got %d", len(records))
  	}
  }

  func TestStore_FindLatestByChannel_MissingFile_ReturnsNotFoundNotError(t *testing.T) {
  	dir := t.TempDir()
  	st := New(filepath.Join(dir, "does-not-exist.json"))

  	_, found, err := st.FindLatestByChannel("channel-1")
  	if err != nil {
  		t.Fatalf("expected no error for a missing file, got: %v", err)
  	}
  	if found {
  		t.Fatal("expected found=false for a missing file")
  	}
  }

  func TestDefault_ReturnsSameInstanceAcrossCalls(t *testing.T) {
  	a := Default()
  	b := Default()
  	if a != b {
  		t.Fatal("expected Default() to return the same *Store pointer on every call, so every caller shares one sync.Mutex")
  	}
  }
  ```
- [ ] Run `cd C:\Users\<user>\Documents\todayistodayBot && go test ./internal/store/...` — expect a build failure (package doesn't exist).
- [ ] Implement. Create `C:\Users\<user>\Documents\todayistodayBot\internal\store\events.go`:
  ```go
  package store

  import (
  	"encoding/json"
  	"fmt"
  	"os"
  	"path/filepath"
  	"sync"
  	"time"
  )

  // EventRecord is the local bookkeeping record the bot keeps for a
  // chosei-sama event it created via /schedule: enough to resolve "the
  // latest event in this channel" (or an explicit ref) and to
  // re-authenticate as the event's owner for /schedule-result and
  // /reminder — chosei-sama deliberately has no list-events API, so this
  // is the bot's own index.
  type EventRecord struct {
  	ChannelID  string    `json:"channel_id"`
  	EventID    string    `json:"event_id"`
  	PublicSlug string    `json:"public_slug"`
  	// OwnerToken is chosei-sama's owner-auth secret for this event. Never
  	// log it, never include it in a Discord response — treat it like
  	// config.json's discord_token (CLAUDE.md's non-negotiable #1).
  	OwnerToken string    `json:"owner_token"`
  	CreatedAt  time.Time `json:"created_at"`
  	CreatedBy  string    `json:"created_by"`
  }

  // DefaultPath is the production data file location, relative to the
  // working directory the bot is launched from — mirrors config.json's
  // "./config.json" convention (internal/config/config.go's defaultConfigPath).
  const DefaultPath = "data/chosei-events.json"

  // Store is a mutex-guarded single-writer JSON file store for EventRecord.
  // Every writer of the SAME underlying file must share one *Store instance
  // (one sync.Mutex) — see Default() for the production singleton.
  type Store struct {
  	mu   sync.Mutex
  	path string
  }

  // New returns a Store backed by path. Production code should use Default()
  // instead, to guarantee every command shares the same mutex; New is for
  // tests, where each test's own t.TempDir()-backed file legitimately needs
  // its own, independent Store.
  func New(path string) *Store {
  	return &Store{path: path}
  }

  var (
  	defaultOnce  sync.Once
  	defaultStore *Store
  )

  // Default returns the process-wide singleton Store for DefaultPath. Every
  // command that persists or reads chosei-sama EventRecords must call
  // Default() — not New(DefaultPath) — so every read/write to
  // data/chosei-events.json goes through the same sync.Mutex.
  func Default() *Store {
  	defaultOnce.Do(func() { defaultStore = New(DefaultPath) })
  	return defaultStore
  }

  // Save appends record, or replaces the existing record with the same
  // EventID if one is already stored.
  func (s *Store) Save(record EventRecord) error {
  	s.mu.Lock()
  	defer s.mu.Unlock()

  	records, err := s.readLocked()
  	if err != nil {
  		return err
  	}

  	replaced := false
  	for i, r := range records {
  		if r.EventID == record.EventID {
  			records[i] = record
  			replaced = true
  			break
  		}
  	}
  	if !replaced {
  		records = append(records, record)
  	}

  	return s.writeLocked(records)
  }

  // FindLatestByChannel returns the most recently created EventRecord for
  // channelID (ok=false if channelID has no stored records).
  func (s *Store) FindLatestByChannel(channelID string) (EventRecord, bool, error) {
  	s.mu.Lock()
  	defer s.mu.Unlock()

  	records, err := s.readLocked()
  	if err != nil {
  		return EventRecord{}, false, err
  	}

  	var latest EventRecord
  	found := false
  	for _, r := range records {
  		if r.ChannelID != channelID {
  			continue
  		}
  		if !found || r.CreatedAt.After(latest.CreatedAt) {
  			latest = r
  			found = true
  		}
  	}
  	return latest, found, nil
  }

  // readLocked reads and parses the JSON file. Caller must hold mu. A
  // missing file is treated as an empty store (not an error) — the
  // pre-first-/schedule state.
  func (s *Store) readLocked() ([]EventRecord, error) {
  	data, err := os.ReadFile(s.path)
  	if err != nil {
  		if os.IsNotExist(err) {
  			return nil, nil
  		}
  		return nil, fmt.Errorf("store: reading %q: %w", s.path, err)
  	}
  	if len(data) == 0 {
  		return nil, nil
  	}
  	var records []EventRecord
  	if err := json.Unmarshal(data, &records); err != nil {
  		return nil, fmt.Errorf("store: parsing %q: %w", s.path, err)
  	}
  	return records, nil
  }

  // writeLocked serializes records to a temp file in the same directory and
  // renames it over the destination path, so a crash mid-write cannot leave
  // a truncated/corrupt data/chosei-events.json (os.Rename is atomic within
  // the same filesystem on both POSIX and Windows NTFS). Caller must hold mu.
  func (s *Store) writeLocked(records []EventRecord) error {
  	dir := filepath.Dir(s.path)
  	if err := os.MkdirAll(dir, 0o755); err != nil {
  		return fmt.Errorf("store: creating directory %q: %w", dir, err)
  	}

  	data, err := json.MarshalIndent(records, "", "  ")
  	if err != nil {
  		return fmt.Errorf("store: marshaling records: %w", err)
  	}

  	tmp, err := os.CreateTemp(dir, ".chosei-events-*.json.tmp")
  	if err != nil {
  		return fmt.Errorf("store: creating temp file: %w", err)
  	}
  	tmpPath := tmp.Name()

  	if _, err := tmp.Write(data); err != nil {
  		tmp.Close()
  		os.Remove(tmpPath)
  		return fmt.Errorf("store: writing temp file: %w", err)
  	}
  	if err := tmp.Close(); err != nil {
  		os.Remove(tmpPath)
  		return fmt.Errorf("store: closing temp file: %w", err)
  	}
  	if err := os.Rename(tmpPath, s.path); err != nil {
  		os.Remove(tmpPath)
  		return fmt.Errorf("store: renaming temp file to %q: %w", s.path, err)
  	}
  	return nil
  }
  ```
  **Important:** do NOT call `Save`/any mutating method on `Default()` from any test — `Default()`'s path is the real relative `data/chosei-events.json`, which under `go test` resolves relative to `internal/store/` and would leave a stray file in the working tree. `TestDefault_ReturnsSameInstanceAcrossCalls` above only checks pointer identity — it never touches disk.
- [ ] Run `go test ./internal/store/...` — expect `ok`.
- [ ] Run `go vet ./internal/store/...` — expect no output.
- [ ] Commit boundary: not yet independently meaningful (no `FindByChannelAndRef` yet, needed by Task 10/12) — bundle with Task 6.

### Task 6 — `internal/store`: `FindByChannelAndRef` + concurrency test

- [ ] Add to `events_test.go`:
  ```go
  func TestStore_FindByChannelAndRef_MatchesEventIDOrPublicSlug(t *testing.T) {
  	dir := t.TempDir()
  	st := New(filepath.Join(dir, "chosei-events.json"))
  	record := EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "tok", CreatedAt: time.Now(), CreatedBy: "user-1"}
  	if err := st.Save(record); err != nil {
  		t.Fatalf("Save: %v", err)
  	}

  	byID, ok, err := st.FindByChannelAndRef("channel-1", "event-1")
  	if err != nil || !ok || byID.PublicSlug != "abc123" {
  		t.Fatalf("expected match by EventID, got ok=%v err=%v record=%+v", ok, err, byID)
  	}

  	bySlug, ok, err := st.FindByChannelAndRef("channel-1", "abc123")
  	if err != nil || !ok || bySlug.EventID != "event-1" {
  		t.Fatalf("expected match by PublicSlug, got ok=%v err=%v record=%+v", ok, err, bySlug)
  	}

  	_, ok, err = st.FindByChannelAndRef("channel-2", "event-1")
  	if err != nil {
  		t.Fatalf("unexpected error: %v", err)
  	}
  	if ok {
  		t.Fatal("expected no match when ref exists but in a different channel — refs must be channel-scoped")
  	}
  }

  func TestStore_Save_ConcurrentWritesDoNotCorruptFile(t *testing.T) {
  	dir := t.TempDir()
  	path := filepath.Join(dir, "chosei-events.json")
  	st := New(path)

  	const n = 20
  	var wg sync.WaitGroup
  	for i := 0; i < n; i++ {
  		wg.Add(1)
  		go func(i int) {
  			defer wg.Done()
  			err := st.Save(EventRecord{
  				ChannelID:  "channel-1",
  				EventID:    fmt.Sprintf("event-%d", i),
  				PublicSlug: fmt.Sprintf("slug-%d", i),
  				OwnerToken: "tok",
  				CreatedAt:  time.Now(),
  				CreatedBy:  "user-1",
  			})
  			if err != nil {
  				t.Errorf("Save() goroutine %d: %v", i, err)
  			}
  		}(i)
  	}
  	wg.Wait()

  	data, err := os.ReadFile(path)
  	if err != nil {
  		t.Fatalf("reading file after concurrent Save: %v", err)
  	}
  	var records []EventRecord
  	if err := json.Unmarshal(data, &records); err != nil {
  		t.Fatalf("file is not valid JSON after concurrent Save (mutex failed to serialize writes): %v", err)
  	}
  	if len(records) != n {
  		t.Fatalf("expected %d records after %d concurrent Save calls, got %d — a lost update means the mutex isn't actually serializing writes", n, n, len(records))
  	}
  }
  ```
  Add `"fmt"` and `"sync"` to `events_test.go`'s import block.
- [ ] Run `go test ./internal/store/...` — expect compile failure (`FindByChannelAndRef` undefined).
- [ ] Add to `events.go`:
  ```go
  // FindByChannelAndRef returns the EventRecord in channelID whose EventID or
  // PublicSlug equals ref (ok=false if none matches). Scoped to channelID so
  // a ref guessed/copied from a different channel cannot be used to read
  // that channel's stored owner token from this one.
  func (s *Store) FindByChannelAndRef(channelID, ref string) (EventRecord, bool, error) {
  	s.mu.Lock()
  	defer s.mu.Unlock()

  	records, err := s.readLocked()
  	if err != nil {
  		return EventRecord{}, false, err
  	}

  	for _, r := range records {
  		if r.ChannelID == channelID && (r.EventID == ref || r.PublicSlug == ref) {
  			return r, true, nil
  		}
  	}
  	return EventRecord{}, false, nil
  }
  ```
- [ ] Run `go test ./internal/store/... && go vet ./internal/store/...` — expect `ok`, no vet output. Optionally also `go test -race ./internal/store/...` and confirm no data-race report.
- [ ] Commit boundary: Tasks 5+6 together are independently committable — `internal/store` is now feature-complete with no caller yet.

### Task 7 — `internal/choseisama`: env-var base URL override

**Correction record:** an earlier draft of this task added a `ChoseiSamaBaseURL` field to `internal/config` instead. Two independent reviews (a Claude plan-reviewer and a separate Codex CLI review) both found that config value was dead code — every command constructs its `choseisama.Client` in its own `init()`, which runs before `main()` ever calls `config.Load()`, so there was no code path from `cfg` to the already-constructed clients without either wiring through `main.go` (breaking Task 14's "zero main.go changes" property) or leaving the setting silently unused. **`internal/config` is untouched by this phase** — do not create or modify `internal/config/config.go` or `internal/config/config_test.go`; they remain exactly as Phase A left them.

- [ ] Write the failing test first. Add to `C:\Users\<user>\Documents\todayistodayBot\internal\choseisama\client_test.go`:
  ```go
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
  ```
- [ ] Run `cd C:\Users\<user>\Documents\todayistodayBot && go test ./internal/choseisama/... -run TestNewClient`. `TestNewClient_ChoseiSamaBaseURLEnvOverride` fails (current `NewClient` unconditionally sets `BaseURL: defaultBaseURL`, ignoring any env var) — this is the RED test driving this task. `TestNewClient_DefaultsToProductionBaseURL` already passes trivially against the pre-fix code; it's kept as a companion characterization test, not itself a red/green driver.
- [ ] Modify `C:\Users\<user>\Documents\todayistodayBot\internal\choseisama\client.go`. Add `"os"` to the import block, and replace the existing `NewClient` function with:
  ```go
  // NewClient returns a Client pointed at chosei-sama's production API, or
  // at the URL in the CHOSEI_SAMA_BASE_URL environment variable if it is
  // set and non-empty — an escape hatch for pointing the bot at a local
  // `wrangler dev` instance (see chosei-sama/README.md's "Local Setup"
  // section), not a config.json-backed setting. This is resolved entirely
  // inside internal/choseisama (no internal/config involvement) precisely
  // because every command's init() constructs its *choseisama.Client
  // before main() ever calls config.Load() — routing this through
  // internal/config would require either wiring config.Load()'s result
  // into each command's init() (impossible: init() order is unspecified
  // relative to main()'s body) or breaking the "zero main.go changes"
  // property Task 14 verifies. An env var read at NewClient() call time
  // has neither problem.
  func NewClient(httpClient *http.Client) *Client {
  	return &Client{httpClient: httpClient, BaseURL: resolveBaseURL()}
  }

  // resolveBaseURL returns CHOSEI_SAMA_BASE_URL's value if set and
  // non-empty, else defaultBaseURL.
  func resolveBaseURL() string {
  	if v := os.Getenv("CHOSEI_SAMA_BASE_URL"); v != "" {
  		return v
  	}
  	return defaultBaseURL
  }
  ```
- [ ] Run `go test ./internal/choseisama/... && go vet ./internal/choseisama/...` — expect `ok` (all tests from Tasks 1-4 plus these two), no vet output.
- [ ] No change needed in Tasks 9, 10, 12 (`schedule.go`, `schedule_result.go`, `reminder.go`): all three already call `choseisama.NewClient(http.DefaultClient)` in their own `init()` with no other arguments, so they automatically pick up the env-var override the moment this task lands.
- [ ] Commit boundary: independently committable — "chosei-samaクライアントにCHOSEI_SAMA_BASE_URL環境変数によるベースURL上書きを追加".

### Task 8 — `internal/commands/schedule_test.go`: parsing/validation/candidate-building tests (RED, no implementation yet)

- [ ] Write the failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\schedule_test.go`:
  ```go
  package commands

  import (
  	"testing"
  	"time"
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
  ```
- [ ] Run `cd C:\Users\<user>\Documents\todayistodayBot && go test ./internal/commands/... -run TestScheduleCommand_Definition` (and the other new `Test*` names) — expect a build failure (`ScheduleCommand`, `parseStartOffset`, `parseTimeArg`, `validateScheduleDays`, `buildCandidates` all undefined).
- [ ] **Do not create `schedule.go` in this task.** `internal/store.EventRecord` and `internal/choseisama.Client` are both needed simultaneously by any correctly-typed `ScheduleCommand` struct — Task 8 only adds the test file above (left in a RED, non-compiling state); Task 9 supplies the real, final `schedule.go`.
- [ ] Commit boundary: Task 8 has **no standalone commit** — its test file is committed together with Task 9's implementation.

### Task 9 — `internal/commands/schedule.go`: full implementation

**Correction record:** an earlier draft of this task wrote the full `schedule.go` implementation first and only appended the three integration tests below afterward, without ever confirming they failed for the stated reason first — a TDD-discipline lapse a plan review caught. This version writes ALL of this task's tests before any implementation exists.

- [ ] **First, extend `schedule_test.go` with all of this task's tests — do not write `schedule.go` yet.** Replace `schedule_test.go`'s import block (Task 8 only had `"testing"` and `"time"`) with the full set both Task 8's and Task 9's tests need:
  ```go
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
  ```
  Then append these three integration tests below Task 8's existing tests (`TestScheduleCommand_Definition`, `TestParseStartOffset`, `TestParseTimeArg`, `TestValidateScheduleDays`, `TestBuildCandidates_LabelsAndStartsAt`, all unchanged from Task 8):
  ```go
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
  				"ownerPath": "x", "exportPath": "y",
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
  ```
- [ ] Run `cd C:\Users\<user>\Documents\todayistodayBot && go test ./internal/commands/...`. Expect a **build failure**: `ScheduleCommand`, `parseStartOffset`, `parseTimeArg`, `validateScheduleDays`, `buildCandidates` are all still undefined (`schedule.go` doesn't exist yet). This confirms every test added across Task 8 and this step is genuinely red before any implementation exists — the entire `schedule_test.go` file, not just the newly-added tests, fails to compile at this checkpoint.
- [ ] **Now** implement. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\schedule.go`:
  ```go
  package commands

  import (
  	"context"
  	"fmt"
  	"log/slog"
  	"net/http"
  	"strconv"
  	"time"

  	"github.com/SioKo-Shox3/todayistodayBot/internal/choseisama"
  	"github.com/SioKo-Shox3/todayistodayBot/internal/store"
  	"github.com/bwmarrin/discordgo"
  )

  func init() {
  	Register(&ScheduleCommand{
  		choseisamaClient: choseisama.NewClient(http.DefaultClient),
  		store:            store.Default(),
  	})
  }

  // ScheduleCommand creates a chosei-sama schedule-coordination event from
  // Discord slash-command args, persists a local reference to it, and posts
  // an embed with the public URL.
  type ScheduleCommand struct {
  	choseisamaClient *choseisama.Client
  	store            *store.Store
  }

  // floatPtr returns a pointer to f — a small helper for discordgo's
  // ApplicationCommandOption.MinValue field, which is *float64 (a pointer,
  // since the zero value 0 must be distinguishable from "no minimum set").
  // Shared with reminder.go's Definition() (Task 11) via this package's
  // normal Go scoping — defined here since schedule.go is the first
  // command to need it. Do not redeclare it in reminder.go.
  func floatPtr(f float64) *float64 {
  	return &f
  }

  func (c *ScheduleCommand) Definition() *discordgo.ApplicationCommand {
  	return &discordgo.ApplicationCommand{
  		Name:        "schedule",
  		Description: "日程調整アンケートを作成します（例: /schedule 忘年会 +1 3 19:00 → 明日から3日分、19:00で作成）",
  		Options: []*discordgo.ApplicationCommandOption{
  			{Type: discordgo.ApplicationCommandOptionString, Name: "title", Description: "イベントのタイトル", Required: true},
  			{Type: discordgo.ApplicationCommandOptionString, Name: "start", Description: "起点日（+1=明日、0=今日、-1=昨日 のような符号付き日数オフセット）", Required: true},
  			{Type: discordgo.ApplicationCommandOptionInteger, Name: "days", Description: "候補日数（1〜50）", Required: true, MinValue: floatPtr(1), MaxValue: 50},
  			{Type: discordgo.ApplicationCommandOptionString, Name: "time", Description: "候補の時刻（HH:mm形式、例: 19:00）", Required: true},
  		},
  	}
  }

  // parseStartOffset parses the "start" arg — a signed day-count offset from
  // "today". strconv.Atoi already accepts a leading "+" sign.
  func parseStartOffset(arg string) (int, error) {
  	offset, err := strconv.Atoi(arg)
  	if err != nil {
  		return 0, fmt.Errorf("❌ 起点日の形式が正しくありません。例: `+1`（明日）、`0`（今日）、`-1`（昨日）")
  	}
  	return offset, nil
  }

  // parseTimeArg parses the "time" arg as HH:mm.
  func parseTimeArg(arg string) (hour, minute int, err error) {
  	t, parseErr := time.Parse("15:04", arg)
  	if parseErr != nil {
  		return 0, 0, fmt.Errorf("❌ 時刻の形式が正しくありません。正しい形式: `19:00`、`09:30` など")
  	}
  	return t.Hour(), t.Minute(), nil
  }

  // validateScheduleDays enforces chosei-sama's candidatesPerEvent limit
  // (chosei-sama/src/shared/schema.ts:5, LIMITS.candidatesPerEvent = 50).
  // Kept even though Discord's client now also enforces this via
  // MinValue/MaxValue above — defense in depth against direct API calls or
  // stale client caches.
  func validateScheduleDays(days int) error {
  	if days < 1 || days > 50 {
  		return fmt.Errorf("❌ 日数は1〜50の範囲で指定してください。")
  	}
  	return nil
  }

  var japaneseWeekdays = [...]string{"日", "月", "火", "水", "木", "金", "土"} // indexed by time.Weekday() (Sunday=0)

  // buildCandidates generates `days` daily candidates starting at base's
  // calendar date, each at hour:minute in base's own time.Location().
  // StartsAt is formatted in UTC per chosei-sama's candidateInputSchema,
  // which requires a bare (UTC-only) ISO 8601 datetime string.
  func buildCandidates(base time.Time, days, hour, minute int) []choseisama.Candidate {
  	candidates := make([]choseisama.Candidate, days)
  	for i := 0; i < days; i++ {
  		d := base.AddDate(0, 0, i)
  		startsAt := time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, d.Location())
  		label := fmt.Sprintf("%04d-%02d-%02d %02d:%02d (%s)",
  			startsAt.Year(), int(startsAt.Month()), startsAt.Day(), startsAt.Hour(), startsAt.Minute(),
  			japaneseWeekdays[int(startsAt.Weekday())])
  		candidates[i] = choseisama.Candidate{
  			Label:    label,
  			StartsAt: startsAt.UTC().Format(time.RFC3339),
  		}
  	}
  	return candidates
  }

  // resolveDisplayName returns the invoking user's Discord display name. A
  // slash-command Interaction populates exactly one of Member (guild) or
  // User (DM).
  func resolveDisplayName(i *discordgo.InteractionCreate) string {
  	if i.Member != nil {
  		return i.Member.DisplayName()
  	}
  	if i.User != nil {
  		return i.User.DisplayName()
  	}
  	return "unknown"
  }

  // resolveUserID returns the invoking user's Discord user ID (for
  // EventRecord.CreatedBy).
  func resolveUserID(i *discordgo.InteractionCreate) string {
  	if i.Member != nil && i.Member.User != nil {
  		return i.Member.User.ID
  	}
  	if i.User != nil {
  		return i.User.ID
  	}
  	return ""
  }

  // buildAndCreateEvent validates args, generates candidates, calls
  // CreateEvent, persists the resulting EventRecord, and returns the embed
  // to post — or a Go error whose Error() text IS the exact Japanese
  // user-facing message to show instead. now is injected for deterministic
  // tests; production Handle() passes time.Now().
  func (c *ScheduleCommand) buildAndCreateEvent(ctx context.Context, title, startArg string, days int, timeArg, channelID, userID, displayName string, now time.Time) (*discordgo.MessageEmbed, error) {
  	if err := validateScheduleDays(days); err != nil {
  		return nil, err
  	}
  	offset, err := parseStartOffset(startArg)
  	if err != nil {
  		return nil, err
  	}
  	hour, minute, err := parseTimeArg(timeArg)
  	if err != nil {
  		return nil, err
  	}

  	base := now.AddDate(0, 0, offset)
  	candidates := buildCandidates(base, days, hour, minute)

  	resp, err := c.choseisamaClient.CreateEvent(ctx, choseisama.CreateEventRequest{
  		Title:      title,
  		OwnerName:  displayName,
  		Candidates: candidates,
  	})
  	if err != nil {
  		slog.Error("choseisama: CreateEvent failed", "error", err)
  		return nil, fmt.Errorf("❌ 日程調整の作成に失敗しました。")
  	}

  	if err := c.store.Save(store.EventRecord{
  		ChannelID:  channelID,
  		EventID:    resp.Event.ID,
  		PublicSlug: resp.Event.PublicSlug,
  		OwnerToken: resp.Secrets.OwnerToken,
  		CreatedAt:  now,
  		CreatedBy:  userID,
  	}); err != nil {
  		slog.Error("store: Save failed", "error", err)
  		return nil, fmt.Errorf("❌ 日程調整の保存に失敗しました。")
  	}

  	return buildScheduleEmbed(resp, displayName), nil
  }

  func buildScheduleEmbed(resp *choseisama.CreateEventResponse, displayName string) *discordgo.MessageEmbed {
  	fields := make([]*discordgo.MessageEmbedField, 0, len(resp.Event.Candidates))
  	for i, cand := range resp.Event.Candidates {
  		fields = append(fields, &discordgo.MessageEmbedField{
  			Name:   fmt.Sprintf("候補%d", i+1),
  			Value:  cand.Label,
  			Inline: false,
  		})
  	}
  	return &discordgo.MessageEmbed{
  		Title:       fmt.Sprintf("📅 %s", resp.Event.Title),
  		URL:         resp.URLs.PublicPath,
  		Description: "参加可能な日程を選んで回答してください！\n" + resp.URLs.PublicPath,
  		Color:       0x3498db,
  		Footer:      &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("作成者: %s", displayName)},
  		Fields:      fields,
  	}
  }

  func (c *ScheduleCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
  	data := i.ApplicationCommandData()
  	var title, startArg, timeArg string
  	var days int
  	for _, opt := range data.Options {
  		switch opt.Name {
  		case "title":
  			title = opt.StringValue()
  		case "start":
  			startArg = opt.StringValue()
  		case "days":
  			days = int(opt.IntValue())
  		case "time":
  			timeArg = opt.StringValue()
  		}
  	}

  	embed, err := c.buildAndCreateEvent(context.Background(), title, startArg, days, timeArg, i.ChannelID, resolveUserID(i), resolveDisplayName(i), time.Now())

  	respData := &discordgo.InteractionResponseData{}
  	if err != nil {
  		respData.Content = err.Error()
  	} else {
  		respData.Embeds = []*discordgo.MessageEmbed{embed}
  	}

  	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
  		Type: discordgo.InteractionResponseChannelMessageWithSource,
  		Data: respData,
  	})
  }
  ```
- [ ] Run `go test ./internal/commands/... && go vet ./internal/commands/...` — expect `ok` (all of Task 8's tests AND this task's three integration tests passing together), no vet output.
- [ ] Commit boundary: Tasks 8+9 together are independently committable ("`/schedule`コマンドを追加").

### Task 10 — `internal/commands/schedule_result.go`: `/schedule-result` + shared `resolveTargetEventRecord`

- [ ] Write the failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\schedule_result_test.go`:
  ```go
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
  ```
- [ ] Run `go test ./internal/commands/... -run 'TestScheduleResultCommand|TestResolveTargetEventRecord'` — expect a build failure.
- [ ] Implement. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\schedule_result.go`:
  ```go
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
  		slog.Error("choseisama: GetEvent failed", "error", err)
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

  	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
  		Type: discordgo.InteractionResponseChannelMessageWithSource,
  		Data: respData,
  	})
  }
  ```
- [ ] Run `go test ./internal/commands/... && go vet ./internal/commands/...` — expect `ok`, no vet output.
- [ ] Commit boundary: independently committable ("`/schedule-result`コマンドを追加").

### Task 11 — `internal/commands/reminder.go`: `Definition()` + `/reminder set`

- [ ] Write the failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\reminder_test.go`:
  ```go
  package commands

  import (
  	"context"
  	"encoding/json"
  	"errors"
  	"net/http"
  	"net/http/httptest"
  	"path/filepath"
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
  	if content == "" || content[0:1] != "✅" {
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
  	if content == "" || content[0:1] != "❌" {
  		t.Fatalf("expected a ❌ error message, got: %q", content)
  	}
  }
  ```
- [ ] Run `go test ./internal/commands/... -run 'TestReminderCommand'` — expect a build failure (`ReminderCommand`, `discordWebhookCreator`, `handleSet` undefined).
- [ ] Implement. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\reminder.go` (this task adds `Definition()`, the `discordWebhookCreator` interface, `enabledText`, `stringOptionValue`, and `handleSet` only — `handleOff`/`handleStatus`/`Handle`/`init()` are added in Task 12; keep the file compilable now by **not yet** registering it):
  ```go
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
  		slog.Error("discord: WebhookCreate failed", "error", err)
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
  		slog.Error("choseisama: SetDiscordReminder failed", "error", err)
  		return "❌ リマインダーの登録に失敗しました。"
  	}

  	return fmt.Sprintf("✅ リマインダーを設定しました。\n全員回答済み通知: %s / %d時間前\n締切通知: %s / %d時間前\n@everyone: %s",
  		enabledText(setting.AllOkEnabled), setting.AllOkHoursBefore,
  		enabledText(setting.DeadlineEnabled), setting.DeadlineHoursBefore,
  		enabledText(setting.MentionEveryone))
  }

  var _ = errors.New // used by handleOff/handleStatus added in Task 12
  ```
- [ ] Run `go build ./... && go test ./internal/commands/... -run 'TestReminderCommand' && go vet ./internal/commands/...` — expect success, all this task's tests passing, no vet output.
- [ ] Commit boundary: not independently meaningful alone — bundle with Task 12.

### Task 12 — `internal/commands/reminder.go`: `/reminder off`, `/reminder status`, `Handle`, `init()`

- [ ] Add to `reminder_test.go`:
  ```go
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
  ```
- [ ] Run `go test ./internal/commands/... -run 'TestReminderCommand'` — expect a build failure (`handleOff`/`handleStatus` undefined).
- [ ] In `reminder.go`: remove the temporary `var _ = errors.New` line and add:
  ```go
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
  		slog.Error("choseisama: SetDiscordReminder (off) failed", "error", err)
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
  		slog.Error("choseisama: GetOwnerView failed", "error", err)
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
  ```
  Then add the `init()` registration at the top of the file (right after the `import` block):
  ```go
  func init() {
  	Register(&ReminderCommand{
  		choseisamaClient: choseisama.NewClient(http.DefaultClient),
  		store:            store.Default(),
  	})
  }
  ```
- [ ] Run `go build ./... && go test ./internal/commands/... && go vet ./internal/commands/...` — expect success, `ok` (all `TestReminderCommand*` tests across Tasks 11-12 passing), no vet output.
- [ ] Commit boundary: Tasks 11+12 together are independently committable ("`/reminder`コマンド(set/off/status)を追加").

### Task 13 — `.gitignore`: add `data/`, retire stale `.NET`-era entries

**Correction record:** this task was originally numbered 14 and ran after the main.go-verification task (then numbered 13, which includes a manual smoke test that creates a real `data/chosei-events.json` containing an `OwnerToken`). A Codex review found this ordering risky: `data/` wasn't gitignored yet at the point the smoke test could create it. Swapped so `/data/` is ignored before any real event-reference file can exist.

- [ ] Modify `C:\Users\<user>\Documents\todayistodayBot\.gitignore`. The current `# Discord Bot specific` section has two old .NET-era data-file entries (`schedules.json`/`reminders.json`, from the deleted .NET `ScheduleStorageService`/`ReminderService`, which the Go rewrite does not use). Replace them with the new Go store's actual runtime file location:
  ```
  # Discord Bot specific
  # 機密情報を含む可能性のある設定ファイル
  appsettings.json
  appsettings.Development.json
  appsettings.Production.json
  config.json
  *.env
  .env

  # chosei-sama連携のローカルイベント参照ファイル(internal/store が書き込む。
  # owner tokenを含む秘密情報 — 絶対にコミットしない)
  /data/

  # Claude Code ワークフロー(マシンローカル。別マシンでは deploy.ps1 で再展開)
  .claude/settings.local.json
  .claude/worktrees/
  ```
- [ ] Verify: `cd C:\Users\<user>\Documents\todayistodayBot && git check-ignore -v data/chosei-events.json` — expect it to print a match against the new `/data/` line. If `data/` doesn't exist yet locally, first create a throwaway file purely to exercise `git check-ignore`, then remove it again — do not leave a stray file in the working tree.
- [ ] Commit boundary: independently committable at any point — but logically lands before Task 14's manual smoke test (and no later than alongside Tasks 5-6) so a stray `data/chosei-events.json` from local testing is never accidentally stageable. Commit as "`.gitignore`にdata/を追加し.NET時代のスケジュール/リマインダーファイル記述を削除".

### Task 14 — Confirm `cmd/bot/main.go` needs no change (verification-only task, no source edit)

**Correction record:** this task was originally numbered 13 and ran before the `.gitignore` update (then numbered 14). Swapped — see Task 13's correction record above.

- [ ] This task makes no code changes — it exists to satisfy the explicit requirement that Phase B's plan verify, not silently assume, that `cmd/bot/main.go` doesn't need editing.
- [ ] Re-read `C:\Users\<user>\Documents\todayistodayBot\cmd\bot\main.go` — confirm it still only depends on `commands.All()` and dispatches by `Definition().Name`, with zero references to any specific command type. `ScheduleCommand`/`ScheduleResultCommand`/`ReminderCommand` all self-register via their own `init()` (Tasks 9, 10, 12), exactly like `ping`/`help`/`weather`/`today`/`dice` already do.
- [ ] Run `cd C:\Users\<user>\Documents\todayistodayBot && go build ./... && go test ./cmd/bot/...` — expect success and `ok`.
- [ ] Manual smoke-test evidence (requires a real Discord bot token in `config.json` or `DISCORD_TOKEN` — run once, locally, before considering Phase B done): `go run ./cmd/bot` and check the `"bot is running"` log line's `registered_commands` field reads **8** (5 from Phase A + `schedule`, `schedule-result`, `reminder`) with no code change to `main.go`. In Discord, confirm `/schedule`, `/schedule-result`, `/reminder set|off|status` all appear in the slash-command autocomplete list. Because Task 13 already gitignored `/data/`, any `data/chosei-events.json` this smoke test creates (containing a real chosei-sama `OwnerToken`) is never at risk of being accidentally staged.
- [ ] Commit boundary: no commit — this task produces no diff.

### Task 15 — `README.md`: describe the real `/schedule`/`/schedule-result`/`/reminder` surface

- [ ] Modify `C:\Users\<user>\Documents\todayistodayBot\README.md`. Replace the "日程調整（フェーズB予定・現時点では未実装）" and "スケジュールリマインダー（フェーズB予定・現時点では未実装）" sections (which currently describe the OLD .NET reaction-based UX and the OLD `/reminder set|enable|disable|list|delete` subcommand names) with:
  ```markdown
  ### 日程調整（chosei-sama連携）
  - `/schedule <title> <start> <days> <time>` - 日程調整アンケートを作成（chosei-samaに委譲）
    - 例: `/schedule 忘年会 +1 3 19:00` → 明日から3日分、各日19:00で候補を作成
    - `start`: 起点日（+数字=○日後、-数字=○日前、0=今日）
    - `days`: 候補日数（1〜50）
    - `time`: 候補の時刻（HH:mm形式）
    - 作成後、chosei-samaの公開URL付きEmbedを投稿します。回答はそのURL先のWebフォームから行います
  - `/schedule-result [event]` - アンケート結果を表示
    - `event`: 対象イベントの参照（省略時はこのチャンネルで最後に作成したイベント）
    - 各候補の✅（参加可能）🤔（未定）❌（不参加）集計を表示

  ### スケジュールリマインダー（chosei-sama連携）
  - `/reminder set [all-ok-hours] [deadline-hours] [mention-everyone] [event]` - このチャンネルにDiscord Webhookを作成し、chosei-samaにリマインダーを登録
    - `all-ok-hours`: 全員が参加可能な候補の何時間前に通知するか（1〜168、既定3）
    - `deadline-hours`: 回答締切の何時間前に通知するか（1〜168、指定すると締切リマインダーが有効になります、既定24）
    - `mention-everyone`: @everyoneで通知するか（既定false）
    - ⚠️ Botに対象チャンネルの「Webhookの管理」権限が必要です
  - `/reminder off [event]` - リマインダーを無効化
  - `/reminder status [event]` - 現在のリマインダー設定を表示
  - 通知の配信自体はchosei-sama側のCron（30分おき）が行い、Botはタイマーロジックを一切持ちません
  ```
- [ ] Replace the "日程調整システム（フェーズB予定・現時点では未実装）" functional-description section with:
  ```markdown
  ### 日程調整システム（chosei-sama連携）
  - 候補生成・回答集計・リマインダー配信のロジックは外部サービス [chosei-sama](https://chosei-sama.choseisama.workers.dev/) に委譲
  - Botは「Discordコマンド ⇄ chosei-sama API」の橋渡しと、作成したイベントの参照情報(イベントID・owner token)をチャンネルごとにローカル保持する役割のみを持つ
  - ローカル保持先: `data/chosei-events.json`（`sync.Mutex`で保護された単一ライター。`.gitignore`対象 — owner tokenを含むため絶対にコミットしない）
  ```
- [ ] Update the "設定" section (note: `chosei_sama_base_url` is NOT a `config.json` field — the base-URL override lives entirely inside `internal/choseisama` per Task 7's env-var-only design, not `internal/config`):
  ```markdown
  ## 設定

  `config.json`（または `DISCORD_TOKEN` 環境変数）で以下の設定が可能です：

  - `discord_token`: Discordボットのトークン（必須。環境変数 `DISCORD_TOKEN` が設定されている場合はそちらが優先）

  `/reminder set` を使うには、Botに対象チャンネルでの「Webhookの管理」権限が必要です。

  ### 高度な設定（通常は不要）

  - `CHOSEI_SAMA_BASE_URL` 環境変数: chosei-samaのベースURLを上書きします（既定値
    `https://chosei-sama.choseisama.workers.dev`）。ローカルの `wrangler dev` インスタンス相手に
    動作確認する場合など、稀な用途向けです。`config.json` にこれに対応するフィールドはありません
    （環境変数のみ対応 — `internal/choseisama` パッケージが直接読みます。詳細は
    `internal/choseisama/client.go` の `NewClient`/`resolveBaseURL`）。
  ```
- [ ] Update the "プロジェクト構造" tree to add the two new packages and three new command files:
  ```
  todayistodayBot/
  ├── cmd/
  │   └── bot/                     # エントリポイント
  │       └── main.go
  ├── internal/
  │   ├── commands/                # 各スラッシュコマンド（Command インターフェース実装）
  │   │   ├── registry.go          # Command インターフェース定義・自己登録レジストリ
  │   │   ├── ping.go
  │   │   ├── help.go
  │   │   ├── weather.go
  │   │   ├── today.go
  │   │   ├── dice.go
  │   │   ├── schedule.go
  │   │   ├── schedule_result.go
  │   │   └── reminder.go
  │   ├── config/                  # 設定読み込み（config.json / 環境変数）
  │   │   └── config.go
  │   ├── weather/                 # Open-Meteo クライアント
  │   │   ├── client.go
  │   │   └── cities.go
  │   ├── today/                   # 今日は何の日 API クライアント
  │   │   └── client.go
  │   ├── choseisama/               # chosei-sama API クライアント
  │   │   └── client.go
  │   └── store/                    # chosei-sama イベント参照のローカルJSON永続化
  │       └── events.go
  ├── deploy/                      # Dockerfile・systemd unit
  ├── Makefile                     # ビルド/テスト/Docker イメージ用タスク
  ├── config.json.template         # 設定ファイルのテンプレート
  └── go.mod
  ```
- [ ] Verify: `grep -n "フェーズB予定・現時点では未実装" README.md` — expect no matches.
- [ ] Commit boundary: independently committable, sequence after Tasks 9-12 land ("READMEのコマンド一覧をchosei-sama連携の実装に合わせて更新").

### Task 16 — `Docs/agent-guide/architecture.md`: document the new persistence layer and dependency edges

- [ ] Modify `C:\Users\<user>\Documents\todayistodayBot\Docs\agent-guide\architecture.md`.
  - In the "全体構造" table, add two rows after the existing "外部 API クライアント" row:
    ```markdown
    | chosei-sama クライアント | 日程調整・リマインダー用の HTTP クライアント | `internal/choseisama/client.go` |
    | ローカル永続化 | chosei-sama イベント参照(channel→event/owner token)の JSON 永続化 | `internal/store/events.go` |
    ```
  - Update the sentence claiming "`Handlers/` / `Services/` / `Models/` の旧レイヤーは引き継がない — フェーズAは永続化を持たず..." (now false for the current repo state) to:
    ```markdown
    `Handlers/` / `Services/` / `Models/` の旧レイヤーは引き継いでいない。フェーズAは永続化を持たなかったが、
    フェーズBで`internal/store`による JSON 永続化(`data/chosei-events.json`)を初めて導入した — 「危険地帯」節参照。
    ```
  - In "所有権と寿命", add a bullet:
    ```markdown
    - `internal/store.Default()` はプロセス内シングルトンの `*store.Store` を返す(`sync.Once` で初回のみ構築)。
      `ScheduleCommand`/`ScheduleResultCommand`/`ReminderCommand` の3コマンドは全て `init()` でこの
      `Default()` を受け取り、同一の `sync.Mutex` を共有する — `store.New(path)` で個別に構築した
      `*Store` を本番コードで使うと、同じファイルに対して排他されないミューテックスが複数存在することに
      なり単一ライター規律が壊れる(テストコードでの `New(tempDir)` 使用は正しい — 別ファイルは別
      ミューテックスで正しい)。
    ```
  - Replace "スレッド / 並行性" の第2項(「フェーズAは永続化された共有可変状態を持たないため、mutex等での排他は不要」— これもフェーズBで false になった)を:
    ```markdown
    - フェーズBで導入した `internal/store.Store` は `sync.Mutex` で単一ライター化されている
      (`store.Default()` を3コマンドが共有)。`discordgo` の `Session` は Gateway イベントを内部
      goroutine で配送するため、`Handle()` は複数の interaction が同時に届けば並行に呼ばれ得る —
      この排他は理論上ではなく実際に必要。
    ```
  - In "依存方向", add:
    ```markdown
    - `internal/commands/{schedule,schedule_result,reminder}.go` → `internal/choseisama`, `internal/store`
      (必要なクライアント/ストアを直接生成、または `store.Default()` を使用)。
    - `internal/choseisama`, `internal/store` → 外部 API またはファイルシステムのみ。他の内部パッケージに
      依存しない(逆流禁止 — `internal/choseisama`/`internal/store` から `internal/commands` を呼ばない)。
    ```
  - Replace the entire "危険地帯" section (its opening claim that "危険地帯として残るのは以下の2点のみ" is now stale) with:
    ```markdown
    ## 危険地帯(変更時に必ず計画レビューを通す領域)

    orchestration.md の昇格条件と対応する。ここに触れる変更は計画レビュー(一次 + Codex 二次)必須。

    フェーズBで `internal/store` による JSON 永続化を初めて導入した。危険地帯は以下の3点(フェーズAの
    2点に1点追加):

    1. **秘密情報を絶対にコミットしない** — `DISCORD_TOKEN` 環境変数・`config.json` に加え、
       `data/chosei-events.json` に保存される `OwnerToken`(chosei-sama側の操作権限トークン)も同様
       (`.gitignore` 対象・ログ出力禁止・Discordへの応答に絶対に含めない)。
    2. **新コマンドの `internal/commands` への追加漏れ** — 自動探索(`init()` 自己登録)のため実質発生しない。
    3. **JSON永続化の単一ライター規律**(フェーズBで新規) — `internal/store` の `Store` は同一ファイルに
       対して必ず `store.Default()`(プロセス内シングルトン)経由でアクセスすること。`New(path)` で
       独立に `Store` を構築して本番コードで使うと、同じファイルに対して排他されない複数の `sync.Mutex`
       が存在することになり、`data/chosei-events.json` の読み書き競合で書き込みロスト/JSON破損が
       起こり得る(`internal/store/events_test.go` の並行 `Save` テストはこの規律が壊れていないことの
       回帰テスト)。
    ```
- [ ] Verify: `grep -n "永続化のリスクを持たない\|危険地帯として残るのは以下の2点のみ" Docs/agent-guide/architecture.md` — expect no matches.
- [ ] Commit boundary: independently committable, sequence after Tasks 9-12.

### Task 17 — `Docs/agent-guide/build-and-verify.md`: update package list and the "no persistence" note

- [ ] Modify `C:\Users\<user>\Documents\todayistodayBot\Docs\agent-guide\build-and-verify.md`.
  - Update the package list sentence to:
    ```markdown
    各パッケージ(`internal/config`, `internal/commands`, `internal/weather`, `internal/today`,
    `internal/choseisama`, `internal/store`, `cmd/bot`)に `_test.go` が存在する。`go test ./...` の
    出力で全パッケージが `ok` になり `FAIL` が無いことを確認する。
    ```
  - Replace "フェーズAには `data/*.json` に相当する永続化ファイルは存在しない(永続化なし)。" (now false) with a new bullet in the "コミットしてはいけない生成物" list:
    ```markdown
    - `data/`(フェーズBで導入した chosei-sama 連携のイベント参照ファイル
      `data/chosei-events.json`。owner tokenを含む秘密情報 — `.gitignore` 済み、絶対にコミットしない。
      ローカル開発で生成された `data/` ディレクトリを誤って `git add` しないこと)
    ```
- [ ] Verify: `grep -n "永続化なし\|data/\*.json.*存在しない" Docs/agent-guide/build-and-verify.md` — expect no matches.
- [ ] Commit boundary: independently committable, sequence after Tasks 9-12.

---

## Verification (full gates, run at the end and after each task per the per-task steps above)

From repo root `C:\Users\<user>\Documents\todayistodayBot`:

```
go build ./...
go vet ./...
go test ./...
```

**Success criteria:**
- `go build ./...` exits 0 with no output.
- `go vet ./...` exits 0 with no output.
- `go test ./...` exits 0 and prints `ok` for every package — `internal/config`, `internal/commands`, `internal/weather`, `internal/today`, `internal/choseisama`, `internal/store`, `cmd/bot` — with zero `FAIL` lines.

Additional recommended (not a mandatory gate, but strongly advised given this phase's new concurrency-sensitive code):
```
go test -race ./internal/store/...
```
**Success criteria:** `ok`, no `WARNING: DATA RACE` output.

Manual smoke test (Task 14's evidence): `go run ./cmd/bot` with a real `config.json`/`DISCORD_TOKEN`, confirm the `"bot is running"` log line shows `registered_commands: 8`, and confirm in a real Discord server that `/schedule`, `/schedule-result`, and `/reminder set|off|status` register and respond (a `/reminder set` call requires the bot to already have "Manage Webhooks" permission in that channel).

For the doc-only tasks (13 `.gitignore`, 15 README.md, 16-17 `Docs/agent-guide/*`), the success criterion is the `grep`/`git check-ignore` checks specified in each task's own steps.

---

## Risk level and containment

- **This phase directly touches a designated high-risk area for the first time: shared mutable persisted state.** `internal/store.Store` is the bot's first-ever file-based persistence with concurrent-writer exposure. Containment already built into the design: `sync.Mutex`-guarded single writer enforced via the `store.Default()` singleton pattern (Task 5); a concurrent-write regression test (Task 6) that fails under `go test -race` if the discipline is ever broken; atomic write-then-rename (`writeLocked`, Task 5) so a crash mid-write cannot corrupt `data/chosei-events.json`.
  - **Rollback/containment if this goes wrong post-merge:** the store is additive and isolated — reverting the commit(s) that introduce `internal/store` and its three callers removes the risk entirely without touching Phase A's 5 stateless commands. If corruption is discovered in production, delete `data/chosei-events.json` (loses only the channel→event bookkeeping, not any chosei-sama server-side data) and let the next `/schedule` repopulate it.
- **Secret handling risk:** `EventRecord.OwnerToken` (Task 5) is equivalent in sensitivity to `config.json`'s `discord_token`. Containment: never logged, never included in any Discord response, and the file it's persisted to is gitignored (Task 13).
- **`discordgo.Session.WebhookCreate` (Task 11-12) requires elevated Discord permissions** ("Manage Webhooks") that may not be granted in every deployment. Containment: a user-facing, recoverable failure (covered by `TestReminderCommand_HandleSet_WebhookCreateFailure_ReturnsJapaneseMessage`), not a crash; README.md (Task 15) documents the permission requirement.
- **No elevated risk in Tasks 1-4 and 7 (all `internal/choseisama`)** — pure new-code additions with no existing behavior to regress, same reasoning the Phase A plan used for `internal/weather`/`internal/today`.
- **Design-decision risk (flagged for reviewer attention, not a code-safety issue):** Task 12's interpretation of "`deadline-hours` argument presence enables deadline reminders" is this plan's own resolution of an ambiguity in the design spec. If the reviewer disagrees, it is a one-function change (`handleSet`'s option-parsing loop) with no ripple effect elsewhere.

---

## Commit boundaries

Each logical change gets its own commit (Japanese, imperative mood):

- Tasks 1-4 (`internal/choseisama` client methods): squash into "chosei-samaクライアントを追加" or commit individually.
- Tasks 5-6 (`internal/store`): "chosei-samaイベント参照のローカルJSON永続化を追加", or individually.
- Task 7 (`internal/choseisama` env override): independently committable — "chosei-samaクライアントにCHOSEI_SAMA_BASE_URL環境変数によるベースURL上書きを追加".
- Tasks 8+9 (`schedule.go`): **must** be committed together — "`/schedule`コマンドを追加".
- Task 10 (`schedule_result.go`): "`/schedule-result`コマンドを追加".
- Tasks 11+12 (`reminder.go`): **must** be committed together — "`/reminder`コマンド(set/off/status)を追加".
- Task 13 (`.gitignore`): independently committable at any point — but logically lands before Task 14's manual smoke test (and no later than alongside Tasks 5-6) so a stray `data/chosei-events.json` from local testing is never accidentally stageable. Commit as "`.gitignore`にdata/を追加し.NET時代のデータファイル記述を整理".
- Task 14: no commit (verification-only, no diff).
- Tasks 15-17 (README.md, architecture.md, build-and-verify.md): land after Tasks 9-12 merge; one combined "ドキュメントをchosei-sama連携の実装に合わせて更新" commit or three separate ones.

The **whole phase** is committable/mergeable once: `go build ./...`, `go vet ./...`, `go test ./...` are all clean; `go test -race ./internal/store/...` shows no data race; the Task 14 manual Discord smoke test has passed with evidence recorded; and README.md/architecture.md/build-and-verify.md no longer contain any "フェーズB予定・現時点では未実装" or "フェーズAは永続化を持たない" stale claims.

---

## Self-review notes (checked before returning this plan)

- **Spec coverage check:** every section of `Docs/superpowers/specs/2026-07-06-chosei-sama-integration-design.md` maps to a task — 基本方針/薄いクライアント (Tasks 1-4, 9-12), `/schedule` (Tasks 8-9), `/schedule-result` (Task 10), `/reminder set/off/status` (Tasks 11-12), ローカル永続化・`EventRecord`構造体 (Tasks 5-6, field names verified identical to the spec), 新規パッケージ構成 (file structure section + Tasks 1-12), エラーハンドリング (each command task's Japanese-message mapping, `errNoStoredEvent` matches spec wording verbatim), テスト方針 (`httptest.Server` for `internal/choseisama`, temp-dir for `internal/store`, pure-helper extraction for commands), スコープ外 (Discord-native reactions/buttons, chosei-sama API changes, multi-reminder support, casino/Phase C — none implemented here).
- **Placeholder scan:** no "add appropriate error handling" or "similar to Task N" shortcuts. The two intentional exceptions (Task 1's `var _ = url.PathEscape` and Task 11's `var _ = errors.New`) are documented, temporary unused-import silencers removed by name in the very next task.
- **Type/signature consistency across tasks:** `store.EventRecord` fields used identically in Tasks 9, 10, 12. `choseisama.Client.BaseURL` set the same way in every test file across Tasks 1-4 and 9-12. `choseisama.SetDiscordReminderRequest`'s 5 non-`omitempty` fields populated explicitly in both Task 11's `handleSet` and Task 12's `handleOff`. `discordWebhookCreator`'s method signature matches `*discordgo.Session.WebhookCreate`'s real signature exactly (verified against `restapi.go:2271`, not assumed).
- **Deliberate deviations disclosed, not hidden:** (1) `choseisama.Client.BaseURL` exported vs. `weather.Client.baseURL` private. (2) `internal/choseisama` returns structured `*APIError` instead of weather.go's pre-formatted-string convention (3 callers needing different messages for the same status code). (3) `resolveTargetEventRecord` lives in `schedule_result.go` but is also called from `reminder.go`. (4) The `deadline-hours`-presence-enables-deadline-reminders interpretation is flagged inline and in the Risk section.
- **Uncertain items still explicitly flagged (not hidden):** the CRLF-vs-LF question for new `.go` files follows Phase A's plan (LF for new files). One genuinely new **UNCERTAIN** item: this plan assumes the production Discord bot's OAuth2 scope already includes (or can be granted) the "Manage Webhooks" permission — not verified against the bot's actual current Discord application permissions during investigation. If the deployed bot's role lacks this permission, `/reminder set` will fail gracefully with the Task 12 error message, but an operator will need to grant the permission before the feature is usable.
- **Post-review revision record:** an earlier draft of this plan was checked by two independent reviewers (a Claude plan-reviewer and a separate Codex CLI review) against the real chosei-sama and discordgo source. Both independently found the same real blocker — a `ChoseiSamaBaseURL` setting introduced in the original Task 7 (routed through `internal/config`) that no code path ever actually consumed, since every command's `init()` runs before `main()` calls `config.Load()`. Codex additionally found that Task 10's `TestScheduleResultCommand_BuildResultEmbed_Success` asserted a hardcoded production URL that could never match the mock-server-derived URL `PublicEventURL()` actually returns in a test, and that Task 14's `.gitignore` update originally ran *after* the manual-smoke-test task that could create a real `data/chosei-events.json` containing a secret `OwnerToken`. The Claude reviewer additionally found a TDD-discipline lapse in the original Task 9 (implementation written before its own integration tests were confirmed to fail). All four were fixed in this version: Task 7 now lives entirely in `internal/choseisama` as an env-var-only override; Task 10's test computes its expected URL dynamically; Task 13/14 were swapped so `.gitignore` lands first; Task 9 now writes all its tests before any implementation. See each task's own "Correction record" note for specifics.

---

Relevant files read during investigation (all absolute paths):
- `C:\Users\<user>\Documents\todayistodayBot\Docs\superpowers\specs\2026-07-06-chosei-sama-integration-design.md`
- `C:\Users\<user>\Documents\todayistodayBot\CLAUDE.md`
- `C:\Users\<user>\Documents\todayistodayBot\Docs\agent-guide\architecture.md`
- `C:\Users\<user>\Documents\todayistodayBot\Docs\agent-guide\coding-style.md`
- `C:\Users\<user>\Documents\todayistodayBot\Docs\agent-guide\build-and-verify.md`
- `C:\Users\<user>\Documents\todayistodayBot\Docs\superpowers\plans\2026-07-05-go-migration-phase-a.md`
- `C:\Users\<user>\Documents\todayistodayBot\internal\commands\{weather,today,dice,help,registry}.go` and their `_test.go` files
- `C:\Users\<user>\Documents\todayistodayBot\internal\weather\client.go`, `client_test.go`
- `C:\Users\<user>\Documents\todayistodayBot\internal\config\config.go`, `config_test.go`
- `C:\Users\<user>\Documents\todayistodayBot\cmd\bot\main.go`, `main_test.go`
- `C:\Users\<user>\Documents\todayistodayBot\go.mod`, `.gitignore`, `README.md`
- `C:\Users\<user>\Documents\chosei-sama\src\shared\schema.ts`, `src\shared\types.ts`, `src\worker\app.ts`, `src\worker\repository.ts`, `src\worker\env.ts`, `wrangler.jsonc`, `README.md`
- `C:\Users\<user>\go\pkg\mod\github.com\bwmarrin\discordgo@v0.29.0\{restapi.go,webhook.go,interactions.go,structs.go,message.go,user.go,endpoints.go}`
- Git history: `git show 030fec7^:TodayIsTodayBot/Commands/Handlers/{ScheduleCommand,ReminderCommand}.cs` (deleted .NET source, read for UX/date-format precedent only)
