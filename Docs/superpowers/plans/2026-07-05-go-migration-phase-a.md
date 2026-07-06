# Go Migration Phase A Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the .NET 9.0/Discord.Net bot with a Go/discordgo bot exposing the 5 stateless commands (`ping`, `help`, `weather`, `today`, `dice`) as native Discord slash commands (Interactions API), with auto-discovery command registration, env-first/file-fallback config, cross-platform build tooling, and updated workflow docs — cutting over in the same repo with the old .NET project deleted at the end.
**Architecture:** `cmd/bot/main.go` builds a `discordgo.Session`, imports `internal/commands` (whose files self-register via `init()`), registers Application Commands with Discord, dispatches `InteractionCreate` events by command name, and waits on SIGINT/SIGTERM for graceful shutdown. `internal/weather` and `internal/today` are pure HTTP-client packages (testable via `httptest.Server`) wrapped by thin command handlers with no persistence layer (all 5 ported commands are stateless).
**Tech Stack:** Go (module TBD version, matching `go.mod` in Task 1), `github.com/bwmarrin/discordgo`, standard library `net/http`, `encoding/json`, `log/slog`, `os/signal`, `flag`/`os.Getenv` for config, `net/http/httptest` for tests, Docker multi-stage build, systemd unit.

---

## Task-0 notes (assumptions to confirm before/while implementing)

- **Go module path**: the repo's git remote is `https://github.com/SioKo-Shox3/todayistodayBot.git` (confirmed via `git remote -v`). This plan assumes the Go module path is `github.com/SioKo-Shox3/todayistodayBot`. If the eventual import path differs (fork, rename, private module), Task 1 must be adjusted — this is a one-line change (`go.mod`'s `module` directive and all internal import paths), flag it to the user if it turns out wrong.
- **discordgo version**: pin to the latest tagged release of `github.com/bwmarrin/discordgo` at implementation time (`go get github.com/bwmarrin/discordgo@latest`); do not hand-write a version number in this plan since it will drift.
- **Go toolchain version**: this machine does not have Go installed (`go version` returned "command not found" during planning-time investigation). The implementer must install a current stable Go toolchain (1.22+) before Task 1, and `go.mod`'s `go` directive should reflect whatever is actually installed. Record the installed version in the PR/commit evidence.
- **No `.NET`↔Go parallel period**: per spec ("同じrepo内で即時置き換え"), the `TodayIsTodayBot/` directory and `todayistodayBot-1.sln` are deleted in the *same phase*, as the final task, only after everything else is green — not in a follow-up phase.

---

## File structure (created / modified / deleted)

**Created:**
```
go.mod
go.sum
cmd/bot/main.go
internal/config/config.go
internal/config/config_test.go
internal/commands/registry.go
internal/commands/registry_test.go
internal/commands/ping.go
internal/commands/ping_test.go
internal/commands/help.go
internal/commands/help_test.go
internal/commands/weather.go
internal/commands/weather_test.go
internal/commands/today.go
internal/commands/today_test.go
internal/commands/dice.go
internal/commands/dice_test.go
internal/weather/client.go
internal/weather/client_test.go
internal/weather/cities.go
internal/today/client.go
internal/today/client_test.go
deploy/Dockerfile
deploy/systemd/todayistodaybot.service
Makefile
config.json.template   (replaces appsettings.json.template as the file-fallback config sample)
```

**Modified:**
```
CLAUDE.md
AGENTS.md
Docs/agent-guide/architecture.md
Docs/agent-guide/coding-style.md
Docs/agent-guide/build-and-verify.md
.claude/agents/researcher.md
.claude/agents/planner.md
.claude/agents/plan-reviewer.md
.claude/agents/implementer.md
.claude/agents/impl-reviewer.md
.claude/agents/verifier.md
.claude/hooks/enforce-codex-impl.mjs
.gitignore   (add config.json / Go build artifacts; remove/retire .NET-only entries that no longer apply, keep the ones that still make sense e.g. secrets)
```

**Deleted (final cutover task only):**
```
TodayIsTodayBot/                (entire directory: Commands/, Handlers/, Services/, Models/, Program.cs, TodayIsTodayBot.csproj, appsettings.json.template, bin/, obj/ if present)
todayistodayBot-1.sln
```

**Note on `.codex/agents/*.toml`:** these are TOML mirrors of `.claude/agents/*.md` for Codex. The investigation found `.codex/agents/` contains `implementer.toml`, `impl_reviewer.toml`, etc. — parallel content to the `.md` files. Since the spec explicitly says "`.claude/agents/*`内の.NET固有記述があれば洗い出して更新" (only mentions `.claude/agents`), and CLAUDE.md's own sync rule is specifically scoped to `CLAUDE.md`/`AGENTS.md` (not the agents directories), this plan treats `.claude/agents/*.md` updates as in-scope and required, and flags `.codex/agents/*.toml` as **UNCERTAIN / likely-needed-but-unconfirmed** — see Task 16.

---

## Tasks

### Task 1 — Go module init

- [ ] Create `C:\Users\<user>\Documents\todayistodayBot\go.mod` by running (from repo root):
  ```
  go mod init github.com/SioKo-Shox3/todayistodayBot
  ```
  This produces a `go.mod` with a `module` line and a `go` directive matching the installed toolchain (see Task-0 notes — record the version used).
- [ ] Verify: `go build ./...` — expect `go: no packages to build` style success (no source files yet, no error). Do not proceed if this errors.
- [ ] Commit boundary: this task alone is not independently committable (empty module is not useful) — bundle with Task 2 (registry) as the first commit. See "Commit boundaries" section at the end.

### Task 2 — `internal/commands` registry + self-registration pattern

- [ ] Write failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\registry_test.go`:
  ```go
  package commands

  import (
      "testing"

      "github.com/bwmarrin/discordgo"
  )

  type fakeCommand struct {
      name string
  }

  func (f *fakeCommand) Definition() *discordgo.ApplicationCommand {
      return &discordgo.ApplicationCommand{Name: f.name}
  }

  func (f *fakeCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
      return nil
  }

  func TestRegisterAndAll(t *testing.T) {
      resetForTest()
      Register(&fakeCommand{name: "foo"})
      Register(&fakeCommand{name: "bar"})

      all := All()
      if len(all) != 2 {
          t.Fatalf("expected 2 registered commands, got %d", len(all))
      }
      names := map[string]bool{}
      for _, c := range all {
          names[c.Definition().Name] = true
      }
      if !names["foo"] || !names["bar"] {
          t.Fatalf("expected foo and bar registered, got %v", names)
      }
  }
  ```
- [ ] Run `go test ./internal/commands/...` — expect a compile failure (`registered`/`Register`/`All`/`resetForTest` undefined), confirming the test fails before implementation exists.
- [ ] Implement `C:\Users\<user>\Documents\todayistodayBot\internal\commands\registry.go`:
  ```go
  package commands

  import "github.com/bwmarrin/discordgo"

  // Command is the contract every internal/commands/*.go file implements and
  // self-registers via init(). main.go only depends on this interface and on
  // All() — it never imports individual command types.
  type Command interface {
      Definition() *discordgo.ApplicationCommand
      Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error
  }

  var registered []Command

  // Register adds cmd to the set dispatched by main.go. Call this from an
  // init() function in the file that defines the command — see ping.go for
  // the canonical example. Do not call Register from anywhere except an
  // init() in this package; main.go must not need to change when a command
  // is added.
  func Register(cmd Command) {
      registered = append(registered, cmd)
  }

  // All returns every self-registered command, in registration order.
  func All() []Command {
      return registered
  }

  // resetForTest clears the registry; test-only helper so each test starts
  // from a clean slate despite the package-level var being shared across the
  // init()-registered production commands in the same test binary.
  func resetForTest() {
      registered = nil
  }
  ```
- [ ] Run `go test ./internal/commands/...` — expect `ok` / `PASS`.
- [ ] Run `go vet ./...` — expect no output (clean).
- [ ] Commit: `git add go.mod go.sum internal/commands/registry.go internal/commands/registry_test.go` then commit with Japanese message, e.g. `feat: Goコマンドレジストリの自己登録パターンを追加`.

**Type consistency note carried through the rest of this plan:** every command file below implements exactly this `Command` interface — method names `Definition() *discordgo.ApplicationCommand` and `Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error`, matching the spec (`Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md:58-61`) verbatim. Do not rename these methods in any individual command task.

### Task 3 — `internal/config` loader (env-first, file-fallback, fail-fast)

Current `.NET` config shape (`TodayIsTodayBot/appsettings.json.template:1-8`, read during investigation):
```json
{
  "Discord": { "BotToken": "..." },
  "Bot": { "FrameRate": 60 }
}
```
`Bot:FrameRate` drove the FPS main loop (`TodayIsTodayBot/Program.cs:59,154-186`), which the spec explicitly retires (`Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md:23-25`) — Phase A's config therefore only needs the Discord token, not a frame rate. The Go config file fallback format drops `Bot:FrameRate` entirely.

- [ ] Write failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\config\config_test.go`:
  ```go
  package config

  import (
      "os"
      "path/filepath"
      "testing"
  )

  func TestLoad_FromEnv(t *testing.T) {
      t.Setenv("DISCORD_TOKEN", "env-token-123")
      cfg, err := Load()
      if err != nil {
          t.Fatalf("Load() returned error: %v", err)
      }
      if cfg.DiscordToken != "env-token-123" {
          t.Fatalf("expected token from env, got %q", cfg.DiscordToken)
      }
  }

  func TestLoad_FromFileFallback(t *testing.T) {
      dir := t.TempDir()
      path := filepath.Join(dir, "config.json")
      if err := os.WriteFile(path, []byte(`{"discord_token":"file-token-456"}`), 0o600); err != nil {
          t.Fatalf("failed to write test config file: %v", err)
      }
      t.Setenv("CONFIG_PATH", path)

      cfg, err := Load()
      if err != nil {
          t.Fatalf("Load() returned error: %v", err)
      }
      if cfg.DiscordToken != "file-token-456" {
          t.Fatalf("expected token from file, got %q", cfg.DiscordToken)
      }
  }

  func TestLoad_FailFastWhenNeitherSet(t *testing.T) {
      dir := t.TempDir()
      t.Setenv("CONFIG_PATH", filepath.Join(dir, "does-not-exist.json"))

      _, err := Load()
      if err == nil {
          t.Fatal("expected Load() to return an error when no token is configured, got nil")
      }
  }
  ```
- [ ] Run `go test ./internal/config/...` — expect compile failure (`Load`, `Config`, `DiscordToken` undefined), confirming red state.
- [ ] Implement `C:\Users\<user>\Documents\todayistodayBot\internal\config\config.go`:
  ```go
  package config

  import (
      "encoding/json"
      "fmt"
      "os"
  )

  // Config holds the runtime configuration for the bot. Phase A only needs
  // the Discord bot token — the .NET version's Bot:FrameRate setting drove a
  // hand-rolled FPS main loop that this Go rewrite removes entirely (see
  // Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md).
  type Config struct {
      DiscordToken string `json:"discord_token"`
  }

  const defaultConfigPath = "./config.json"

  // Load resolves configuration with environment variables taking priority
  // over a fallback config file, and fails fast (non-nil error) if neither
  // source provides a Discord token.
  //
  // Resolution order:
  //  1. DISCORD_TOKEN environment variable.
  //  2. JSON file at CONFIG_PATH (default "./config.json") with a
  //     "discord_token" field.
  //  3. Neither present => error.
  func Load() (*Config, error) {
      if token := os.Getenv("DISCORD_TOKEN"); token != "" {
          return &Config{DiscordToken: token}, nil
      }

      path := os.Getenv("CONFIG_PATH")
      if path == "" {
          path = defaultConfigPath
      }

      data, err := os.ReadFile(path)
      if err != nil {
          return nil, fmt.Errorf("config: DISCORD_TOKEN not set and config file %q not readable: %w", path, err)
      }

      var cfg Config
      if err := json.Unmarshal(data, &cfg); err != nil {
          return nil, fmt.Errorf("config: failed to parse %q: %w", path, err)
      }

      if cfg.DiscordToken == "" {
          return nil, fmt.Errorf("config: %q did not contain a non-empty discord_token", path)
      }

      return &cfg, nil
  }
  ```
- [ ] Run `go test ./internal/config/...` — expect `ok` / `PASS` for all 3 cases.
- [ ] Run `go vet ./...` — expect clean.
- [ ] Commit: `feat: env優先・ファイルfallbackの設定ローダーを追加`.

### Task 4 — `config.json.template` (replaces `appsettings.json.template`)

- [ ] Create `C:\Users\<user>\Documents\todayistodayBot\config.json.template`:
  ```json
  {
    "discord_token": "ここにDiscordボットトークンを入力してください"
  }
  ```
