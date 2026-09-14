# PROGRESS — todayistodayBot

セッション/反復の引き継ぎ。毎回の開始儀式で最初に読み、反復の終わりに更新する。
`git log` が第二の記録。ここには git に無いこと(判断・未解決・次に見るべき場所)を書く。

## Done
- フェーズ C-1(カジノ経済基盤+スロット)の実装 Task 1〜19 は `feat/casino-c1` に着地済み(2026-07-27、9 コミット)。レビューは 2026-09-14 に開始。
- R-001(`/daily` の日付境界二重受給)= caa2753。`ClaimDaily` は受給日が最後の受給日以下なら `ErrAlreadyClaimedToday`(受給日は単調非減少)、時刻は `Store.clock` からロック内で取る(引数の `now` を廃止)。検証出力: `.harness/runs/20260914-105906/verify-R-001-{1,2,3}.txt`(build+vet clean / casino 58 PASS / commands TestDaily 2 PASS)。
- R-002(`EnsureTodayRate` の順序逆転)= 20ed1a0。レート履歴は前へしか進まない: 渡された日付が履歴末尾の日付以下なら `settledRateLocked` がその日の記録(無ければ最新の記録)を返し、追加も抽選もしない。`RecentRates` は返ったレートの日付より後のエントリを落とすので `/rate` の見出しと `/exchange` の請求が一致する。検証出力: `.harness/runs/20260914-105906/verify-R-002-{1,2,3,4}.txt`(build+vet clean / casino 87 PASS / commands TestRate・TestExchange 12 PASS / `go test ./...` 8 パッケージ ok)。

- R-002 の評価者指摘(反復 2 の `NEEDS_WORK`)= 33d6bfa。逆順で保存された履歴(`D, D+1, D`)でも確定済みの日を再抽選しない: 抽選の前に履歴**全体**を日付で検索する(末尾比較だけでは D+1 が既にあることを見落とす)。`RecentRates` は日付ではなく**返ったレートの位置**で切り詰めるので、後ろの要素が前の日付を持つ履歴でも /rate の見出しと /exchange の請求が一致する。検証出力: `.harness/runs/20260914-105906/verify-R-002-{5,6,7,8}.txt`(build+vet clean / casino 71 PASS / commands TestRate・TestExchange 16 PASS / `go test ./...` 8 パッケージ ok)。
- R-003(Interaction トークンのログ流出)= 72db222。`casino_shared.go` に `redactInteractionError(err) string` を置き、`internal/commands` の `slog` 呼び出し **28 箇所すべて**を通した。`*url.Error` は `Op` と原因だけにして URL を捨て、それ以外は `/interactions/<id>/<token>` と `/webhooks/<id>/<token>` のトークン部分を `[redacted]` に置換する。9 時掲示は本番配線(`StartCasinoAnnounceScheduler` の送信クロージャ)で包んだ — ログ行自体は `internal/casino/announce.go`(R-003 の `paths:` 外)にあるため。検証出力: `.harness/runs/20260914-105906/verify-R-003-{1,2,3}.txt`(build+vet clean / commands 141 PASS・FAIL 0 / `go test ./...` 8 パッケージ ok)。

## In progress
- (いま手を付けているタスク id と、どこまで進んだか)

## Next
- R-004(配備設定で保存先 `data/` に書けるようにする)。`TASKS.md` の未完はこれ 1 件。
- Astra の C-1 レビュー(`.harness/reviews/2026-09-14-astra-casino-c1-round1.md`)の所見を R 系タスクにして消化 → 2 周目 PASS → `main` へ ff マージ(ユーザー承認済み 2026-09-14)→ 片付け。稼働(トークン・実行場所)は後日、ユーザー判断。

## Notes
- **R-003 で `paths:` の外に残した 1 件**: `internal/casino/announce.go` の `slog.Error("casino: announcement callback failed", ...)` は、コールバックが返したエラーをそのまま出す。本番のコールバック(`internal/commands/casino_announce.go`)が中で秘匿化してから返すので実害は無いが、`internal/casino` を直に使う別の呼び出し元が現れたら素通しになる。`internal/casino` 側にも同等のヘルパーを置くかは C-2 で判断する。
- **トークン秘匿の規律**: `internal/commands` で `slog` にエラーを渡すときは必ず `redactInteractionError(err)` を通す。新しいログ行を足すときも同じ(`"error", err` を直接渡さない)。
- **時刻注入の形(R-001 で決めた)**: `Store` に非公開の `clock func() time.Time`(nil = `time.Now`)を持たせ、`nowLocked()` を `Update` のクロージャ内からだけ読む(`rng` と同じ規律)。テストは同パッケージの `st.at(tm)` で差し込む。並行テストは goroutine 起動前に 1 回だけ `at` を呼ぶ(クロージャ書き換えは競合する)。`internal/commands` からは時刻を差せない(フィールドが非公開)ので、コマンド側テストは既定の実時計のまま。
- **R-002 で `paths:` の外に残した 1 件**: 9 時掲示(`announce.go`)は `Today` と `RecentRates` を生の履歴から組むので、時計が巻き戻った日だけ見出しとスパークラインの最終日がずれうる(`RecentRates` と同じ切り詰めを入れれば消える)。掲示は 1 日 1 回・`LastAnnounced` で守られており表示だけの問題。C-2 か announce を触る次のタスクで拾う。
- **レビュー 1 周目の non-blocking で残すもの(残課題)**: 9 時掲示の「送信成功 → MarkAnnounced」の間で落ちると同日再起動で再掲示する(障害をまたぐ必ず 1 回は未保証)/ 送信中の ctx cancel に停止期限が無い。どちらも通常運転では起きず、設計判断を要するので C-2 の設計に回す。
- **開始儀式(2026-09-14、`feat/casino-c1` = 92594d9)**: `go build ./... && go vet ./... && go test ./...` → 8 パッケージ ok。`-race` は cgo 無しで不可(既知)。Git Bash に `make` と `go` が無い — `go` は `C:/Program Files/Go/bin/go`、`make build` の代わりは `go build -o bin/todayistodaybot ./cmd/bot`。
- `main` は .NET 時代(go.mod 無し)。`feat/casino-c1` はフェーズ A/B/C-1 を全部持ち、`main` へ ff 可能。
- `config.json` / `DISCORD_TOKEN` は未設定(稼働時にユーザーが置く。エージェントは触らない)。
