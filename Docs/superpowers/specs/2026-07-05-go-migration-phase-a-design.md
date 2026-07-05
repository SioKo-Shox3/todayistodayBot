# フェーズA設計: .NET/Discord.Net → Go/discordgo 移行

## 背景・目的

TodayIsTodayBot(Discord用日本語応答Bot)は現在 .NET 9.0 / Discord.Net で実装されているが、
実運用先はLinux環境であり、.NETのビルド・デプロイの手間が課題になっている。

3つの独立した取り組み(A: 実行基盤/言語移行、B: chosei-sama連携への置き換え、C: カジノ/賭け事機能の新設)
のうち、本設計は **フェーズA(実行基盤/言語移行)** を対象とする。B・Cはこのフェーズの上に積む。

## スコープ

- 移行先: **Go + discordgo**
- コマンドUX: メッセージ内`/`テキストコマンドから、**Discordネイティブのスラッシュコマンド(Interactions API)** へ刷新
- 移植対象は現行コマンドのうち **ステートレスな5つ**: `ping` / `help` / `weather` / `today` / `dice`
- **`schedule` / `schedule-result` / `reminder` はフェーズAでは実装しない**
  (JSON永続化・リアクション投票・自作タイマーループを含む既存ロジックは移植せず、フェーズBでchosei-sama連携として新規実装する。使い捨てになる移植作業を避けるため)
- リポジトリは同じrepo内で即時置き換え(並行ディレクトリでの移行期間は設けない)
- デプロイは「素のバイナリ+systemd」と「Dockerコンテナ」の両方を用意し、ビルドターゲットはamd64/arm64の両方に対応する(実デプロイ先未定のため)

## アーキテクチャ概要

現行のhand-rolled FPSメインループ(`Program.MainLoopAsync`、フレームカウントでリマインダーチェックを駆動する仕組み)は完全に廃止する。
discordgoの`Session`がGatewayのWebSocket接続とイベント配送をgoroutineベースで内部処理するため、
Botプロセスは以下のシンプルな構造になる:

1. 設定読み込み(env優先、ファイルfallback)
2. discordgo Sessionを生成し、コマンドを登録(Application Command登録 + Interactionディスパッチのハンドラ登録)
3. Sessionを開始し、SIGINT/SIGTERMを待機してgraceful shutdown

フェーズAでは永続化層は不要(移植対象の5コマンドはすべてステートレス/外部API呼び出しのみ)。

## ディレクトリ構成

```
/cmd/bot/main.go            エントリポイント
/internal/config/           env優先・ファイルfallbackの設定読み込み
/internal/commands/
    registry.go              コマンド登録レジストリ(下記「コマンド登録の方式」参照)
    ping.go / help.go / weather.go / today.go / dice.go
/internal/weather/          Open-Meteoクライアント(WeatherServiceの移植)
/internal/today/            WhatIsToday APIクライアント(TodayServiceの移植)
/deploy/
    Dockerfile
    systemd/todayistodaybot.service
Makefile
```

## コマンド登録の方式(自動探索)

現行CLAUDE.mdの絶対規則②は「新コマンドは`Program.RegisterCommands()`に登録する(自動探索は無い、登録漏れ=無反応)」だが、
**Go版では自動探索方式に変更する**(ユーザー承認済み)。

Go の `init()` による自己登録パターンを採用する:

```go
// internal/commands/registry.go
type Command interface {
    Definition() *discordgo.ApplicationCommand
    Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error
}

var registered []Command

func Register(cmd Command) { registered = append(registered, cmd) }
func All() []Command        { return registered }
```

```go
// internal/commands/weather.go
func init() { Register(&WeatherCommand{}) }
```

`internal/commands`パッケージ配下に新しい`.go`ファイルを追加し、そのファイルの`init()`で`Register(...)`を呼ぶだけで、
`main.go`側の一覧を編集せずに新コマンドが登録される(パッケージがインポートされた時点で全ファイルの`init()`が実行されるため)。
`main.go`は`commands.All()`を走査して、Discordへのコマンド定義登録と`InteractionCreate`のディスパッチ(コマンド名一致)を行う。

`/schedule`・`/reminder`はフェーズAでは`internal/commands`に存在しない(=登録もされない)。

## 設定・シークレット

環境変数(`DISCORD_TOKEN`など)を優先し、未設定なら設定ファイル(デフォルト`./config.json`、`CONFIG_PATH`で変更可)にフォールバック。
両方に無ければ起動時に即座にエラー終了(fail fast)。

## ビルド・配布

`Makefile`に以下を用意する:

- `build`: ホストOS向けビルド
- `build-linux-amd64` / `build-linux-arm64`: `GOOS=linux GOARCH=... CGO_ENABLED=0 go build`によるクロスコンパイル
- `docker-build`: マルチステージDockerfile(`golang:alpine`でビルド→`scratch`または`alpine`に静的バイナリのみコピー)

`deploy/systemd/todayistodaybot.service`テンプレート(`Restart=on-failure`、非rootユーザー実行、`EnvironmentFile`任意指定)も同梱する。

## エラーハンドリング・ログ

標準ライブラリ`log/slog`で構造化ログ(追加の外部依存なし)。コマンドハンドラは`error`を返し、
失敗時はログに詳細を出しつつ、ユーザーには現行同様の日本語の一般的エラーメッセージをインタラクション応答として返す。

## テスト・品質ゲート

`go vet ./...` / `go build ./...` / `go test ./...` を「エラー0・警告0・テスト成功」のDone条件とする
(現行の「dotnet build 警告0」規律を踏襲)。WeatherService/TodayServiceの移植先は`httptest.Server`でモックした単体テストを書く。
discordgo依存部分は薄く保ち、レスポンス整形などのロジックは分離してテスト可能にする。

## ワークフロー/ツールの移行(本フェーズに含む作業)

- `CLAUDE.md`/`AGENTS.md`のスタック記述を.NET→Goに更新
  - 絶対規則②を「新コマンドは`internal/commands`にファイルを追加し`init()`で自己登録する(自動探索)」に書き換え
  - 絶対規則③(async/await必須)をGoの並行性規律(goroutine/channel、将来永続化を持つ場合のmutex/排他)に書き換え
  - 絶対規則④(dotnet build警告0)を`go vet`/`go build`/`go test`クリーンに書き換え
- `.claude/hooks/enforce-codex-impl.mjs`: 現在`.cs`をブロック対象にしているのを`.go`に付け替え
- `Docs/agent-guide/architecture.md` / `coding-style.md` / `build-and-verify.md` をGoスタック向けに書き直し
- `.claude/agents/*`内の.NET固有記述があれば洗い出して更新

## カットオーバー

同じrepo内で即時置き換え。Go版5コマンドの実装・テスト・手動疎通確認(実際のDiscordサーバーでの動作確認)が完了した時点で、
同一の変更の中で.NETプロジェクト一式を削除し、CLAUDE.md/AGENTS.md/Docsも合わせて更新する(gitの履歴には.NET版が残る)。

## 本フェーズのスコープ外(次フェーズ以降)

- chosei-sama連携によるスケジュール調整・リマインダー機能(フェーズB)
- カジノ/賭け事機能(フェーズC)
- ネイティブスラッシュコマンドのオートコンプリート等の高度なUX拡張(必要になれば都度追加)