- [ ] Add `config.json` to `.gitignore` (the actual runtime file with the real token) if not already covered — check current `.gitignore` (`C:\Users\<user>\Documents\todayistodayBot\.gitignore`) which already ignores `config.json` (confirmed present, line present in the "Discord Bot specific" section read during investigation) — no change needed here, just confirm during implementation that the entry still matches `config.json` exactly (it does).
- [ ] No automated test for this (it's a template file) — verification is visual: `cat config.json.template` shows the placeholder JSON shown above.
- [ ] Commit alongside Task 3 (same logical change: config loading).

### Task 5 — `ping` command

Current behavior to preserve (read from `TodayIsTodayBot/Commands/Handlers/PingCommand.cs:15-18`): replies with the literal text `🏓 Pong!`. No arguments, no error paths.

- [ ] Write failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\ping_test.go`:
  ```go
  package commands

  import "testing"

  func TestPingCommand_Definition(t *testing.T) {
      cmd := &PingCommand{}
      def := cmd.Definition()
      if def.Name != "ping" {
          t.Fatalf("expected command name 'ping', got %q", def.Name)
      }
      if def.Description == "" {
          t.Fatal("expected non-empty description")
      }
  }

  func TestPingCommand_ResponseText(t *testing.T) {
      cmd := &PingCommand{}
      if got := cmd.responseText(); got != "🏓 Pong!" {
          t.Fatalf("expected '🏓 Pong!', got %q", got)
      }
  }
  ```
- [ ] Run `go test ./internal/commands/... -run TestPingCommand` — expect compile failure (`PingCommand` undefined), confirming red.
- [ ] Implement `C:\Users\<user>\Documents\todayistodayBot\internal\commands\ping.go`:
  ```go
  package commands

  import "github.com/bwmarrin/discordgo"

  func init() { Register(&PingCommand{}) }

  // PingCommand replies with a fixed pong message to confirm the bot is
  // responding. Ported from TodayIsTodayBot/Commands/Handlers/PingCommand.cs.
  type PingCommand struct{}

  func (c *PingCommand) Definition() *discordgo.ApplicationCommand {
      return &discordgo.ApplicationCommand{
          Name:        "ping",
          Description: "ボットが応答しているか確認します",
      }
  }

  func (c *PingCommand) responseText() string {
      return "🏓 Pong!"
  }

  func (c *PingCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
      return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
          Type: discordgo.InteractionResponseChannelMessageWithSource,
          Data: &discordgo.InteractionResponseData{
              Content: c.responseText(),
          },
      })
  }
  ```
- [ ] Run `go test ./internal/commands/... -run TestPingCommand` — expect `PASS`.
- [ ] Run `go vet ./...` — expect clean.
- [ ] Commit: `feat: pingコマンドをGo/discordgoへ移植`.

### Task 6 — `help` command

Current behavior (`TodayIsTodayBot/Commands/Handlers/HelpCommand.cs:22-40`): lists all registered commands sorted alphabetically by name, formatted as:
```
**📋 利用可能なコマンド一覧**

`/<name>` - <description>
`/<name2>` - <description2>
...
```
If zero commands registered, replies `現在、利用可能なコマンドはありません。` (this branch is now effectively dead code in Go since `help` itself is always registered, but the plan preserves the guard for behavioral parity and defensive coding).

- [ ] Write failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\help_test.go`:
  ```go
  package commands

  import (
      "strings"
      "testing"

      "github.com/bwmarrin/discordgo"
  )

  func TestFormatHelpText_ListsCommandsSortedByName(t *testing.T) {
      defs := []*discordgo.ApplicationCommand{
          {Name: "weather", Description: "天気情報を取得します"},
          {Name: "dice", Description: "サイコロを振ります"},
      }

      got := formatHelpText(defs)

      wantOrder := []string{"`/dice` - サイコロを振ります", "`/weather` - 天気情報を取得します"}
      diceIdx := strings.Index(got, wantOrder[0])
      weatherIdx := strings.Index(got, wantOrder[1])
      if diceIdx == -1 || weatherIdx == -1 {
          t.Fatalf("expected both command lines present, got: %q", got)
      }
      if diceIdx > weatherIdx {
          t.Fatalf("expected dice before weather (alphabetical), got: %q", got)
      }
      if !strings.HasPrefix(got, "**📋 利用可能なコマンド一覧**") {
          t.Fatalf("expected header prefix, got: %q", got)
      }
  }

  func TestFormatHelpText_Empty(t *testing.T) {
      got := formatHelpText(nil)
      if got != "現在、利用可能なコマンドはありません。" {
          t.Fatalf("expected empty-list message, got: %q", got)
      }
  }
  ```
- [ ] Run `go test ./internal/commands/... -run TestFormatHelpText` — expect compile failure (`formatHelpText` undefined), confirming red.
- [ ] Implement `C:\Users\<user>\Documents\todayistodayBot\internal\commands\help.go`:
  ```go
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

      return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
          Type: discordgo.InteractionResponseChannelMessageWithSource,
          Data: &discordgo.InteractionResponseData{
              Content: formatHelpText(defs),
          },
      })
  }
  ```
- [ ] Run `go test ./internal/commands/... -run TestFormatHelpText` — expect `PASS`.
- [ ] Run `go vet ./...` — expect clean.
- [ ] Commit: `feat: helpコマンドをGo/discordgoへ移植`.

### Task 7 — `internal/weather` HTTP client (Open-Meteo)

Exact current behavior read from `TodayIsTodayBot/Services/WeatherService.cs`:
- Endpoint (line 12): `https://api.open-meteo.com/v1/forecast`
- Query built at lines 211-213: `?latitude={lat}&longitude={lon}&current=temperature_2m,relative_humidity_2m,apparent_temperature,weather_code,wind_speed_10m&timezone={timezone}`
- City table (lines 22-191): a large `Dictionary<string,(double Lat, double Lon, string Timezone)>` keyed case-insensitively (`StringComparer.OrdinalIgnoreCase`), covering 47 Japanese prefectures/major cities (each with a Japanese-name key and an English-name key both mapping to the same coordinates) plus ~20 major US cities. Every entry must be ported verbatim (same lat/lon, same timezone strings, same duplicate-key aliasing e.g. `"東京"` and `"Tokyo"` both -> `(35.6895, 139.6917, "Asia/Tokyo")`).
- Response shape (lines 318-340): `OpenMeteoResponse.Current` with fields `temperature_2m` (float), `apparent_temperature` (float), `relative_humidity_2m` (int), `weather_code` (int), `wind_speed_10m` (float).
- Weather code -> Japanese description mapping (lines 264-285) and -> emoji mapping (lines 290-304) — exact `switch` tables to port 1:1.
- Formatted message (lines 246-259):
  ```
  **{icon} {cityName}の天気**

  🌡️ **気温**: {temp:F1}°C (体感: {apparentTemp:F1}°C)
  📊 **状態**: {description}
  💧 **湿度**: {humidity}%
  💨 **風速**: {windSpeed:F1} m/s
  ```
  (`:F1` = fixed-point, 1 decimal place.)
- Unknown city -> (lines 202-206): `❌ 都市「{cityName}」が見つかりませんでした。都道府県名または主要都市名を指定してください。\n\n` + `GetAvailableCities()`.
- `GetAvailableCities()` (lines 309-314) returns:
  ```
  **利用可能な地域**:
  🇯🇵 **日本**: 東京, 大阪, 京都, 名古屋, 札幌, 福岡, 仙台, 広島, 神戸, 横浜, 沖縄 など
  🇺🇸 **アメリカ**: New York, Los Angeles, Chicago, San Francisco, Seattle, Las Vegas, Miami, Honolulu など
  ```
- Non-2xx HTTP status -> `❌ 天気情報の取得に失敗しました。(ステータスコード: {status})`.
- Network/deserialization errors -> `❌ 天気情報の取得中にネットワークエラーが発生しました: {msg}` / `❌ 天気情報の解析に失敗しました。` / generic `❌ 天気情報の取得中にエラーが発生しました: {msg}`.

