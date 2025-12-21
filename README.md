# TodayIsTodayBot

Discord用の応答Botです。

## セットアップ

### 必要なもの
- .NET 9.0 SDK以降
- Discordボットトークン

### インストール手順

1. リポジトリをクローン
```bash
git clone https://github.com/SioKo-Shox3/todayistodayBot.git
cd todayistodayBot/TodayIsTodayBot
```

2. 必要なパッケージの復元
```bash
dotnet restore
```

3. 設定ファイルの準備
```bash
cp appsettings.json.template appsettings.json
```

4. `appsettings.json` を編集して、Discordボットトークンを設定

```json
{
  "Discord": {
    "BotToken": "あなたのDiscordボットトークンをここに入力"
  },
  "Bot": {
    "FrameRate": 60
  }
}
```

⚠️ **重要**: `appsettings.json`は機密情報を含むため、`.gitignore`に追加されています。絶対にGitにコミットしないでください。

5. アプリケーションの実行
```bash
dotnet run
```

## 利用可能なコマンド

### 基本コマンド
- `/ping` - ボットが応答しているか確認
- `/help` - 利用可能なコマンド一覧を表示

### 天気情報
- `/weather [地域名]` - 指定した地域の天気情報を取得（例: `/weather 東京`）

### 日程調整
- `/schedule [日程1], [日程2], ...` - 日程調整アンケートを作成
  - 例: `/schedule 2025-01-15 19:00, 2025-01-16 19:00, 2025-01-17 20:00`
  - カンマ区切りで複数の日程を指定（最大10個）
  - リアクション（数字の絵文字）で投票
- `/schedule-result [poll-id]` - アンケート結果を表示
  - 全員が参加可能な日程を自動判定
  - 各日程の投票状況を可視化

## 機能

### コマンドシステム
- `/` から始まるコマンドを認識
- 拡張可能なコマンドハンドラアーキテクチャ
- エラーハンドリングとログ機能

### 天気情報取得
- Open-Meteo APIを使用（APIキー不要）
- 日本全国47都道府県に対応
- リアルタイムの気温、湿度、風速などを表示

### 日程調整システム
- Discordのリアクション機能を活用したアンケート作成
- JSONファイルによる投票データの永続化
- 全員が参加可能な日程の自動判定
- 投票状況の可視化（進捗バー表示）

### メインループシステム
- Discord.Netを使用したDiscord Bot
- 毎フレーム実行されるメインループ（設定可能なFPS）
- デルタタイム計算による時間管理
- JSON設定ファイルによる構成管理

## プロジェクト構造

```
TodayIsTodayBot/
├── Commands/                    # コマンド処理
│   ├── ICommandHandler.cs       # コマンドハンドラインターフェース
│   ├── CommandContext.cs        # コマンド実行コンテキスト
│   ├── CommandService.cs        # コマンド管理サービス
│   └── Handlers/                # 各コマンドハンドラ
│       ├── PingCommand.cs
│       ├── HelpCommand.cs
│       ├── WeatherCommand.cs
│       ├── ScheduleCommand.cs
│       └── ScheduleResultCommand.cs
├── Handlers/                    # イベントハンドラ
│   ├── MessageHandler.cs
│   └── ReactionHandler.cs
├── Models/                      # データモデル
│   └── SchedulePoll.cs
├── Services/                    # 外部サービス連携
│   ├── WeatherService.cs
│   └── ScheduleStorageService.cs
├── Program.cs                   # メインプログラム
├── appsettings.json.template    # 設定ファイルのテンプレート
└── TodayIsTodayBot.csproj       # プロジェクトファイル
```

## 設定

`appsettings.json` で以下の設定が可能です：

- `Discord:BotToken`: Discordボットのトークン（必須）
- `Bot:FrameRate`: メインループのフレームレート（デフォルト: 60）

## 開発

### 新しいコマンドの追加

1. `Commands/Handlers/`に新しいコマンドクラスを作成
2. `ICommandHandler`インターフェースを実装
3. `Program.cs`の`RegisterCommands()`メソッドに登録

```csharp
public class YourCommand : ICommandHandler
{
    public string CommandName => "yourcommand";
    public string Description => "説明";
    
    public async Task ExecuteAsync(SocketMessage message, string[] args)
    {
        // コマンドの処理
        await message.Channel.SendMessageAsync("応答メッセージ");
    }
}
```

### ブランチ戦略
- `develop`: 開発用ブランチ（デフォルト）

## ライセンス

MIT
