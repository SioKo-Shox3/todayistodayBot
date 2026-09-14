# PROGRESS — todayistodayBot

セッション/反復の引き継ぎ。毎回の開始儀式で最初に読み、反復の終わりに更新する。
`git log` が第二の記録。ここには git に無いこと(判断・未解決・次に見るべき場所)を書く。

## Done
- フェーズ C-1(カジノ経済基盤+スロット)の実装 Task 1〜19 は `feat/casino-c1` に着地済み(2026-07-27、9 コミット)。レビューは 2026-09-14 に開始。

## In progress
- (いま手を付けているタスク id と、どこまで進んだか)

## Next
- Astra の C-1 レビュー(`.harness/reviews/2026-09-14-astra-casino-c1-round1.md`)の所見を R 系タスクにして消化 → 2 周目 PASS → `main` へ ff マージ(ユーザー承認済み 2026-09-14)→ 片付け。稼働(トークン・実行場所)は後日、ユーザー判断。

## Notes
- **レビュー 1 周目の non-blocking で残すもの(残課題)**: 9 時掲示の「送信成功 → MarkAnnounced」の間で落ちると同日再起動で再掲示する(障害をまたぐ必ず 1 回は未保証)/ 送信中の ctx cancel に停止期限が無い。どちらも通常運転では起きず、設計判断を要するので C-2 の設計に回す。
- **開始儀式(2026-09-14、`feat/casino-c1` = 92594d9)**: `go build ./... && go vet ./... && go test ./...` → 8 パッケージ ok。`-race` は cgo 無しで不可(既知)。Git Bash に `make` と `go` が無い — `go` は `C:/Program Files/Go/bin/go`、`make build` の代わりは `go build -o bin/todayistodaybot ./cmd/bot`。
- `main` は .NET 時代(go.mod 無し)。`feat/casino-c1` はフェーズ A/B/C-1 を全部持ち、`main` へ ff 可能。
- `config.json` / `DISCORD_TOKEN` は未設定(稼働時にユーザーが置く。エージェントは触らない)。