- [ ] Write failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\weather\client_test.go`:
  ```go
  package weather

  import (
      "context"
      "encoding/json"
      "net/http"
      "net/http/httptest"
      "strings"
      "testing"
  )

  func TestGetWeather_KnownCity_FormatsMessage(t *testing.T) {
      server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          resp := openMeteoResponse{
              Current: &currentWeather{
                  Temperature:         21.4,
                  ApparentTemperature: 20.1,
                  RelativeHumidity:    55,
                  WeatherCode:         0,
                  WindSpeed:           3.2,
              },
          }
          w.Header().Set("Content-Type", "application/json")
          _ = json.NewEncoder(w).Encode(resp)
      }))
      defer server.Close()

      client := NewClient(server.Client())
      client.baseURL = server.URL

      got, err := client.GetWeather(context.Background(), "東京")
      if err != nil {
          t.Fatalf("GetWeather returned error: %v", err)
      }
      if !strings.Contains(got, "東京の天気") {
          t.Fatalf("expected city name in message, got: %q", got)
      }
      if !strings.Contains(got, "21.4") || !strings.Contains(got, "20.1") {
          t.Fatalf("expected temperature values in message, got: %q", got)
      }
      if !strings.Contains(got, "快晴") {
          t.Fatalf("expected weather_code 0 to map to 快晴, got: %q", got)
      }
  }

  func TestGetWeather_UnknownCity_ReturnsErrorMessage(t *testing.T) {
      client := NewClient(http.DefaultClient)

      got, err := client.GetWeather(context.Background(), "存在しない場所999")
      if err != nil {
          t.Fatalf("GetWeather should not return a Go error for unknown city, got: %v", err)
      }
      if !strings.Contains(got, "見つかりませんでした") {
          t.Fatalf("expected not-found message, got: %q", got)
      }
  }

  func TestGetWeather_NonOKStatus_ReturnsErrorMessage(t *testing.T) {
      server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          w.WriteHeader(http.StatusInternalServerError)
      }))
      defer server.Close()

      client := NewClient(server.Client())
      client.baseURL = server.URL

      got, err := client.GetWeather(context.Background(), "大阪")
      if err != nil {
          t.Fatalf("GetWeather should not return a Go error on non-200, got: %v", err)
      }
      if !strings.Contains(got, "取得に失敗しました") {
          t.Fatalf("expected failure message, got: %q", got)
      }
  }

  func TestGetWeather_MissingCurrentKey_ReturnsParseErrorMessage(t *testing.T) {
      server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          // Deliberately omit "current" entirely (not just zero-valued) to
          // reproduce WeatherService.cs:225-227's `weatherData?.Current == null`
          // guard path — a missing key must not silently format as 0°C/快晴.
          w.Header().Set("Content-Type", "application/json")
          _, _ = w.Write([]byte(`{}`))
      }))
      defer server.Close()

      client := NewClient(server.Client())
      client.baseURL = server.URL

      got, err := client.GetWeather(context.Background(), "東京")
      if err != nil {
          t.Fatalf("GetWeather should not return a Go error for missing current key, got: %v", err)
      }
      if got != "❌ 天気情報の解析に失敗しました。" {
          t.Fatalf("expected exact parse-failure message for missing current key, got: %q", got)
      }
  }

  func TestLookupCity_KnownCoordinates(t *testing.T) {
      // Table-driven spot-check against the ported city table (WeatherService.cs:25-190)
      // to catch transcription errors (wrong lat/lon/timezone) that the
      // message-formatting tests above would not detect.
      cases := []struct {
          name    string
          wantLat float64
          wantLon float64
          wantTZ  string
      }{
          {"東京", 35.6895, 139.6917, "Asia/Tokyo"},
          {"Tokyo", 35.6895, 139.6917, "Asia/Tokyo"},
          {"大阪", 34.6937, 135.5023, "Asia/Tokyo"},
          {"沖縄", 26.2124, 127.6809, "Asia/Tokyo"},
          {"New York", 40.7128, -74.0060, "America/New_York"},
      }

      for _, tc := range cases {
          t.Run(tc.name, func(t *testing.T) {
              loc, ok := lookupCity(tc.name)
              if !ok {
                  t.Fatalf("expected %q to be found in city table", tc.name)
              }
              if loc.Lat != tc.wantLat || loc.Lon != tc.wantLon {
                  t.Fatalf("city %q: expected (%g, %g), got (%g, %g)", tc.name, tc.wantLat, tc.wantLon, loc.Lat, loc.Lon)
              }
              if loc.Timezone != tc.wantTZ {
                  t.Fatalf("city %q: expected timezone %q, got %q", tc.name, tc.wantTZ, loc.Timezone)
              }
          })
      }
  }

  func TestGetWeather_RequestURL_ContainsExpectedQueryValues(t *testing.T) {
      // Verifies GetWeather actually builds a request URL carrying the looked-up
      // city's coordinates/timezone, catching a transcription or wiring error
      // between the city table and the outgoing request (not just the table
      // itself, which TestLookupCity_KnownCoordinates already covers).
      cases := []struct {
          city    string
          wantLat string
          wantLon string
          wantTZ  string
      }{
          {"東京", "35.6895", "139.6917", "Asia/Tokyo"},
          {"Osaka", "34.6937", "135.5023", "Asia/Tokyo"},
          {"福岡", "33.5904", "130.4017", "Asia/Tokyo"},
          {"San Francisco", "37.7749", "-122.4194", "America/Los_Angeles"},
      }

      for _, tc := range cases {
          t.Run(tc.city, func(t *testing.T) {
              var gotURL string
              server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                  gotURL = r.URL.String()
                  w.Header().Set("Content-Type", "application/json")
                  _ = json.NewEncoder(w).Encode(openMeteoResponse{Current: &currentWeather{}})
              }))
              defer server.Close()

              client := NewClient(server.Client())
              client.baseURL = server.URL

              if _, err := client.GetWeather(context.Background(), tc.city); err != nil {
                  t.Fatalf("GetWeather returned error: %v", err)
              }

              for _, want := range []string{
                  "latitude=" + tc.wantLat,
                  "longitude=" + tc.wantLon,
                  "timezone=" + tc.wantTZ,
              } {
                  if !strings.Contains(gotURL, want) {
                      t.Fatalf("city %q: expected request URL to contain %q, got: %q", tc.city, want, gotURL)
                  }
              }
          })
      }
  }
  ```
  (Note: `TestGetWeather_RequestURL_ContainsExpectedQueryValues` reuses the `latitude`/`longitude` format Go's `%g` verb produces in the URL built by `GetWeather` — the implementer should confirm at implementation time that `%g` renders e.g. `139.6917` without scientific notation or trailing-zero differences for all table entries covered here, or switch the URL-building `%g` to an explicit format if any coordinate's `%g` rendering surprises the test.)
- [ ] Run `go test ./internal/weather/...` — expect compile failure (package/types undefined), confirming red.
- [ ] Implement `C:\Users\<user>\Documents\todayistodayBot\internal\weather\cities.go` (port the full city table from `WeatherService.cs:22-191` verbatim — same coordinates, same timezone strings, same dual JP/EN keys, lookup case-insensitive):
  ```go
  package weather

  import "strings"

  type cityLocation struct {
      Lat      float64
      Lon      float64
      Timezone string
  }

  // cityMapping mirrors TodayIsTodayBot/Services/WeatherService.cs's
  // _cityMapping dictionary verbatim (same coordinates/timezones, same
  // Japanese+English dual-key aliasing). Lookups are case-insensitive (see
  // lookupCity below), matching the C# StringComparer.OrdinalIgnoreCase.
  var cityMapping = map[string]cityLocation{
      "東京": {35.6895, 139.6917, "Asia/Tokyo"},
      "Tokyo": {35.6895, 139.6917, "Asia/Tokyo"},
      "大阪": {34.6937, 135.5023, "Asia/Tokyo"},
      "Osaka": {34.6937, 135.5023, "Asia/Tokyo"},
      // ... every remaining entry from WeatherService.cs:25-190 ported 1:1,
      // including all 47 prefectures/cities and ~20 US cities. Do not
      // summarize or drop entries — this is a direct data port. The
      // implementer must open WeatherService.cs side-by-side and transcribe
      // every remaining map entry before this file is considered complete.
  }

  func lookupCity(name string) (cityLocation, bool) {
      for k, v := range cityMapping {
          if strings.EqualFold(k, name) {
              return v, true
          }
      }
      return cityLocation{}, false
  }
  ```
  **Implementer note:** the case-insensitive lookup above is O(n) per call by design-simplicity; if this is flagged in review as a performance concern, an acceptable alternative is building a lowercased index map once at package init — either is fine functionally, but the linear scan is sufficient here (dozens of entries, called per Discord interaction, not a hot path). Do not introduce a persistence layer or external dependency to solve this. **This task is not complete until every city entry from `WeatherService.cs` has been transcribed** — the plan shows only 2 entries as an illustration of the format; treat the full transcription as part of this task's definition of done, not optional follow-up.
- [ ] Implement `C:\Users\<user>\Documents\todayistodayBot\internal\weather\client.go`:
  ```go
  package weather

  import (
      "context"
      "encoding/json"
      "fmt"
      "net/http"
      "strings"
  )

  const defaultBaseURL = "https://api.open-meteo.com/v1/forecast"

  type openMeteoResponse struct {
      // Pointer so a missing/null "current" key in the response JSON is
      // distinguishable from a present-but-zero-valued object (see the nil
      // check in GetWeather below, matching WeatherService.cs:225-227).
      Current *currentWeather `json:"current"`
  }

  type currentWeather struct {
      Temperature         float64 `json:"temperature_2m"`
      ApparentTemperature float64 `json:"apparent_temperature"`
      RelativeHumidity    int     `json:"relative_humidity_2m"`
      WeatherCode         int     `json:"weather_code"`
      WindSpeed           float64 `json:"wind_speed_10m"`
  }

  // Client fetches and formats weather info from Open-Meteo. Ported from
  // TodayIsTodayBot/Services/WeatherService.cs.
  type Client struct {
      httpClient *http.Client
      baseURL    string // overridable in tests; defaults to defaultBaseURL
  }

  func NewClient(httpClient *http.Client) *Client {
      return &Client{httpClient: httpClient, baseURL: defaultBaseURL}
  }

  // GetWeather returns a user-facing formatted message (success or a
  // Japanese error message) for cityName. It only returns a non-nil error
  // for programmer errors (e.g. malformed request construction) — network
  // and API failures are surfaced as formatted error strings, matching the
  // .NET WeatherService.GetWeatherAsync behavior of never throwing to the
  // caller for expected failure modes.
  func (c *Client) GetWeather(ctx context.Context, cityName string) (string, error) {
      loc, ok := lookupCity(cityName)
      if !ok {
          return fmt.Sprintf("❌ 都市「%s」が見つかりませんでした。都道府県名または主要都市名を指定してください。\n\n%s",
              cityName, AvailableCities()), nil
      }

      url := fmt.Sprintf("%s?latitude=%g&longitude=%g&current=temperature_2m,relative_humidity_2m,apparent_temperature,weather_code,wind_speed_10m&timezone=%s",
          c.baseURL, loc.Lat, loc.Lon, loc.Timezone)

      req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
      if err != nil {
          return "", fmt.Errorf("weather: building request: %w", err)
      }

      resp, err := c.httpClient.Do(req)
      if err != nil {
          return fmt.Sprintf("❌ 天気情報の取得中にネットワークエラーが発生しました: %s", err.Error()), nil
      }
      defer resp.Body.Close()

      if resp.StatusCode < 200 || resp.StatusCode >= 300 {
          // NOTE (deliberate, acknowledged wording difference — not a silent
          // regression): the .NET source interpolates .NET's HttpStatusCode
          // enum name here (e.g. "InternalServerError"), not a bare number —
          // see WeatherService.cs:219 `{response.StatusCode}`. Go's
          // http.StatusText() doesn't produce matching no-space PascalCase
          // names either, and hand-rolling a name table for this rare,
          // low-stakes error-wording path isn't worth the maintenance cost.
          // This Go port intentionally uses the numeric HTTP status code
          // instead (e.g. "500") in this one message.
          return fmt.Sprintf("❌ 天気情報の取得に失敗しました。(ステータスコード: %d)", resp.StatusCode), nil
      }

      var parsed openMeteoResponse
      if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
          return "❌ 天気情報の解析に失敗しました。", nil
      }

      // Matches WeatherService.cs:225-227's `if (weatherData?.Current == null)
      // return "❌ 天気情報の解析に失敗しました。";` guard — Current is a pointer
      // here specifically so a missing/null "current" key in the response JSON
      // is distinguishable from a present-but-zero-valued object. Without the
      // pointer, a missing key would silently format as 0°C/快晴 instead of
      // surfacing this error.
      if parsed.Current == nil {
          return "❌ 天気情報の解析に失敗しました。", nil
      }

      return formatWeatherMessage(*parsed.Current, cityName), nil
  }

  func formatWeatherMessage(cur currentWeather, requestedCity string) string {
      icon := weatherIcon(cur.WeatherCode)
      desc := weatherDescription(cur.WeatherCode)

      var b strings.Builder
      fmt.Fprintf(&b, "**%s %sの天気**\n\n", icon, requestedCity)
      fmt.Fprintf(&b, "🌡️ **気温**: %.1f°C (体感: %.1f°C)\n", cur.Temperature, cur.ApparentTemperature)
      fmt.Fprintf(&b, "📊 **状態**: %s\n", desc)
      fmt.Fprintf(&b, "💧 **湿度**: %d%%\n", cur.RelativeHumidity)
      fmt.Fprintf(&b, "💨 **風速**: %.1f m/s\n", cur.WindSpeed)
      return b.String()
  }

  func weatherDescription(code int) string {
      switch code {
      case 0:
          return "快晴"
      case 1:
          return "ほぼ晴れ"
      case 2:
          return "部分的に曇り"
      case 3:
          return "曇り"
      case 45, 48:
          return "霧"
      case 51, 53, 55:
          return "霧雨"
      case 56, 57:
          return "凍る霧雨"
      case 61, 63, 65:
          return "雨"
      case 66, 67:
          return "凍る雨"
      case 71, 73, 75:
          return "雪"
      case 77:
          return "みぞれ"
      case 80, 81, 82:
          return "にわか雨"
      case 85, 86:
          return "にわか雪"
      case 95:
          return "雷雨"
      case 96, 99:
          return "雹を伴う雷雨"
      default:
          return "不明"
      }
  }

  func weatherIcon(code int) string {
      switch code {
      case 0:
          return "☀️"
      case 1, 2:
          return "🌤️"
      case 3:
          return "☁️"
      case 45, 48:
          return "🌫️"
      case 51, 53, 55, 56, 57:
          return "🌦️"
      case 61, 63, 65, 66, 67, 80, 81, 82:
          return "🌧️"
      case 71, 73, 75, 77, 85, 86:
          return "❄️"
      case 95, 96, 99:
          return "⛈️"
      default:
          return "🌤️"
      }
  }

  // AvailableCities returns the same "available regions" summary as
  // TodayIsTodayBot/Services/WeatherService.cs's GetAvailableCities().
  func AvailableCities() string {
      return "**利用可能な地域**:\n" +
          "🇯🇵 **日本**: 東京, 大阪, 京都, 名古屋, 札幌, 福岡, 仙台, 広島, 神戸, 横浜, 沖縄 など\n" +
          "🇺🇸 **アメリカ**: New York, Los Angeles, Chicago, San Francisco, Seattle, Las Vegas, Miami, Honolulu など"
  }
  ```
- [ ] Run `go test ./internal/weather/...` — expect `PASS` for all cases (known city formats correctly with `快晴` for code 0, unknown city returns not-found message, non-200 returns failure message, missing `current` key returns the exact parse-failure message, city-table and request-URL spot-checks pass for the representative cities listed).
- [ ] Run `go vet ./...` — expect clean.
- [ ] Commit: `feat: Open-Meteo天気クライアントをGoへ移植`.

### Task 8 — `weather` command handler

Current behavior (`TodayIsTodayBot/Commands/Handlers/WeatherCommand.cs:21-67`): no-args -> help message with usage examples + `GetAvailableCities()`; with args, joins them with spaces as the city name, sends a "取得中" placeholder then edits/follows up with the result (in the .NET version this was two separate messages — processing message deleted, then result sent as a new message; in Discord's Interactions API the idiomatic equivalent is a deferred response followed by a followup/edit, but a single synchronous response is also acceptable since Open-Meteo calls are fast — this plan chooses the simpler single synchronous response to avoid extra defer/edit complexity not required by the spec).

- [ ] Write failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\weather_test.go`:
  ```go
  package commands

  import (
      "strings"
      "testing"
  )

  func TestWeatherCommand_NoArgs_ReturnsHelpText(t *testing.T) {
      cmd := &WeatherCommand{}
      got := cmd.helpText()
      if !strings.Contains(got, "天気コマンドの使い方") {
          t.Fatalf("expected usage help text, got: %q", got)
      }
      if !strings.Contains(got, "/weather 東京") {
          t.Fatalf("expected usage example, got: %q", got)
      }
  }

  func TestWeatherCommand_Definition(t *testing.T) {
      cmd := &WeatherCommand{}
      def := cmd.Definition()
      if def.Name != "weather" {
          t.Fatalf("expected command name 'weather', got %q", def.Name)
      }
      if len(def.Options) != 1 {
          t.Fatalf("expected exactly 1 option (city), got %d", len(def.Options))
      }
      if def.Options[0].Required {
          t.Fatal("expected city option to be optional, to allow the no-args help-text path")
      }
  }
  ```
- [ ] Run `go test ./internal/commands/... -run TestWeatherCommand` — expect compile failure, confirming red.
- [ ] Implement `C:\Users\<user>\Documents\todayistodayBot\internal\commands\weather.go`:
  ```go
  package commands

  import (
      "context"
      "fmt"
      "net/http"

      "github.com/SioKo-Shox3/todayistodayBot/internal/weather"
      "github.com/bwmarrin/discordgo"
  )

  func init() { Register(&WeatherCommand{weatherClient: weather.NewClient(http.DefaultClient)}) }

  // WeatherCommand fetches weather via Open-Meteo. Ported from
  // TodayIsTodayBot/Commands/Handlers/WeatherCommand.cs; the city option
  // replaces the old space-joined free-text args.
  type WeatherCommand struct {
      weatherClient *weather.Client
  }

  func (c *WeatherCommand) Definition() *discordgo.ApplicationCommand {
      return &discordgo.ApplicationCommand{
          Name: "weather",
          // Full-width parens「（…）」— matches WeatherCommand.cs:19 exactly.
          Description: "指定した地域の天気情報を取得します（日本語・英語対応、例: /weather 東京 または /weather Tokyo）",
          Options: []*discordgo.ApplicationCommandOption{
              {
                  Type: discordgo.ApplicationCommandOptionString,
                  Name: "city",
                  // No direct .NET source string for this option's description
                  // (new Discord slash-command UI copy) — full-width parens
                  // chosen for internal consistency with the rest of the
                  // command's user-facing text, not a ported string.
                  Description: "都市名（日本語または英語、例: 東京、Tokyo）",
                  Required:    false,
              },
          },
      }
  }

  func (c *WeatherCommand) helpText() string {
      return "**🌤️ 天気コマンドの使い方**\n\n" +
          "**使用例**:\n" +
          "`/weather 東京` - 東京の天気を取得\n" +
          "`/weather 大阪` - 大阪の天気を取得\n" +
          "`/weather Tokyo` - 英語の都市名でも可\n\n" +
          weather.AvailableCities()
  }

  // buildResponseContent returns the message text Handle sends back for the
  // given city argument (empty string = no city option supplied). Extracted
  // as a pure function — decoupled from *discordgo.Session/InteractionCreate —
  // so tests can assert on the exact response content without needing to
  // intercept discordgo's InteractionRespond HTTP call.
  func (c *WeatherCommand) buildResponseContent(cityName string) string {
      if cityName == "" {
          return c.helpText()
      }

      result, err := c.weatherClient.GetWeather(context.Background(), cityName)
      if err != nil {
          return fmt.Sprintf("❌ 天気情報の取得中にエラーが発生しました: %s", err.Error())
      }
      return result
  }

  func (c *WeatherCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
      var cityName string
      for _, opt := range i.ApplicationCommandData().Options {
          if opt.Name == "city" {
              cityName = opt.StringValue()
          }
      }

      return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
          Type: discordgo.InteractionResponseChannelMessageWithSource,
          Data: &discordgo.InteractionResponseData{Content: c.buildResponseContent(cityName)},
      })
  }
  ```
  (`context.Background()` is used directly since Phase A has no cancellation requirement and Open-Meteo calls are short-lived; add the `"context"` import as shown.)
