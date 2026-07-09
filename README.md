# TodayIsTodayBot

Discord用の応答Botです。

## セットアップ

### 必要なもの
- `go.mod` の `go` ディレクティブが指定するバージョンの Go toolchain
- Discordボットトークン

### インストール手順

1. リポジトリをクローン
```bash
git clone https://github.com/SioKo-Shox3/todayistodayBot.git
cd todayistodayBot
```

2. 設定ファイルの準備
```bash
cp config.json.template config.json
```

3. `config.json` を編集して、Discordボットトークンを設定

```json
{
  "discord_token": "あなたのDiscordボットトークンをここに入力"
}
```

⚠️ **重要**: `config.json`は機密情報を含むため、`.gitignore`に追加されています。絶対にGitにコミットしないでください。

代わりに `DISCORD_TOKEN` 環境変数でトークンを渡すこともできます（環境変数が `config.json` より優先されます）。

4. アプリケーションのビルドと実行
```bash
go build ./cmd/bot
./bot
```

または `make build && ./bin/todayistodaybot`、開発中は `go run ./cmd/bot` でも実行できます。

## 利用可能なコマンド

### 基本コマンド
- `/ping` - ボットが応答しているか確認
- `/help` - 利用可能なコマンド一覧を表示
- `/today [date]` - 今日は何の日かを表示（例: `/today` または `/today 0101` で1月1日の情報）
  - `date`: MMdd形式の日付（例: `0101` = 1月1日）。省略時は今日
- `/dice [sides] [rolls]` - サイコロを振る（例: `/dice 6 3` で6面ダイスを3回振る）
  - `sides`: 面数（2以上、既定6）
  - `rolls`: 回数（1以上100以下、既定1）

### 天気情報
- `/weather [地域名]` - 指定した地域の天気情報を取得（例: `/weather 東京`）

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

## 機能

### コマンドシステム
- `/` から始まるコマンドを認識
- 拡張可能なコマンドハンドラアーキテクチャ
- エラーハンドリングとログ機能

### 天気情報取得
- Open-Meteo APIを使用（APIキー不要）
- 日本全国47都道府県に対応
- リアルタイムの気温、湿度、風速などを表示

### 日程調整システム（chosei-sama連携）
- 候補生成・回答集計・リマインダー配信のロジックは外部サービス [chosei-sama](https://chosei-sama.choseisama.workers.dev/) に委譲
- Botは「Discordコマンド ⇄ chosei-sama API」の橋渡しと、作成したイベントの参照情報(イベントID・owner token)をチャンネルごとにローカル保持する役割のみを持つ
- ローカル保持先: `data/chosei-events.json`（`sync.Mutex`で保護された単一ライター。`.gitignore`対象 — owner tokenを含むため絶対にコミットしない）

### メインループシステム
- discordgo（Go）を使用したDiscord Bot
- スラッシュコマンド（Interaction）ベースのイベント駆動処理
- JSON設定ファイル・環境変数による構成管理

## プロジェクト構造

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

## 開発

### 新しいコマンドの追加

1. `internal/commands/` に新しいコマンドファイルを作成
2. `Command` インターフェース（`Definition()` / `Handle()`）を実装
3. ファイル内の `init()` で `Register(&YourCommand{})` を呼んで自己登録（`main.go` の編集は不要）

```go
package commands

import "github.com/bwmarrin/discordgo"

func init() { Register(&YourCommand{}) }

type YourCommand struct{}

func (c *YourCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "yourcommand",
		Description: "説明",
	}
}

func (c *YourCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "応答メッセージ",
		},
	})
}
```

### ブランチ戦略
- `develop`: 開発用ブランチ（デフォルト）

## ライセンス

MIT
