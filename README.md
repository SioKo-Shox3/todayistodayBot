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

### 天気情報
- `/weather [地域名]` - 指定した地域の天気情報を取得（例: `/weather 東京`）

### 日程調整（フェーズB予定・現時点では未実装）
> ⚠️ 以下の `/schedule` `/schedule-result` コマンドは、フェーズBでchosei-sama連携として
> 再実装予定の機能であり、**現在のGo実装にはまだ含まれていません**。

- `/schedule [基準日] [日数] [時刻]` - 日程調整アンケートを作成
  - 例: `/schedule +1 3 19:00` → 明日から3日分、各日19:00でアンケート作成
  - 例: `/schedule 0 5 20:00` → 今日から5日分、各日20:00でアンケート作成
  - 基準日: +数字（○日後）、-数字（○日前）、0（今日）
  - 日数: 1〜10個まで指定可能
  - 曜日も自動表示
  - リアクション（数字の絵文字）で投票
- `/schedule-result [poll-id]` - アンケート結果を表示
  - 全員が参加可能な日程を自動判定
  - 各日程の投票状況を可視化

### スケジュールリマインダー（フェーズB予定・現時点では未実装）
> ⚠️ 以下の `/reminder` コマンドは、フェーズBでchosei-sama連携として再実装予定の機能であり、
> **現在のGo実装にはまだ含まれていません**。

- `/reminder set [poll-id]` - このチャンネルにリマインダーを設定
- `/reminder enable [poll-id]` - リマインダーを有効化
- `/reminder disable [poll-id]` - リマインダーを無効化
- `/reminder list` - 設定済みリマインダー一覧を表示
- `/reminder delete [poll-id]` - リマインダーを削除
  - 全員が参加可能な日程の開始時間に @everyone で自動通知
  - 通知はリマインダー設定時のチャンネルに送信
  - 1分間隔でチェック（開始時間の±5分以内に通知）

## 機能

### コマンドシステム
- `/` から始まるコマンドを認識
- 拡張可能なコマンドハンドラアーキテクチャ
- エラーハンドリングとログ機能

### 天気情報取得
- Open-Meteo APIを使用（APIキー不要）
- 日本全国47都道府県に対応
- リアルタイムの気温、湿度、風速などを表示

### 日程調整システム（フェーズB予定・現時点では未実装）
> ⚠️ 日程調整・リマインダー機能はフェーズBでchosei-sama連携として再実装予定(現時点では未実装)。
> 以下は将来の構想であり、現在のGo実装には含まれていません。

- Discordのリアクション機能を活用したアンケート作成
- JSONファイルによる投票データの永続化
- 全員が参加可能な日程の自動判定
- 投票状況の可視化（進捗バー表示）

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
│   │   └── dice.go
│   ├── config/                  # 設定読み込み（config.json / 環境変数）
│   │   └── config.go
│   ├── weather/                 # Open-Meteo クライアント
│   │   ├── client.go
│   │   └── cities.go
│   └── today/                   # 今日は何の日 API クライアント
│       └── client.go
├── deploy/                      # Dockerfile・systemd unit
├── Makefile                     # ビルド/テスト/Docker イメージ用タスク
├── config.json.template         # 設定ファイルのテンプレート
└── go.mod
```

## 設定

`config.json`（または `DISCORD_TOKEN` 環境変数）で以下の設定が可能です：

- `discord_token`: Discordボットのトークン（必須。環境変数 `DISCORD_TOKEN` が設定されている場合はそちらが優先）

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