- [ ] Add this test to `internal/commands/weather_test.go` (exercises the actual `Handle()`-feeding no-city branch via the extracted pure function, not just `helpText()`/`Definition()` in isolation — closes the gap where nothing previously called the code path that decides "no city → help text"):
  ```go
  func TestWeatherCommand_BuildResponseContent_NoCityArg_ReturnsHelpText(t *testing.T) {
      cmd := &WeatherCommand{}
      got := cmd.buildResponseContent("")
      if got != cmd.helpText() {
          t.Fatalf("expected buildResponseContent(\"\") to equal helpText(), got: %q", got)
      }
  }
  ```
- [ ] Run `go test ./internal/commands/... -run TestWeatherCommand` — expect `PASS`.
- [ ] Run `go vet ./...` and `go build ./...` — expect clean (this task introduces the first cross-package import; confirm the module path chosen in Task 1 resolves correctly here).
- [ ] Commit: `feat: weatherコマンドをGo/discordgoへ移植`.

### Task 9 — `internal/today` HTTP client (WhatIsToday API)

Exact current behavior read from `TodayIsTodayBot/Services/TodayService.cs`:
- Three endpoints (lines 12-14), each suffixed with `MMdd`:
  - `https://api.whatistoday.cyou/v3/anniv/{mmdd}` — anniversary/記念日
  - `https://api.whatistoday.cyou/v3/birthflower/{mmdd}` — 誕生花
  - `https://api.whatistoday.cyou/v3/famousbirthday/{mmdd}` — 偉人誕生日
- All three fetched concurrently (lines 110-124, `Task.WhenAll`), with per-call errors swallowed at the aggregate level (individual nil results are then treated as "no data" rather than aborting the whole command).
- Response shapes (lines 195-256):
  - `AnniversaryResponse`: `id` (int), `mmdd` (string), `anniv1`..`anniv5` (string, nullable) — up to 5 anniversary strings.
  - `BirthflowerResponse`: `id`, `mmdd`, `flower` (string), `lang` (string, the flower's meaning/"花言葉").
  - `FamousBirthdayResponse`: `id`, `mmdd`, `lifespan` (string), `name` (string), `profile` (string).
  - JSON deserialization uses `PropertyNameCaseInsensitive = true` in .NET (lines 36-39 etc.) — the Go port should use lowercase JSON tags matching the actual field names (`id`, `mmdd`, `anniv1`, `flower`, `lang`, `lifespan`, `name`, `profile`) since Go's `encoding/json` is case-insensitive on unmarshal by default for matching tags, achieving equivalent behavior.
- Combined formatted output (lines 105-189), for a `dateStr` like `7月5日`:
  ```
  📅 **{dateStr}は何の日？**

  🎉 **記念日**
    • {anniv1}
    • {anniv2}
    ... (only non-empty anniv1..anniv5, each its own bullet line; if all empty: "  （情報なし）\n" — full-width parens)

  🌸 **誕生花**
    • {flower}（花言葉: {lang}）    <- "（花言葉: ...）" segment (full-width parens) omitted if lang is empty; whole 誕生花 block omitted if flower is empty/response nil

  👤 **この日生まれの偉人**
    • {name}（{profile}）[{lifespan}]   <- profile segment "（{profile}）" (full-width parens) omitted if empty, lifespan segment "[{lifespan}]" omitted if empty; whole block omitted if name is empty/response nil

  _Powered by [whatistoday API](https://note.com/sooz/n/naffb68c7f53b)_
  ```
  Exact line/blank-line placement per `TodayService.cs:130-188` — note the anniversary block always ends with a blank line after itself (line 155 `result += "\n"`), the flower block ends with `"\n\n"` (line 167) only if a flower was found, the famous-birthday block ends with just `"\n"` (line 183), and the credit line is always appended last preceded by a blank line (line 186 `"\n_Powered by..."`). All parenthetical segments above use FULL-WIDTH `（）`/`？`, matching `TodayService.cs:130,153,165,177` exactly — do not substitute ASCII `()`/`?`.

  **Corrected failure-handling model (this supersedes an earlier draft of this plan that incorrectly claimed direct .NET parity here):** tracing `TodayService.cs`'s actual control flow — `GetAnniversaryAsync`/`GetBirthflowerAsync`/`GetFamousBirthdayAsync` each `throw;` internally on failure (lines 44,70,97). `GetTodayInfoAsync` wraps each in `.ContinueWith(t => (object?)t.Result)` (lines 112-114) — when the antecedent task is Faulted, evaluating `t.Result` inside the continuation throws, so the continuation task itself becomes Faulted too. `Task.WhenAll` then throws, swallowed by an empty `catch {}` (lines 117-124) — but the following lines (`tasks[0].Result as AnniversaryResponse` etc., lines 126-128) call `.Result` on the still-Faulted task AGAIN, which throws synchronously and is NOT caught anywhere inside `GetTodayInfoAsync`. It propagates out to `TodayCommand.ExecuteAsync`'s `catch (Exception ex)` (`TodayCommand.cs:71-83`), which discards the entire reply and substitutes one generic `❌ 情報の取得中にエラーが発生しました: {ex.Message}` message. **So the real .NET behavior is: any single sub-API failure (not just all three) aborts the whole `/today` reply with one generic error** — this reads as an unintentional side effect of mixing `.ContinueWith` with `.Result`, not a deliberate design choice. For this Go port, a partial/best-effort reply (omitting only the failed section) is strictly better UX for a low-stakes info command, so the Go implementation below INTENTIONALLY implements best-effort per-section omission instead of reproducing the all-or-nothing failure path above. This is a documented, deliberate behavior improvement for Phase A, not a faithful port of this specific failure path.

- [ ] Write failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\today\client_test.go`:
  ```go
  package today

  import (
      "context"
      "encoding/json"
      "net/http"
      "net/http/httptest"
      "strings"
      "testing"
      "time"
  )

  func mustParseDate(t *testing.T, s string) time.Time {
      t.Helper()
      d, err := time.Parse("2006-01-02", s)
      if err != nil {
          t.Fatalf("failed to parse test date %q: %v", s, err)
      }
      return d
  }

  func TestGetTodayInfo_AllThreeSucceed_FormatsAllSections(t *testing.T) {
      mux := http.NewServeMux()
      mux.HandleFunc("/v3/anniv/0705", func(w http.ResponseWriter, r *http.Request) {
          _ = json.NewEncoder(w).Encode(anniversaryResponse{Anniv1: "テスト記念日"})
      })
      mux.HandleFunc("/v3/birthflower/0705", func(w http.ResponseWriter, r *http.Request) {
          _ = json.NewEncoder(w).Encode(birthflowerResponse{Flower: "ひまわり", Lang: "憧れ"})
      })
      mux.HandleFunc("/v3/famousbirthday/0705", func(w http.ResponseWriter, r *http.Request) {
          _ = json.NewEncoder(w).Encode(famousBirthdayResponse{Name: "テスト偉人", Profile: "発明家", Lifespan: "1900-1980"})
      })
      server := httptest.NewServer(mux)
      defer server.Close()

      client := NewClient(server.Client())
      client.baseURL = server.URL

      date := mustParseDate(t, "2026-07-05")
      got := client.GetTodayInfo(context.Background(), date)

      for _, want := range []string{
          "7月5日は何の日？", // full-width "？", matches TodayService.cs:130
          "テスト記念日",
          "ひまわり（花言葉: 憧れ）", // full-width parens, matches TodayService.cs:165
          "テスト偉人（発明家）", // full-width parens, matches TodayService.cs:177
          "[1900-1980]",
          "Powered by",
      } {
          if !strings.Contains(got, want) {
              t.Fatalf("expected output to contain %q, got: %q", want, got)
          }
      }
  }

  func TestGetTodayInfo_AnniversaryEmpty_ShowsFullWidthNoInfoMessage(t *testing.T) {
      // Matches TodayService.cs:153's "  （情報なし）\n" — full-width parens,
      // shown when the anniversary sub-fetch succeeds but returns no anniv1..5.
      mux := http.NewServeMux()
      mux.HandleFunc("/v3/anniv/0705", func(w http.ResponseWriter, r *http.Request) {
          _ = json.NewEncoder(w).Encode(anniversaryResponse{})
      })
      mux.HandleFunc("/v3/birthflower/0705", func(w http.ResponseWriter, r *http.Request) {
          w.WriteHeader(http.StatusInternalServerError)
      })
      mux.HandleFunc("/v3/famousbirthday/0705", func(w http.ResponseWriter, r *http.Request) {
          w.WriteHeader(http.StatusInternalServerError)
      })
      server := httptest.NewServer(mux)
      defer server.Close()

      client := NewClient(server.Client())
      client.baseURL = server.URL

      date := mustParseDate(t, "2026-07-05")
      got := client.GetTodayInfo(context.Background(), date)

      if !strings.Contains(got, "  （情報なし）") {
          t.Fatalf("expected full-width empty-anniversary message, got: %q", got)
      }
  }

  func TestGetTodayInfo_AllFail_OmitsSectionsGracefully(t *testing.T) {
      server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          w.WriteHeader(http.StatusInternalServerError)
      }))
      defer server.Close()

      client := NewClient(server.Client())
      client.baseURL = server.URL

      date := mustParseDate(t, "2026-07-05")
      got := client.GetTodayInfo(context.Background(), date)

      if !strings.Contains(got, "7月5日は何の日？") {
          t.Fatalf("expected header even when all sub-fetches fail, got: %q", got)
      }
      if strings.Contains(got, "テスト記念日") {
          t.Fatal("did not expect anniversary content when fetch failed")
      }
  }
  ```
- [ ] Run `go test ./internal/today/...` — expect compile failure, confirming red.
- [ ] Implement `C:\Users\<user>\Documents\todayistodayBot\internal\today\client.go`:
  ```go
  package today

  import (
      "context"
      "encoding/json"
      "fmt"
      "net/http"
      "strings"
      "sync"
      "time"
  )

  const (
      defaultAnnivBaseURL          = "https://api.whatistoday.cyou/v3/anniv/"
      defaultBirthflowerBaseURL    = "https://api.whatistoday.cyou/v3/birthflower/"
      defaultFamousBirthdayBaseURL = "https://api.whatistoday.cyou/v3/famousbirthday/"
  )

  type anniversaryResponse struct {
      ID     int    `json:"id"`
      Mmdd   string `json:"mmdd"`
      Anniv1 string `json:"anniv1"`
      Anniv2 string `json:"anniv2"`
      Anniv3 string `json:"anniv3"`
      Anniv4 string `json:"anniv4"`
      Anniv5 string `json:"anniv5"`
  }

  type birthflowerResponse struct {
      ID     int    `json:"id"`
      Mmdd   string `json:"mmdd"`
      Flower string `json:"flower"`
      Lang   string `json:"lang"`
  }

  type famousBirthdayResponse struct {
      ID       int    `json:"id"`
      Mmdd     string `json:"mmdd"`
      Lifespan string `json:"lifespan"`
      Name     string `json:"name"`
      Profile  string `json:"profile"`
  }

  // Client fetches "what day is today" info (anniversaries, birth flower,
  // famous birthdays) from the WhatIsToday API. Ported from
  // TodayIsTodayBot/Services/TodayService.cs.
  type Client struct {
      httpClient *http.Client
      baseURL    string // overridable in tests; when set, used as the scheme+host prefix for all 3 sub-paths
  }

  func NewClient(httpClient *http.Client) *Client {
      return &Client{httpClient: httpClient}
  }

  func (c *Client) endpoint(kind, mmdd string) string {
      if c.baseURL != "" {
          return fmt.Sprintf("%s/v3/%s/%s", c.baseURL, kind, mmdd)
      }
      switch kind {
      case "anniv":
          return defaultAnnivBaseURL + mmdd
      case "birthflower":
          return defaultBirthflowerBaseURL + mmdd
      case "famousbirthday":
          return defaultFamousBirthdayBaseURL + mmdd
      }
      return ""
  }

  func (c *Client) fetchJSON(ctx context.Context, url string, out interface{}) error {
      req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
      if err != nil {
          return err
      }
      resp, err := c.httpClient.Do(req)
      if err != nil {
          return err
      }
      defer resp.Body.Close()
      if resp.StatusCode < 200 || resp.StatusCode >= 300 {
          return fmt.Errorf("today: unexpected status %d from %s", resp.StatusCode, url)
      }
      return json.NewDecoder(resp.Body).Decode(out)
  }

  // GetTodayInfo fetches all three sub-APIs concurrently and returns a single
  // formatted Japanese message, omitting any section whose sub-fetch failed.
  //
  // IMPORTANT — this is a deliberate, documented BEHAVIOR CHANGE from the .NET
  // source, not a faithful port of TodayService.cs's failure path. Tracing
  // TodayService.cs's actual control flow: GetAnniversaryAsync /
  // GetBirthflowerAsync / GetFamousBirthdayAsync each `throw;` internally on
  // failure (TodayService.cs:44,70,97). GetTodayInfoAsync wraps each in
  // `.ContinueWith(t => (object?)t.Result)` (TodayService.cs:112-114) — when
  // the antecedent task is Faulted, evaluating `t.Result` inside the
  // continuation throws, so the continuation itself becomes Faulted too.
  // `Task.WhenAll` then throws, which is swallowed by an empty `catch {}`
  // (TodayService.cs:117-124) — but the following lines
  // (`tasks[0].Result as AnniversaryResponse` etc., TodayService.cs:126-128)
  // call `.Result` on the still-Faulted task AGAIN, which throws synchronously
  // and is NOT caught anywhere inside GetTodayInfoAsync. It propagates out to
  // TodayCommand.ExecuteAsync's `catch (Exception ex)` (TodayCommand.cs:71-83),
  // which discards the entire reply and substitutes one generic
  // "❌ 情報の取得中にエラーが発生しました: {ex.Message}" message.
  //
  // So the REAL .NET behavior is: any single sub-API failure (not just all
  // three) aborts the WHOLE /today reply with one generic error — this reads
  // as an unintentional side effect of mixing .ContinueWith with .Result, not
  // a deliberate design choice. For this Go port, a partial/best-effort reply
  // (omitting only the failed section) is strictly better UX for a low-stakes
  // info command, so GetTodayInfo intentionally implements best-effort
  // per-section omission instead of reproducing the all-or-nothing failure
  // path above.
  func (c *Client) GetTodayInfo(ctx context.Context, date time.Time) string {
      mmdd := date.Format("0102")

      var (
          wg       sync.WaitGroup
          anniv    anniversaryResponse
          annivOK  bool
          flower   birthflowerResponse
          flowerOK bool
          famous   famousBirthdayResponse
          famousOK bool
      )

      wg.Add(3)
      go func() {
          defer wg.Done()
          if err := c.fetchJSON(ctx, c.endpoint("anniv", mmdd), &anniv); err == nil {
              annivOK = true
          }
      }()
      go func() {
          defer wg.Done()
          if err := c.fetchJSON(ctx, c.endpoint("birthflower", mmdd), &flower); err == nil {
              flowerOK = true
          }
      }()
      go func() {
          defer wg.Done()
          if err := c.fetchJSON(ctx, c.endpoint("famousbirthday", mmdd), &famous); err == nil {
              famousOK = true
          }
      }()
      wg.Wait()

      return formatTodayInfo(date, anniv, annivOK, flower, flowerOK, famous, famousOK)
  }

  func formatTodayInfo(
      date time.Time,
      anniv anniversaryResponse, annivOK bool,
      flower birthflowerResponse, flowerOK bool,
      famous famousBirthdayResponse, famousOK bool,
  ) string {
      dateStr := fmt.Sprintf("%d月%d日", int(date.Month()), date.Day())

      var b strings.Builder
      fmt.Fprintf(&b, "📅 **%sは何の日？**\n\n", dateStr)

      if annivOK {
          b.WriteString("🎉 **記念日**\n")
          items := []string{anniv.Anniv1, anniv.Anniv2, anniv.Anniv3, anniv.Anniv4, anniv.Anniv5}
          any := false
          for _, item := range items {
              if item != "" {
                  fmt.Fprintf(&b, "  • %s\n", item)
                  any = true
              }
          }
          if !any {
              b.WriteString("  （情報なし）\n")
          }
          b.WriteString("\n")
      }

      if flowerOK && flower.Flower != "" {
          b.WriteString("🌸 **誕生花**\n")
          fmt.Fprintf(&b, "  • %s", flower.Flower)
          if flower.Lang != "" {
              fmt.Fprintf(&b, "（花言葉: %s）", flower.Lang)
          }
          b.WriteString("\n\n")
      }

      if famousOK && famous.Name != "" {
          b.WriteString("👤 **この日生まれの偉人**\n")
          fmt.Fprintf(&b, "  • %s", famous.Name)
          if famous.Profile != "" {
              fmt.Fprintf(&b, "（%s）", famous.Profile)
          }
          if famous.Lifespan != "" {
              fmt.Fprintf(&b, " [%s]", famous.Lifespan)
          }
          b.WriteString("\n")
      }

      b.WriteString("\n_Powered by [whatistoday API](https://note.com/sooz/n/naffb68c7f53b)_")

      return b.String()
  }
  ```
  **Data-fidelity note (verified, not a placeholder):** the full-width `？`/`（）` characters above were confirmed byte-for-byte against `TodayService.cs:130,153,165,177` during plan review (an earlier draft of this plan used ASCII `?`/`()` here — that was wrong and has been corrected). Do not substitute ASCII punctuation when implementing.
- [ ] Run `go test ./internal/today/...` — expect `PASS` for all cases (all-succeed formats every section correctly with exact full-width Japanese punctuation; anniversary-empty-but-present shows the full-width `（情報なし）` message; all-fail still shows the header and omits every section).
- [ ] Run `go vet ./...` — expect clean.
- [ ] Commit: `feat: WhatIsToday APIクライアントをGoへ移植`.

### Task 10 — `today` command handler

Current behavior (`TodayIsTodayBot/Commands/Handlers/TodayCommand.cs:21-84`): optional `MMdd`-format date argument (e.g. `0101` for Jan 1); if omitted, uses today's date (server-local, `DateTime.Today`). **Two distinct error messages** depending on the invalid-input case (verified against source — do not collapse into one generic message):
- Malformed input (not exactly 4 digits, or non-numeric) → `TodayCommand.cs:48`'s message.
- Well-formed 4 digits but not a valid calendar date (e.g. `0230` = Feb 30) → `TodayCommand.cs:42`'s message (different text).

- [ ] Write failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\today_test.go`:
  ```go
  package commands

  import "testing"

  func TestTodayCommand_ParseDate_ValidMMdd(t *testing.T) {
      cmd := &TodayCommand{}
      d, err := cmd.parseDateArg("1225")
      if err != nil {
          t.Fatalf("expected valid MMdd to parse, got error: %v", err)
      }
      if d.Month() != 12 || d.Day() != 25 {
          t.Fatalf("expected December 25, got month=%d day=%d", d.Month(), d.Day())
      }
  }

  func TestTodayCommand_ParseDate_InvalidLength_ReturnsMalformedMessage(t *testing.T) {
      cmd := &TodayCommand{}
      _, err := cmd.parseDateArg("725")
      if err == nil {
          t.Fatal("expected error for non-4-digit input")
      }
      got := todayDateErrorMessage(err)
      want := "❌ 日付はMMdd形式で入力してください（例: 0101 = 1月1日、1225 = 12月25日）"
      if got != want {
          t.Fatalf("expected malformed-input message %q, got %q", want, got)
      }
  }

  func TestTodayCommand_ParseDate_NonNumeric_ReturnsMalformedMessage(t *testing.T) {
      cmd := &TodayCommand{}
      _, err := cmd.parseDateArg("abcd")
      if err == nil {
          t.Fatal("expected error for non-numeric input")
      }
      got := todayDateErrorMessage(err)
      want := "❌ 日付はMMdd形式で入力してください（例: 0101 = 1月1日、1225 = 12月25日）"
      if got != want {
          t.Fatalf("expected malformed-input message %q, got %q", want, got)
      }
  }

  func TestTodayCommand_ParseDate_InvalidCalendarDate_ReturnsInvalidDateMessage(t *testing.T) {
      cmd := &TodayCommand{}
      _, err := cmd.parseDateArg("0230") // Feb 30 does not exist
      if err == nil {
          t.Fatal("expected error for Feb 30 (invalid calendar date)")
      }
      got := todayDateErrorMessage(err)
      want := "❌ 無効な日付です。MMdd形式で正しい日付を入力してください（例: 0101 = 1月1日）"
      if got != want {
          t.Fatalf("expected invalid-calendar-date message %q, got %q", want, got)
      }
  }

  func TestTodayCommand_Definition(t *testing.T) {
      cmd := &TodayCommand{}
      def := cmd.Definition()
      if def.Name != "today" {
          t.Fatalf("expected command name 'today', got %q", def.Name)
      }
  }
  ```
