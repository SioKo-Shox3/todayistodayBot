# フェーズB設計: chosei-sama連携による日程調整・リマインダー機能

## 背景・目的

3つの独立した取り組み(A: 実行基盤/言語移行、B: chosei-sama連携への置き換え、C: カジノ/賭け事機能の新設)
のうち、フェーズA(.NET→Go/discordgo移行)は完了済み。本設計は **フェーズB** を対象とする。

現状、Go版Bot(フェーズA)には日程調整・リマインダー機能が存在しない(READMEには「フェーズB予定」と
明記済み)。本フェーズでは、外部サービス [chosei-sama](https://chosei-sama.choseisama.workers.dev/)
(リポジトリ: `C:\Users\<user>\Documents\chosei-sama`、Cloudflare Workers + Hono + D1、
**本番デプロイ済み**)のAPIを呼び出す形でこの機能を実装する。

## 基本方針

- **Botはchosei-samaに対する薄いクライアント**とする。候補生成・回答集計・リマインダー配信の
  ロジックはすべてchosei-sama側に委ね、Bot側は「Discordコマンド⇄chosei-sama API」の橋渡しと、
  作成したイベントの参照情報(イベントID・owner token)をチャンネルごとにローカル保持する役割に限定する。
- **回答(投票)はchosei-samaのWebフォームへ誘導する**。chosei-samaは候補ごとにok/maybe/ngで回答し、
  参加者名・コメントも付けられるリッチなモデルであり、Discordのリアクション/ボタンで再現するより
  Web誘導の方が機能を損なわず実装コストも低い。
- **リマインダーはchosei-sama自身のCron(30分おき)+Discord Incoming Webhookに任せる**。
  Bot側はチャンネルにWebhookを作成してchosei-samaへ登録するだけで、以降のタイマーロジックを
  一切持たない(フェーズAで削除した「自作タイマーループ」を再導入しない)。
- **chosei-sama側へのAPI追加は不要**と判断した。作成・公開読み取り・回答・Discordリマインダー登録の
  すべてが既存API(`POST /api/v1/events`、`GET /api/v1/events/:ref`、
  `PUT /api/v1/events/:ref/discord-reminder` 等)で賄える。「イベント一覧」APIが無い点は
  Bot側のローカルJSONで代替する(chosei-sama自体がログイン無しのURL共有モデルであり、
  一覧APIを持たない設計は意図的なもの)。

## コマンド構成

旧.NET版の命名(`/schedule` `/schedule-result` `/reminder`)を踏襲する(ユーザーの学習コストを
下げるため)。ネイティブDiscordスラッシュコマンド(フェーズAで確立した形式)として実装する。

### `/schedule <title> <start> <days> <time>`

- `title`(文字列、必須): イベントタイトル
- `start`(文字列、必須): 候補生成の起点。フェーズA以前の`/schedule`同様、送信日からの符号付き
  日数オフセット(例: `+1`)
- `days`(整数、必須、1〜50): 候補日数。chosei-samaの`candidatesPerEvent`上限(50)に合わせる
- `time`(文字列、必須): `HH:mm`形式の時刻。全候補に共通適用

`start`から`days`日分、毎日`time`時刻の候補を生成し、`ownerName`にコマンド実行者のDiscord表示名を
設定して`POST /api/v1/events`を呼ぶ。作成後、公開URL付きのEmbedをチャンネルに投稿し、
イベントID・owner token・channel IDをローカルJSONに保存する。

### `/schedule-result [event]`

`event`(文字列、任意): 対象イベントの参照(省略時はそのチャンネルで最後に作成したイベント)。
`GET /api/v1/events/:ref`(認証不要の公開読み取り)を呼び、参加者の回答状況・集計を埋め込みで表示する。

### `/reminder set [all-ok-hours] [deadline-hours] [mention-everyone]`

対象チャンネルにDiscord Incoming Webhookを作成(`session.ChannelWebhookCreate`。
Botに「Webhookの管理」権限が必要 — 運用要件としてREADME/デプロイ手順に明記する)し、
そのURLをchosei-samaの`PUT /api/v1/events/:ref/discord-reminder`に登録する。
オプション省略時はchosei-samaのデフォルト(all-OK候補: 3時間前有効、締切: 24時間前無効)に従う。

### `/reminder off`

同エンドポイントを`allOkEnabled: false, deadlineEnabled: false`で呼び直し、実質的に無効化する
(chosei-sama側にDiscord Webhook登録解除APIは無いため、フラグOFFで対応する)。

### `/reminder status`

`GET /api/v1/events/:ref/owner`(owner token認証)で現在の設定を表示する。

いずれのコマンドも対象イベントは「チャンネルの最新イベント」を既定とし、複数イベントを
同時に扱う場合のみ`event`引数で明示指定する。

## ローカル永続化

`data/chosei-events.json`を`sync.Mutex`で保護された単一ライター方式で読み書きする
(フェーズA以前の.NET版の設計思想を踏襲。`Docs/agent-guide/architecture.md`が
フェーズAで「持ち越された危険地帯」として既に言及している箇所)。

```go
type EventRecord struct {
    ChannelID  string    `json:"channel_id"`
    EventID    string    `json:"event_id"`
    PublicSlug string    `json:"public_slug"`
    OwnerToken string    `json:"owner_token"` // chosei-sama側の操作権限。Discordには絶対に出力しない
    CreatedAt  time.Time `json:"created_at"`
    CreatedBy  string    `json:"created_by"`  // Discord user ID
}
```

チャンネルごとに複数イベントを保持し、「省略時は最新」のルックアップは`ChannelID`+`CreatedAt`降順で行う。
`OwnerToken`は秘密情報であり、`config.json`同様`.gitignore`対象・ログ出力禁止とする。

## 新規パッケージ構成

```
internal/choseisama/client.go       chosei-sama APIクライアント(create/get/discord-reminder登録)
internal/choseisama/client_test.go
internal/store/events.go            data/chosei-events.json の読み書き(sync.Mutex単一ライター)
internal/store/events_test.go
internal/commands/schedule.go       /schedule
internal/commands/schedule_test.go
internal/commands/schedule_result.go /schedule-result
internal/commands/schedule_result_test.go
internal/commands/reminder.go       /reminder set/off/status
internal/commands/reminder_test.go
```

`internal/choseisama`はHTTP呼び出しのみを担当し、Discord固有の型(discordgo)に依存しない
(フェーズAの`internal/weather`・`internal/today`と同じ設計原則)。`internal/store`は
JSON永続化のみを担当し、chosei-samaのAPI形状を知らない。

## エラーハンドリング

chosei-sama APIの認証エラー(401)・バリデーションエラー(400)・レート制限(429)は、
ユーザー向けには日本語の簡潔なメッセージ(例: 「❌ 日程調整の作成に失敗しました」)に変換し、
詳細はログにのみ出力する(フェーズAの`weather`/`today`クライアントと同じ方針)。
ローカルJSONにイベントが見つからない場合(`/schedule-result`や`/reminder`をイベント未作成の
チャンネルで実行した場合)は「❌ このチャンネルではまだ日程調整イベントが作成されていません」を返す。

## テスト方針

`internal/choseisama`は`httptest.Server`でchosei-sama APIをモックした単体テスト。
`internal/store`は一時ディレクトリでの読み書きテスト(同時書き込みの排他も含む)。
コマンドハンドラは、フェーズAの`weather`コマンドで確立した「`Handle()`から純粋関数を切り出して
テストする」パターン(discordgoの実インタラクションをモックしない)を踏襲する。

## スコープ外

- Discordのリアクション/ボタンによるDiscord内完結の回答体験(将来的な拡張候補として除外)
- chosei-sama側へのAPI追加・変更(不要と判断)
- 複数リマインダー設定の同時管理(chosei-samaのモデルが1イベント1リマインダー設定のため)
- カジノ/賭け事機能(フェーズC)
