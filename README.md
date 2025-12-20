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

3. 環境変数の設定

Windowsの場合（PowerShell）:
```powershell
$env:DISCORD_BOT_TOKEN="あなたのDiscordボットトークン"
```

または、`.env`ファイルを作成して設定することもできます。

4. アプリケーションの実行
```bash
dotnet run
```

## 機能

- Discord.Netを使用したDiscord Bot
- 毎フレーム実行されるメインループ（約60FPS）
- デルタタイム計算による時間管理

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