- [ ] Run `go test ./internal/commands/... -run TestTodayCommand` — expect compile failure, confirming red.
- [ ] Implement `C:\Users\<user>\Documents\todayistodayBot\internal\commands\today.go`:
  ```go
  package commands

  import (
      "context"
      "fmt"
      "net/http"
      "strconv"
      "time"

      "github.com/SioKo-Shox3/todayistodayBot/internal/today"
      "github.com/bwmarrin/discordgo"
  )

  func init() { Register(&TodayCommand{todayClient: today.NewClient(http.DefaultClient)}) }

  // TodayCommand reports anniversaries/birth-flower/famous-birthdays for a
  // given date. Ported from TodayIsTodayBot/Commands/Handlers/TodayCommand.cs.
  type TodayCommand struct {
      todayClient *today.Client
  }

  func (c *TodayCommand) Definition() *discordgo.ApplicationCommand {
      return &discordgo.ApplicationCommand{
          Name: "today",
          // Full-width parens「（…）」— matches TodayCommand.cs:19 exactly.
          Description: "今日は何の日かを表示します（例: /today または /today 0101 で1月1日の情報）",
          Options: []*discordgo.ApplicationCommandOption{
              {
                  Type: discordgo.ApplicationCommandOptionString,
                  Name: "date",
                  // No direct .NET source string for this option's description
                  // (new Discord slash-command UI copy) — full-width parens
                  // chosen for internal consistency, not a ported string.
                  Description: "MMdd形式の日付（例: 0101 = 1月1日）。省略時は今日",
                  Required:    false,
              },
          },
      }
  }

  // dateParseErrorKind distinguishes the two invalid-input cases that
  // TodayCommand.cs handles with two DIFFERENT user-facing messages —
  // TodayCommand.cs:46-49 (malformed input: not exactly 4 digits, or
  // non-numeric) vs. TodayCommand.cs:40-44 (well-formed 4 digits, but not a
  // valid calendar date, e.g. "0230" = Feb 30). Do not collapse these into one
  // generic error — the message text differs between the two cases.
  type dateParseErrorKind int

  const (
      dateParseErrorMalformed dateParseErrorKind = iota
      dateParseErrorInvalidCalendarDate
  )

  type dateParseError struct {
      kind dateParseErrorKind
  }

  func (e *dateParseError) Error() string {
      switch e.kind {
      case dateParseErrorMalformed:
          return "malformed MMdd date argument"
      case dateParseErrorInvalidCalendarDate:
          return "well-formed but invalid calendar date"
      default:
          return "date parse error"
      }
  }

  // parseDateArg mirrors TodayCommand.cs's MMdd parsing: exactly 4 numeric
  // digits, first 2 = month, last 2 = day, validated as a real calendar date
  // in the current year. Returns a *dateParseError so the caller (Handle) can
  // tell the two invalid-input cases apart and pick the matching .NET-parity
  // message for each.
  func (c *TodayCommand) parseDateArg(arg string) (time.Time, error) {
      if len(arg) != 4 {
          return time.Time{}, &dateParseError{kind: dateParseErrorMalformed}
      }
      if _, err := strconv.Atoi(arg); err != nil {
          return time.Time{}, &dateParseError{kind: dateParseErrorMalformed}
      }
      month, _ := strconv.Atoi(arg[0:2])
      day, _ := strconv.Atoi(arg[2:4])

      year := time.Now().Year()
      d := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
      // time.Date silently normalizes out-of-range values (e.g. Feb 30 rolls
      // into March); detect that here to match .NET's ArgumentOutOfRangeException
      // rejecting invalid calendar dates (TodayCommand.cs:40-44).
      if int(d.Month()) != month || d.Day() != day {
          return time.Time{}, &dateParseError{kind: dateParseErrorInvalidCalendarDate}
      }
      return d, nil
  }

  func (c *TodayCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
      var dateArg string
      for _, opt := range i.ApplicationCommandData().Options {
          if opt.Name == "date" {
              dateArg = opt.StringValue()
          }
      }

      targetDate := time.Now()
      if dateArg != "" {
          parsed, err := c.parseDateArg(dateArg)
          if err != nil {
              content := todayDateErrorMessage(err)
              return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
                  Type: discordgo.InteractionResponseChannelMessageWithSource,
                  Data: &discordgo.InteractionResponseData{Content: content},
              })
          }
          targetDate = parsed
      }

      result := c.todayClient.GetTodayInfo(context.Background(), targetDate)

      return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
          Type: discordgo.InteractionResponseChannelMessageWithSource,
          Data: &discordgo.InteractionResponseData{Content: result},
      })
  }

  // todayDateErrorMessage maps a parseDateArg error to the exact .NET-parity
  // user-facing message for its kind. Falls back to the malformed-input
  // message for any error type it doesn't recognize (defensive default; in
  // practice parseDateArg only ever returns *dateParseError).
  func todayDateErrorMessage(err error) string {
      if dpe, ok := err.(*dateParseError); ok && dpe.kind == dateParseErrorInvalidCalendarDate {
          // Matches TodayCommand.cs:42 exactly.
          return "❌ 無効な日付です。MMdd形式で正しい日付を入力してください（例: 0101 = 1月1日）"
      }
      // Matches TodayCommand.cs:48 exactly.
      return "❌ 日付はMMdd形式で入力してください（例: 0101 = 1月1日、1225 = 12月25日）"
  }
  ```
- [ ] Run `go test ./internal/commands/... -run TestTodayCommand` — expect `PASS` (both distinct invalid-input messages assert their exact full-width text).
- [ ] Run `go vet ./...` and `go build ./...` — expect clean.
- [ ] Commit: `feat: todayコマンドをGo/discordgoへ移植`.

### Task 11 — `dice` command

Current behavior (`TodayIsTodayBot/Commands/Handlers/DiceCommand.cs:22-84`):
- Default `sides=6`, `rolls=1`.
- If `sides` arg present but not a parseable int `>= 2` -> `❌ 面数は2以上の整数で指定してください。`
- If `rolls` arg present but not a parseable int `>= 1` -> `❌ 回数は1以上の整数で指定してください。`
- `rolls > 100` -> `❌ 一度に振れる回数は100回までです。`
- `sides > 1000000` -> `❌ 面数は1,000,000以下で指定してください。`
- Rolls `rolls` times uniformly in `[1, sides]`, sums them.
- `rolls == 1` -> `🎲 {sides}面ダイスを1回振って、結果は **{total}** です!`
- `rolls > 1` -> `🎲 {sides}面ダイスを{rolls}回振って、結果は **{total}** ({r1}+{r2}+...)です!` (individual results joined with full-width `+`, exact character: `＋`).

