# TodayIsTodayBot

Discord用の応答Botです。

## セットアップ

### 必要なもの
- .NET 8.0 SDK以降
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

3. 設定ファイルの作成

`appsettings.example.json` をコピーして `appsettings.json` を作成します。

Windowsの場合（PowerShell）:
```powershell
Copy-Item appsettings.example.json appsettings.json
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

5. アプリケーションの実行
```bash
dotnet run
```

## 機能

- Discord.Netを使用したDiscord Bot
- 毎フレーム実行されるメインループ（設定可能なFPS）
- デルタタイム計算による時間管理
- JSON設定ファイルによる構成管理

## 設定

`appsettings.json` で以下の設定が可能です：

- `Discord:BotToken`: Discordボットのトークン（必須）
- `Bot:FrameRate`: メインループのフレームレート（デフォルト: 60）

## プロジェクト構造

```
TodayIsTodayBot/
├── Program.cs                    # メインプログラム
├── appsettings.example.json      # 設定ファイルのサンプル
└── TodayIsTodayBot.csproj       # プロジェクトファイル
```

## 開発

### ブランチ戦略
- `main`: 本番用ブランチ
- `develop`: 開発用ブランチ

### メインループについて

`MainLoopAsync()`メソッドが毎フレーム実行されるルーチンです。
- フレームレート: 約60FPS（16msごとに更新）
- デルタタイム計算による時間管理
- `UpdateAsync()`メソッドに毎フレームの処理を記述

## ライセンス

MIT