- [ ] Write failing test first. Create `C:\Users\<user>\Documents\todayistodayBot\internal\commands\dice_test.go`:
  ```go
  package commands

  import (
      "strings"
      "testing"
  )

  func TestDiceCommand_Roll_SingleRoll_Format(t *testing.T) {
      cmd := &DiceCommand{}
      msg, err := cmd.roll(6, 1, func(n int) int { return 4 }) // deterministic roll func for testing
      if err != nil {
          t.Fatalf("unexpected error: %v", err)
      }
      if msg != "🎲 6面ダイスを1回振って、結果は **4** です！" {
          t.Fatalf("unexpected message: %q", msg)
      }
  }

  func TestDiceCommand_Roll_MultipleRolls_Format(t *testing.T) {
      cmd := &DiceCommand{}
      seq := []int{2, 5, 3}
      idx := 0
      msg, err := cmd.roll(6, 3, func(n int) int {
          v := seq[idx]
          idx++
          return v
      })
      if err != nil {
          t.Fatalf("unexpected error: %v", err)
      }
      if !strings.Contains(msg, "**10**") {
          t.Fatalf("expected total 10, got: %q", msg)
      }
      // Full-width parens + full-width plus — matches DiceCommand.cs:79-80
      // exactly. This assertion fails if the parens were accidentally ASCII,
      // unlike a loose "contains 2＋5＋3" check without the parens.
      if !strings.Contains(msg, "（2＋5＋3）") {
          t.Fatalf("expected full-width-paren individual results, got: %q", msg)
      }
  }

  func TestDiceCommand_ValidateArgs_SidesTooLow(t *testing.T) {
      cmd := &DiceCommand{}
      if err := cmd.validateArgs(1, 1); err == nil {
          t.Fatal("expected error for sides < 2")
      }
  }

  func TestDiceCommand_ValidateArgs_RollsTooMany(t *testing.T) {
      cmd := &DiceCommand{}
      if err := cmd.validateArgs(6, 101); err == nil {
          t.Fatal("expected error for rolls > 100")
      }
  }

  func TestDiceCommand_ValidateArgs_SidesTooHigh(t *testing.T) {
      cmd := &DiceCommand{}
      if err := cmd.validateArgs(1000001, 1); err == nil {
          t.Fatal("expected error for sides > 1,000,000")
      }
  }
  ```
- [ ] Run `go test ./internal/commands/... -run TestDiceCommand` — expect compile failure, confirming red.
- [ ] Implement `C:\Users\<user>\Documents\todayistodayBot\internal\commands\dice.go`:
  ```go
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

      return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
          Type: discordgo.InteractionResponseChannelMessageWithSource,
          Data: &discordgo.InteractionResponseData{Content: msg},
      })
  }
  ```
  **Data-fidelity note (verified, not a placeholder):** the multi-roll message's full-width `（%s）` was confirmed against `DiceCommand.cs:80` during plan review (an earlier draft used ASCII `(%s)` — that was wrong and has been corrected). Note `Definition()`'s `Description` field intentionally keeps its HALF-width parens (`(例: /dice 6 3 で6面ダイスを3回振る)`), matching `DiceCommand.cs:15` exactly — the C# source itself is inconsistent between the two strings, so do not "fix" `Definition()` to full-width.
- [ ] Run `go test ./internal/commands/... -run TestDiceCommand` — expect `PASS`.
- [ ] Run `go vet ./...` — expect clean.
- [ ] Commit: `feat: diceコマンドをGo/discordgoへ移植`.

### Task 12 — `cmd/bot/main.go` wiring

- [ ] Write failing test first — `main.go` is glue code that's inherently hard to unit test end-to-end (it opens a real Discord gateway connection), so instead extract the one pure/testable piece: the interaction dispatcher that maps a command name to the registered `Command`. Create `C:\Users\<user>\Documents\todayistodayBot\cmd\bot\main_test.go`:
  ```go
  package main

  import (
      "testing"

      "github.com/SioKo-Shox3/todayistodayBot/internal/commands"
      "github.com/bwmarrin/discordgo"
  )

  type stubCommand struct {
      name    string
      handled bool
  }

  func (s *stubCommand) Definition() *discordgo.ApplicationCommand {
      return &discordgo.ApplicationCommand{Name: s.name}
  }

  func (s *stubCommand) Handle(sess *discordgo.Session, i *discordgo.InteractionCreate) error {
      s.handled = true
      return nil
  }

  func TestFindCommandByName(t *testing.T) {
      cmds := []commands.Command{
          &stubCommand{name: "ping"},
          &stubCommand{name: "dice"},
      }

      found := findCommandByName(cmds, "dice")
      if found == nil {
          t.Fatal("expected to find 'dice' command")
      }
      if found.Definition().Name != "dice" {
          t.Fatalf("expected dice command, got %q", found.Definition().Name)
      }

      notFound := findCommandByName(cmds, "nonexistent")
      if notFound != nil {
          t.Fatal("expected nil for unregistered command name")
      }
  }
  ```
- [ ] Run `go test ./cmd/bot/...` — expect compile failure (`findCommandByName` undefined), confirming red.
- [ ] Implement `C:\Users\<user>\Documents\todayistodayBot\cmd\bot\main.go`:
  ```go
  package main

  import (
      "context"
      "log/slog"
      "os"
      "os/signal"
      "syscall"

      "github.com/SioKo-Shox3/todayistodayBot/internal/commands"
      "github.com/SioKo-Shox3/todayistodayBot/internal/config"

      "github.com/bwmarrin/discordgo"
  )

  func findCommandByName(cmds []commands.Command, name string) commands.Command {
      for _, c := range cmds {
          if c.Definition().Name == name {
              return c
          }
      }
      return nil
  }

  func main() {
      logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
      slog.SetDefault(logger)

      cfg, err := config.Load()
      if err != nil {
          slog.Error("failed to load configuration", "error", err)
          os.Exit(1)
      }

      session, err := discordgo.New("Bot " + cfg.DiscordToken)
      if err != nil {
          slog.Error("failed to create discordgo session", "error", err)
          os.Exit(1)
      }

      all := commands.All()

      session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
          if i.Type != discordgo.InteractionApplicationCommand {
              return
          }
          name := i.ApplicationCommandData().Name
          cmd := findCommandByName(all, name)
          if cmd == nil {
              slog.Warn("received interaction for unregistered command", "name", name)
              return
          }
          if err := cmd.Handle(s, i); err != nil {
              slog.Error("command handler returned an error", "name", name, "error", err)
              _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
                  Type: discordgo.InteractionResponseChannelMessageWithSource,
                  Data: &discordgo.InteractionResponseData{
                      Content: "❌ コマンドの実行中にエラーが発生しました。",
                  },
              })
          }
      })

      session.Identify.Intents = discordgo.IntentsNone

      if err := session.Open(); err != nil {
          slog.Error("failed to open discord session", "error", err)
          os.Exit(1)
      }
      defer session.Close()

      for _, cmd := range all {
          if _, err := session.ApplicationCommandCreate(session.State.User.ID, "", cmd.Definition()); err != nil {
              slog.Error("failed to register application command", "name", cmd.Definition().Name, "error", err)
              os.Exit(1)
          }
      }

      slog.Info("bot is running", "registered_commands", len(all))

      ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
      defer stop()
      <-ctx.Done()

      slog.Info("shutdown signal received, closing session")
  }
  ```
  **Important — fix before compiling:** `commands.All()` requires every command file's `init()` to have already run, which in Go happens automatically once the `internal/commands` package is imported *at all* — the `import "github.com/SioKo-Shox3/todayistodayBot/internal/commands"` line above (a normal, non-blank import, since `commands.Command` and `commands.All()` are referenced by name) is sufficient to trigger every `init()` in that package. Do NOT use a blank `_` import here — write it as a regular import exactly as shown, since the package's exported names (`Command`, `All`) are used directly in this file.

  **Known limitation (documented, not silently absent):** the `init()`-based auto-discovery registration pattern (Task 2) only handles the "add a command" direction. There is no de-registration/cleanup path in this Phase A design — if a command's `.go` file is later deleted from `internal/commands/`, its Application Command definition previously registered with Discord via `session.ApplicationCommandCreate` above will linger as a stale global command on Discord's side (Discord does not automatically remove commands the bot process no longer defines). Cleaning up stale commands would require either diffing the currently-registered set against `commands.All()` on startup and calling `session.ApplicationCommandDelete` for anything no longer present, or a manual one-off cleanup script — neither is implemented in Phase A. This is an acceptable known limitation given the phase's scope (5 stable, unlikely-to-be-removed commands), not an oversight left undocumented.
- [ ] Run `go test ./cmd/bot/...` — expect `PASS`.
- [ ] Run `go build ./...` — expect a successful build producing a `bot` (or `bot.exe` on Windows) binary; confirm no compile errors across the whole module now that `cmd/bot` ties every package together.
- [ ] Run `go vet ./...` — expect clean across the whole module.
- [ ] Commit: `feat: discordgo Session配線とグレースフルシャットダウンを実装`.

### Task 13 — `Makefile` + `Dockerfile`

- [ ] Create `C:\Users\<user>\Documents\todayistodayBot\Makefile`:
  ```makefile
  BINARY_NAME := todayistodaybot
  CMD_PATH := ./cmd/bot

  .PHONY: build build-linux-amd64 build-linux-arm64 docker-build test vet clean

  build:
  	go build -o bin/$(BINARY_NAME) $(CMD_PATH)

  build-linux-amd64:
  	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/$(BINARY_NAME)-linux-amd64 $(CMD_PATH)

  build-linux-arm64:
  	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/$(BINARY_NAME)-linux-arm64 $(CMD_PATH)

  docker-build:
  	docker build -t $(BINARY_NAME):latest -f deploy/Dockerfile .

  test:
  	go test ./...

  vet:
  	go vet ./...

  clean:
  	rm -rf bin/
  ```
- [ ] Create `C:\Users\<user>\Documents\todayistodayBot\deploy\Dockerfile`:
  ```dockerfile
  # syntax=docker/dockerfile:1

  FROM golang:alpine AS build
  WORKDIR /src
  COPY go.mod go.sum ./
  RUN go mod download
  COPY . .
  RUN CGO_ENABLED=0 go build -o /out/todayistodaybot ./cmd/bot

  FROM alpine:latest
  RUN adduser -D -u 10001 botuser
  COPY --from=build /out/todayistodaybot /usr/local/bin/todayistodaybot
  USER botuser
  ENTRYPOINT ["/usr/local/bin/todayistodaybot"]
  ```
  (Uses `alpine` rather than `scratch` for the final stage so `ca-certificates` — required for HTTPS calls to Open-Meteo/WhatIsToday/Discord — is present out of the box; this satisfies the spec's "`scratch`または`alpine`" either-or, chosen for simplicity over minimal image size.)

  **Config resolution inside the container:** the image sets no `WORKDIR` in the final stage and bakes in no `config.json`/`CONFIG_PATH` default — this is intentional, not an oversight. The env-first resolution in `internal/config.Load()` (Task 3) means the standard way to run this image is `docker run -e DISCORD_TOKEN=... todayistodaybot`; operators who prefer a mounted config file must also set `-e CONFIG_PATH=/path/inside/container/config.json` and bind-mount that path, since there is no baked-in default file location to fall back to inside the container.
- [ ] Verification (manual, since Go/Docker are not installed on this planning machine — see Task-0 notes): once the implementer has Go and Docker available,
  ```
  make build
  make build-linux-amd64
  make build-linux-arm64
  make docker-build
  ```
  Expect each to produce a binary under `bin/` (or a Docker image tagged `todayistodaybot:latest`) with no errors. Record actual command output as evidence.
- [ ] Commit: `build: Makefileとマルチステージdocker-buildを追加`.

### Task 14 — `deploy/systemd/todayistodaybot.service`

- [ ] Create `C:\Users\<user>\Documents\todayistodayBot\deploy\systemd\todayistodaybot.service`:
  ```ini
  [Unit]
  Description=TodayIsTodayBot Discord bot
  After=network-online.target
  Wants=network-online.target

  [Service]
  Type=simple
  User=todayistodaybot
  Group=todayistodaybot
  WorkingDirectory=/opt/todayistodaybot
  ExecStart=/opt/todayistodaybot/todayistodaybot
  EnvironmentFile=-/etc/todayistodaybot/todayistodaybot.env
  Restart=on-failure
  RestartSec=5
  NoNewPrivileges=true
  ProtectSystem=strict
  ProtectHome=true

  [Install]
  WantedBy=multi-user.target
  ```
  (`EnvironmentFile=-/etc/...` — the leading `-` makes the file optional, matching the spec's "`EnvironmentFile`任意指定"; `User=`/`Group=` run as a dedicated non-root service account per the spec's "非rootユーザー実行".)
- [ ] Verification: no automated test (it's a static unit file); manual verification is `systemd-analyze verify deploy/systemd/todayistodaybot.service` if systemd tooling is available in the implementation environment, otherwise a visual review that syntax matches standard systemd unit-file grammar (section headers in brackets, `Key=Value` lines).
- [ ] Commit: `deploy: systemdユニットファイルを追加`.

### Task 15 — Update `CLAUDE.md` and `AGENTS.md` (identically)

Per the project's own rule (`CLAUDE.md:7-9`, read during investigation: "`CLAUDE.md` と `AGENTS.md` は完全に同一内容で運用する"), both files must receive byte-identical changes. Concretely, in the copy of `CLAUDE.md` read during investigation:

- [ ] Line `CLAUDE.md:18-21` (project overview mentioning ".NET 9.0 / Discord.Net") — rewrite to describe the Go/discordgo stack, dropping the schedule/reminder mention (out of Phase A scope) or noting it moved to Phase B. Example replacement text:
  ```markdown
  TodayIsTodayBot は Discord 用の日本語応答 Bot(Go / discordgo)。Discordネイティブのスラッシュコマンド
  (Interactions API)に応答する: 天気(Open-Meteo)・今日は何の日(WhatIsToday API)・サイコロなど。
  単一プロセスで、コマンドは `internal/commands` の各ファイルが `init()` で自己登録する(自動探索)。
  日程調整・リマインダー機能はフェーズBでchosei-sama連携として再実装予定(現時点では未実装)。
  詳細は `Docs/agent-guide/architecture.md`。
  ```
- [ ] Line `CLAUDE.md:27-28` (絶対規則②, `Program.RegisterCommands()`手動登録) — rewrite per spec (`Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md:109`):
  ```markdown
  2. **新コマンドは `internal/commands` にファイルを追加し `init()` で自己登録する**(自動探索)。
     `Command` インターフェース(`Definition()` / `Handle()`)を実装し、ファイル内の `init()` で
     `Register(...)` を呼ぶ(→ `Docs/agent-guide/coding-style.md`)。`main.go` の一覧編集は不要。
  ```
- [ ] Line `CLAUDE.md:29-30` (絶対規則③, async/await必須・SemaphoreSlim) — rewrite per spec (`Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md:110`):
  ```markdown
  3. **並行性はGoの規律に従う** — goroutine/channelを使い、共有可変状態には`sync.Mutex`/`sync.WaitGroup`で
     排他する。フェーズAは永続化を持たないため対象箇所は無いが、将来永続化を追加する際は単一ライター
     化する(→ `Docs/agent-guide/architecture.md` の危険地帯)。
  ```
- [ ] Line `CLAUDE.md:31` (絶対規則④, dotnet build警告0) — rewrite per spec (`Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md:111`):
  ```markdown
  4. **ビルド警告を握りつぶさない** — `go vet ./...` / `go build ./...` / `go test ./...` がエラー0・
     警告0・テスト成功で通ることを Done の条件とする。
  ```
- [ ] Any other `.NET`/`dotnet`/C# references elsewhere in `CLAUDE.md` (re-grep the file at implementation time to catch anything not listed above) should be updated consistently.
- [ ] Apply the exact same diff to `AGENTS.md` (verify with `diff CLAUDE.md AGENTS.md` showing no output after the edit).
- [ ] Verification: `diff C:\Users\<user>\Documents\todayistodayBot\CLAUDE.md C:\Users\<user>\Documents\todayistodayBot\AGENTS.md` — expect empty output (files identical).
- [ ] Commit: `docs: CLAUDE.md/AGENTS.mdをGoスタック向けに更新`.

### Task 16 — Repoint `.claude/hooks/enforce-codex-impl.mjs` from `.cs` to `.go`

Current logic read at `C:\Users\<user>\Documents\todayistodayBot\.claude\hooks\enforce-codex-impl.mjs:31`:
```js
const IMPL_SOURCE_RE = /\.cs$/i;
```
with an accompanying comment at lines 27-30 explicitly calling out "TodayIsTodayBot is a single-language C#/.NET project, so this is just `.cs`."

- [ ] Change line 31 to:
  ```js
  const IMPL_SOURCE_RE = /\.go$/i;
  ```
- [ ] Update the comment at lines 27-30 to reflect Go instead of C#/.NET, e.g.:
  ```js
  // Implementation source the main thread must not TYPE. TodayIsTodayBot is a
  // single-language Go project, so this is just `.go`. Docs (.md), the module
  // file (go.mod), config (config.json*), the Makefile, and these hook
  // scripts (.mjs) are NOT blocked — the main thread legitimately edits those.
  ```
- [ ] No automated test exists for this hook (it's a Node.js script invoked by the Claude Code harness's PreToolUse mechanism, not part of the Go module or its test suite) — verification is manual: after editing, attempt (in a real Claude Code session, not as part of this plan's Go build) an `Edit` on a `.go` file from the main thread and confirm it is blocked with exit code 2; attempt an `Edit` on a `.md` file and confirm it passes. This manual check is out of scope for the implementer to perform during Phase A coding (it requires a live harness invocation) — note it as a follow-up manual smoke test for the orchestrator, not a blocking gate for this task's commit.
- [ ] Commit: `chore: 実装ガードフックの対象を.csから.goへ変更`.

### Task 17 — Rewrite `Docs/agent-guide/architecture.md`, `coding-style.md`, `build-and-verify.md`

**`architecture.md`** (currently describes .NET layering at `Docs/agent-guide/architecture.md:1-67`, read in full during investigation) — rewrite to describe:
- [ ] Overall structure table: `cmd/bot/main.go` (entry — config load, session wiring, command registration, dispatch, graceful shutdown), `internal/commands/` (registry + 5 command files, each self-registering via `init()`), `internal/weather/` (Open-Meteo client), `internal/today/` (WhatIsToday client), `internal/config/` (env-first/file-fallback loader). No `Handlers/`, `Services/`, `Models/` layers carried over — Phase A has no persistence and no gateway event handlers beyond `InteractionCreate`.
- [ ] Ownership/lifetime: replace "`Program.cs`が起動時に1回だけ生成、コンストラクタ注入" with "`main.go`が起動時に`config.Load()`→`discordgo.New()`→コマンド一覧`commands.All()`を1回だけ取得し、以後は読み取り専用で使う。共有`*http.Client`は`http.DefaultClient`を各コマンドが直接使う(フェーズAはDIコンテナなし、手組み配線を踏襲)。"
- [ ] Threading: replace Discord.Net's async-callback model + FPS main loop description with: "discordgoの`Session`は内部でgoroutineを使いGatewayイベントを配送する。`InteractionCreate`ハンドラは`main.go`が1つだけ登録し、コマンド名で`internal/commands`のレジストリから対象`Command`を探して`Handle()`を呼ぶ。フェーズAは永続化された共有可変状態を持たないため、mutex等での排他は不要(将来スケジュール機能等を追加する際は`sync.Mutex`での単一ライター化を検討)。"
- [ ] Dependency direction: `cmd/bot` → `internal/commands`(登録一覧取得・ディスパッチ) / `internal/config`。`internal/commands/*` → `internal/weather`, `internal/today`(必要なクライアントを直接生成)。`internal/weather`, `internal/today` → 外部APIのみ、他の内部パッケージに依存しない(逆流禁止は維持)。
- [ ] Danger zones: since Phase A removes the reaction-vote race, JSON persistence, 10-emoji cap, and FPS main-loop blocking risk (`architecture.md:58-67`'s items 1-4 no longer apply — those were all `ScheduleCommand`/`ReminderService`/`ReactionHandler`/`MainLoopAsync` concerns, all deleted in this phase), replace the danger-zone list with: "フェーズAには永続化・投票競合状態が存在しない(スコープ外)。危険地帯として残るのは (1) 秘密情報(`DISCORD_TOKEN`環境変数・`config.json`)を絶対にコミットしない、(2) 新コマンドの`internal/commands`への追加漏れ(ただし自動探索のため、ファイルを追加し忘れない限り登録漏れは起きない — 旧来の『登録行の書き忘れ』リスクは無くなった)。" — Note to the doc-writer: do NOT simply delete the danger-zone section; explicitly state what changed and why, so future sessions understand the .NET-era risks are retired, not merely unmentioned.

**`coding-style.md`** (currently `Docs/agent-guide/coding-style.md:1-38`, read in full) — rewrite to describe:
- [ ] Go conventions: standard `gofmt`/`go vet` formatting (no custom brace style debates — gofmt is canonical and non-negotiable), package-per-directory under `internal/`, exported vs. unexported naming (`PascalCase` for exported, `camelCase` for unexported — note this differs from C#'s underscore-prefixed private fields), error handling via returned `error` values (not exceptions/panics for expected failure paths), JSON via `encoding/json` with struct tags (not attributes).
- [ ] New command convention: implement `commands.Command` (`Definition()`, `Handle()`), place in `internal/commands/<name>.go`, self-register via `func init() { Register(&XCommand{}) }` — replacing the old "`ICommandHandler`実装+`Program.RegisterCommands()`登録" section.
- [ ] File placement: `cmd/bot/` (entrypoint only), `internal/commands/` (one file per command + `registry.go`), `internal/weather/`, `internal/today/` (one package per external API), `internal/config/`. Test files co-located as `_test.go` per Go convention (not a separate test project, unlike the retired "テストプロジェクトは現状なし" .NET note).
- [ ] Encoding/line-endings: Go source is UTF-8 (gofmt enforces this); note whether the repo continues CRLF or switches to LF for `.go` files — **UNCERTAIN**, recommend the implementer follow whatever `git config core.autocrlf` / `.gitattributes` (if any) dictates, or default to LF for new Go files since that's the Go ecosystem norm, and flag this choice explicitly in the doc rather than leaving it implicit.

**`build-and-verify.md`** (currently `Docs/agent-guide/build-and-verify.md:1-63`, read in full) — rewrite to describe:
- [ ] Prerequisites: Go 1.22+ toolchain (note: no admin/SDK-install step is needed beyond installing Go itself — no NuGet-equivalent restore step, `go mod download` runs automatically or via `go build`).
- [ ] Config setup: `cp config.json.template config.json` then fill in `discord_token`, OR set `DISCORD_TOKEN` env var (env takes priority) — replacing the old `appsettings.json.template` copy step.
- [ ] Build commands:
  ```
  go build ./...
  go vet ./...
  go test ./...
  make build
  make build-linux-amd64
  make build-linux-arm64
  make docker-build
  ```
- [ ] Quality gate: `go build ./...` and `go vet ./...` must produce no errors and no output (clean); `go test ./...` must show `ok` for every package with no `FAIL`. This replaces "`dotnet build`エラー0・警告0" as the Done condition.
- [ ] Evidence standard: paste the actual `go build`/`go vet`/`go test` invocation and its final output lines (e.g. `ok  	github.com/SioKo-Shox3/todayistodayBot/internal/commands	0.123s`), same rigor as the old "`Build succeeded`/`ビルドに成功しました`と`0 Error(s)`" requirement.
- [ ] Artifacts not to commit: replace `bin/`, `obj/`, `appsettings.json`, `data/schedules.json`, `data/reminders.json` with `bin/` (Go build output dir used by the Makefile — same name, different meaning), `config.json`, and note there is no `data/*.json` anymore (no persistence in Phase A).
- [ ] Commit (bundling this task with Task 15's docs, or as its own commit — implementer's choice, but keep `CLAUDE.md`/`AGENTS.md` and the three `Docs/agent-guide/*.md` files together if committed together since they're one logical "docs migration" change): `docs: architecture/coding-style/build-and-verifyをGoスタック向けに書き直し`.

### Task 18 — Update `.claude/agents/*.md` .NET-specific references

Investigation found exactly these 6 lines across 6 files containing hard-coded .NET/C#/dotnet references (confirmed via grep during investigation):

- [ ] `C:\Users\<user>\Documents\todayistodayBot\.claude\agents\verifier.md:47` — currently:
  ```
  - ゲートは `dotnet build todayistodayBot-1.sln` を実際に走らせ、末尾の結果行(エラー0・警告0)を証拠に貼る。
  ```
  Change to:
  ```
  - ゲートは `go build ./...` / `go vet ./...` / `go test ./...` を実際に走らせ、エラー0・警告0・テスト成功を証拠に貼る。
  ```
- [ ] `C:\Users\<user>\Documents\todayistodayBot\.claude\agents\researcher.md:29-30` — currently:
  ```
  - スタックは C# / .NET 9.0 / Discord.Net 3.18.0(コンソール実行、DI なしの手組み配線)。
  - コマンドは `Commands/Handlers/*.cs`(`ICommandHandler` 実装)+ `Program.RegisterCommands()` の手動登録。
  ```
  Change to:
  ```
  - スタックは Go / discordgo(単一バイナリ実行、DI コンテナなしの手組み配線)。
  - コマンドは `internal/commands/*.go`(`Command` インターフェース実装)+ ファイル内 `init()` の自己登録(自動探索)。
  ```
- [ ] `C:\Users\<user>\Documents\todayistodayBot\.claude\agents\plan-reviewer.md:48` — currently:
  ```
  - 検証コマンドが `dotnet build`(警告0)以上を満たし、失敗し得るか。
  ```
  Change to:
  ```
  - 検証コマンドが `go build` / `go vet` / `go test`(いずれもエラー0・警告0・テスト成功)以上を満たし、失敗し得るか。
  ```
- [ ] `C:\Users\<user>\Documents\todayistodayBot\.claude\agents\planner.md:51` — currently:
  ```
  - 検証は最低 `dotnet build todayistodayBot-1.sln`(エラー0・警告0)。挙動確認が要るなら手動手順も書く。
  ```
  Change to:
  ```
  - 検証は最低 `go build ./...` / `go vet ./...` / `go test ./...`(エラー0・警告0・テスト成功)。挙動確認が要るなら手動手順も書く。
  ```
- [ ] `C:\Users\<user>\Documents\todayistodayBot\.claude\agents\implementer.md:28-31` — currently:
  ```
  - C# / .NET 9.0。file-scoped namespace・Nullable 有効・全 I/O は async(`.Result` / `.Wait()` 禁止)。
  - 新コマンド: `Commands/Handlers/<Name>Command.cs` に `ICommandHandler` を実装し、`Program.RegisterCommands()` に登録行を足す。
  ...
  - `appsettings.json` `data/*.json` `bin/` `obj/` を触らない。検証は `dotnet build todayistodayBot-1.sln`(エラー0・警告0)。
  ```
  Change to:
  ```
  - Go。gofmt準拠・全エラーは戻り値の`error`で扱う(パニックで制御フローを作らない)。
  - 新コマンド: `internal/commands/<name>.go` に `Command` インターフェース(`Definition()`/`Handle()`)を実装し、
    ファイル内の `init()` で `Register(&XCommand{})` を呼ぶ(`main.go`の編集は不要)。
  ...
  - `config.json` `bin/` を触らない。検証は `go build ./...` / `go vet ./...` / `go test ./...`(エラー0・警告0・テスト成功)。
  ```
  (Implementer must re-read the full file at `C:\Users\<user>\Documents\todayistodayBot\.claude\agents\implementer.md` before editing to preserve surrounding context/formatting exactly — only the .NET-specific lines quoted above change.)
- [ ] `C:\Users\<user>\Documents\todayistodayBot\.claude\agents\impl-reviewer.md:51` — currently:
  ```
  - Nullable 警告の握りつぶし、外部 API 応答の例外未処理。検証は `dotnet build`(警告0)が実行されたか。
  ```
  Change to:
  ```
  - エラー値の握りつぶし(`err`の無視)、外部 API 応答のエラーハンドリング漏れ。検証は `go build` / `go vet` / `go test`(エラー0・警告0・テスト成功)が実行されたか。
  ```
- [ ] **UNCERTAIN — `.codex/agents/*.toml`**: the spec (`Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md:114`) only explicitly names `.claude/agents/*`. Investigation found `.codex/agents/` contains a parallel TOML set (`implementer.toml`, `impl_reviewer.toml`, `plan_reviewer.toml`, `planner.toml`, `researcher.toml`, `test_designer.toml`, `verifier.toml`) that likely mirrors the same content in a different format, per this project's stated Claude/Codex parity convention (`CLAUDE.md:7-9`, though that rule is scoped to `CLAUDE.md`/`AGENTS.md` specifically, not the agents directories). Before doing this work, the implementer/orchestrator should diff-check whether `.codex/agents/*.toml` actually contains the equivalent .NET-specific strings (grep for `dotnet`/`\.cs`/`C#`/`\.NET` inside `.codex/agents/*.toml`) and, if so, apply the equivalent TOML-syntax edits for consistency — flagging this to the user/orchestrator as a decision point rather than silently skipping it, since the spec is silent on this specific directory.
- [ ] Verification: re-run the same grep used during investigation (`grep -rE "\.cs|dotnet|C#|\.NET|Discord\.Net" .claude/agents/`) — expect no matches after this task's edits.
- [ ] Commit: `docs: サブエージェント定義の.NET固有記述をGoスタック向けに更新`.

### Task 19 — Final cutover: delete the .NET project

**This task only starts once Tasks 1-18 are all green** (`go build ./...`, `go vet ./...`, `go test ./...` all clean, and — per the spec's cutover condition, `Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md:118-119` — a manual smoke test against a real Discord server has been performed and confirmed working for all 5 commands).

- [ ] Manual smoke-test checklist (perform against a real/test Discord server with the bot's application commands registered — this cannot be automated and is the explicit gate the spec requires before deleting the .NET code):
  - [ ] `/ping` replies `🏓 Pong!`
  - [ ] `/help` lists all 5 commands (ping, help, weather, today, dice) sorted alphabetically
  - [ ] `/weather city:東京` returns a formatted weather message; `/weather` with no city shows the usage help
  - [ ] `/today` (no args) shows today's anniversary/flower/famous-birthday info; `/today date:1225` shows Dec 25 info; `/today date:abc` shows the format-error message
  - [ ] `/dice` rolls a single d6; `/dice sides:20 rolls:3` rolls 3d20 and shows the sum + individual results
  - Record the actual Discord responses observed (screenshots or copy-pasted text) as evidence in the commit/PR description.
- [ ] Delete `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\` (entire directory: `Commands/`, `Handlers/`, `Services/`, `Models/`, `Program.cs`, `TodayIsTodayBot.csproj`, `appsettings.json.template`, and any `bin/`/`obj/` build artifacts if present — confirm these are gitignored and not tracked before deleting, so `git rm` only needs to touch tracked files).
- [ ] Delete `C:\Users\<user>\Documents\todayistodayBot\todayistodayBot-1.sln`.
- [ ] Verify no remaining references to the deleted paths: grep the repo for `TodayIsTodayBot.csproj`, `todayistodayBot-1.sln`, `dotnet build`, `appsettings.json` outside of git history/this plan document itself — expect zero hits in any file that is not this plan or a historical doc note.
- [ ] Run the full Go quality gate one more time post-deletion to confirm nothing in the deleted tree was accidentally load-bearing: `go build ./...`, `go vet ./...`, `go test ./...` — expect all clean (the .NET deletion should have zero effect on the Go build since they're unrelated toolchains in the same repo).
- [ ] Commit: `chore: フェーズA移行完了に伴い.NETプロジェクト一式を削除`.

---

## Verification (full gates, run at the end and after each task per the per-task steps above)

From repo root `C:\Users\<user>\Documents\todayistodayBot`:

```
go build ./...
go vet ./...
go test ./...
```

**Success criteria:**
- `go build ./...` exits 0 with no output (no compile errors).
- `go vet ./...` exits 0 with no output (no vet warnings).
- `go test ./...` exits 0 and prints `ok` for every package (`internal/config`, `internal/commands`, `internal/weather`, `internal/today`, `cmd/bot`), with zero `FAIL` lines.

Additionally, once Tasks 13-14 are done:
```
make build
make build-linux-amd64
make build-linux-arm64
make docker-build
```
**Success criteria:** each produces a binary (`bin/todayistodaybot*`) or Docker image with no error output.

**UNCERTAIN:** this planning session found Go is not installed on the current machine (`go version` returned not-found during investigation). The implementer must install Go before Task 1 and should report the installed version as part of Task 1's evidence — this is not a blocker for the plan itself but is a precondition the orchestrator should confirm before handing this plan to the `implementer` subagent.

For the doc-only tasks (15, 17, 18), the success criterion is not a build command but: `diff CLAUDE.md AGENTS.md` produces no output (Task 15), and the grep re-run in Task 18 produces no matches for `.NET`-era terms in `.claude/agents/*.md`.

---

## Risk level and containment

Per `CLAUDE.md`'s danger-zone framing (`Docs/agent-guide/architecture.md:54-67`, read during investigation) and the project's own high-risk categories (concurrency/persistence touching `ReactionHandler`/`*StorageService`/`SemaphoreSlim`):

- **This phase does NOT touch any of the project's designated high-risk areas.** `ReactionHandler`, `ScheduleStorageService`, `ReminderService`, and their `SemaphoreSlim`-guarded persistence are explicitly out of scope (spec: `Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md:16-17`) and are deleted, not modified, in Task 19. The 5 ported commands are stateless with no shared mutable state requiring mutex/semaphore protection.
- **Elevated risk items specific to this phase:**
  1. **Task 19 (deletion) is irreversible in the working tree** (though recoverable via git history). Containment: gate it strictly behind all prior tasks being green AND the manual Discord smoke test passing, as specified. Rollback strategy: `git revert <task-19-commit-sha>` restores the deleted `.NET` tree from the previous commit; because this plan mandates one task = one commit with Task 19 isolated as the very last commit, reverting it does not touch any Go code committed in Tasks 1-18.
  2. **Data-fidelity risk in Tasks 7 and 9** (the weather city table and the today-info formatting) — these are large, detail-heavy ports where a transcription error (wrong lat/lon, wrong weather-code mapping, wrong punctuation in the formatted string) would silently change user-facing behavior without failing any test unless the tests assert the exact values. Containment: the test cases in this plan assert specific known values (e.g. weather_code 0 -> `快晴`, specific coordinates implied through formatted output) — the test-designer/implementer should consider adding a few more spot-check assertions (e.g. a table-driven test iterating a sample of city names against expected coordinates) during Task 7/9 implementation to catch transcription errors the plan's example tests might miss.
  3. **`.claude/hooks/enforce-codex-impl.mjs` (Task 16)** gates the *harness's* enforcement of the workflow itself — a mistake here doesn't break the bot but could silently disable (or overly aggressively enable) the main-thread edit guard. Containment: the manual verification step in Task 16 (attempt a blocked `.go` edit, attempt an allowed `.md` edit) should actually be performed by the orchestrator in a live session before trusting the new regex, not just code-reviewed.
- **No high-risk containment/rollback is needed for Tasks 1-14** (pure new-code Go additions with no existing behavior to regress) beyond the standard "each task is its own commit, revert if wrong."

---

## Commit boundaries

Each logical change gets its own commit (Japanese, imperative mood, per `CLAUDE.md`'s "1コミット=1論理変更、日本語・命令形" rule, `CLAUDE.md:93`) — most tasks are one commit each, but a few adjacent tasks bundle into a single commit where the earlier task alone would not be a meaningful checkpoint, as noted below:

- Tasks 1+2 bundle into one commit (empty `go.mod` alone is not a meaningful checkpoint).
- Tasks 3+4 bundle into one commit (config loader + its template file are one logical change).
- Tasks 5, 6, 8, 10, 11 (each command) are independently committable once their own tests pass — a partially-ported command set is a valid intermediate state since nothing yet depends on all 5 existing simultaneously (each self-registers independently; `main.go` from Task 12 works with however many are registered so far).
- Tasks 7 and 9 (the two external-API client packages) are independently committable before their corresponding command tasks (8, 10) — a client with no caller is still a valid, tested unit.
- Task 12 (`main.go`) is only meaningfully committable once at least one command exists to dispatch to — in practice this plan sequences it after all 5 commands (Tasks 5-11), so by the time Task 12 lands, the full command set is wired.
- Tasks 13, 14 (build/deploy tooling) are independently committable any time after Task 12 (they package the binary `main.go` produces).
- Tasks 15, 17, 18 (docs) are independently committable at any point once the Go stack exists to describe — but should logically land only once the code they describe is stable, i.e. after Task 14, to avoid documenting a moving target.
- Task 16 (hook repoint) is independently committable at any time (it's a one-line regex change unrelated to Go code existing yet) but is sequenced late here for narrative clarity — no technical dependency forces this ordering.
- **Task 19 (deletion) MUST be the last commit of this phase**, strictly after 1-18 are green and the manual Discord smoke test has passed, per the spec's explicit cutover condition (`Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md:116-119`).

The **whole phase** (all 19 tasks) is considered committable/mergeable to `main` once: `go build ./...`, `go vet ./...`, `go test ./...` are all clean; the manual Discord smoke test in Task 19 has passed with evidence recorded; `CLAUDE.md`/`AGENTS.md` are byte-identical; and the `.NET` project has been deleted with no dangling references.

---

## Self-review notes (checked before returning this plan)

- **Spec coverage check:** every section of `Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md` maps to a task — スコープ/5コマンド (Tasks 5,6,8,10,11), ディレクトリ構成 (file structure section + Tasks 1,2,7,9,12,13,14), コマンド登録の方式/自動探索 (Task 2, and every command task's `init()`), 設定・シークレット (Task 3,4), ビルド・配布 (Task 13,14), エラーハンドリング・ログ (Task 12's `log/slog` use, and each command's error-return convention), テスト・品質ゲート (per-task test steps + Verification section), ワークフロー/ツールの移行 (Tasks 15,16,17,18), カットオーバー (Task 19). Nothing in the spec is unaddressed.
- **Placeholder scan:** re-read every task's Go code blocks — no "add appropriate error handling" or "similar to Task N, do the same" shortcuts remain; Tasks 7, 8, 9, 10 (the longest) each have fully written-out code rather than a reference to another task.
- **Type/signature consistency:** `Command` interface (`Definition() *discordgo.ApplicationCommand`, `Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error`) is used identically in Task 2's definition, Tasks 5/6/8/10/11's five command implementations, and Task 12's `findCommandByName`/dispatch loop — verified no method-name drift (e.g. no task accidentally writes `Execute` instead of `Handle`).
- **Uncertain items still explicitly flagged (not hidden):** Go module path assumption (Task-0 notes), discordgo/Go versions (Task-0 notes), Go not installed on planning machine (Verification section), `.codex/agents/*.toml` in-scope-or-not (Task 18's UNCERTAIN bullet), CRLF-vs-LF for new `.go` files (Task 17's coding-style.md bullet), Task 8's `discordgo` API-shape assumption for testing interaction responses (resolved in-plan via the `buildResponseContent` extraction, avoiding the need for a discordgo-mocking test helper).
- **Post-review revision record:** an earlier draft of this plan was checked by two independent reviewers (a Claude plan-reviewer and a separate Codex CLI review) against the actual C# source, and both surfaced the same class of bug: several full-width Japanese punctuation characters (`？`/`（）`) in the ported `today`/`dice`/`weather` message templates and Discord command descriptions had been transcribed as ASCII (`?`/`()`). The Codex review additionally found that Task 9's original claim of matching `TodayService.cs`'s failure-handling behavior was actually wrong — the real .NET behavior on any single sub-API failure is a total command failure (traced through `.ContinueWith`/`.Result` semantics), not a per-section omission. All of these were independently re-verified against the actual C# source files by the orchestrator before this plan was corrected: Tasks 7, 8, 9, 10, 11, 12, and the commit-boundaries section above reflect the corrected content. See each task's "Data-fidelity note" / "Corrected failure-handling model" callouts for the specifics and source-line citations.

---

Relevant files read during investigation (all absolute paths):
- `C:\Users\<user>\Documents\todayistodayBot\Docs\superpowers\specs\2026-07-05-go-migration-phase-a-design.md`
- `C:\Users\<user>\Documents\todayistodayBot\CLAUDE.md`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\Commands\Handlers\PingCommand.cs`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\Commands\Handlers\HelpCommand.cs`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\Commands\Handlers\WeatherCommand.cs`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\Services\WeatherService.cs`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\Commands\Handlers\TodayCommand.cs`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\Services\TodayService.cs`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\Commands\Handlers\DiceCommand.cs`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\Commands\CommandService.cs`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\Commands\CommandContext.cs`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\Commands\ICommandHandler.cs`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\Program.cs`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\appsettings.json.template`
- `C:\Users\<user>\Documents\todayistodayBot\TodayIsTodayBot\TodayIsTodayBot.csproj`
- `C:\Users\<user>\Documents\todayistodayBot\todayistodayBot-1.sln`
- `C:\Users\<user>\Documents\todayistodayBot\Docs\agent-guide\architecture.md`
- `C:\Users\<user>\Documents\todayistodayBot\Docs\agent-guide\coding-style.md`
- `C:\Users\<user>\Documents\todayistodayBot\Docs\agent-guide\build-and-verify.md`
- `C:\Users\<user>\Documents\todayistodayBot\.claude\hooks\enforce-codex-impl.mjs`
- `C:\Users\<user>\Documents\todayistodayBot\.claude\agents\*.md` (all 6 files)
- `C:\Users\<user>\Documents\todayistodayBot\.gitignore`
- `C:\Users\<user>\Documents\todayistodayBot\.codex\agents\*.toml` (listing only, not full content)
