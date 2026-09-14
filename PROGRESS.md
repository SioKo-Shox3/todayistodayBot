# PROGRESS — todayistodayBot

セッション/反復の引き継ぎ。毎回の開始儀式で最初に読み、反復の終わりに更新する。
`git log` が第二の記録。ここには git に無いこと(判断・未解決・次に見るべき場所)を書く。

## Done
- フェーズ C-1(カジノ経済基盤+スロット)の実装 Task 1〜19 は `feat/casino-c1` に着地済み(2026-07-27、9 コミット)。レビューは 2026-09-14 に開始。
- R-001(`/daily` の日付境界二重受給)= caa2753。`ClaimDaily` は受給日が最後の受給日以下なら `ErrAlreadyClaimedToday`(受給日は単調非減少)、時刻は `Store.clock` からロック内で取る(引数の `now` を廃止)。検証出力: `.harness/runs/20260914-105906/verify-R-001-{1,2,3}.txt`(build+vet clean / casino 58 PASS / commands TestDaily 2 PASS)。
- R-002(`EnsureTodayRate` の順序逆転)= 20ed1a0。レート履歴は前へしか進まない: 渡された日付が履歴末尾の日付以下なら `settledRateLocked` がその日の記録(無ければ最新の記録)を返し、追加も抽選もしない。`RecentRates` は返ったレートの日付より後のエントリを落とすので `/rate` の見出しと `/exchange` の請求が一致する。検証出力: `.harness/runs/20260914-105906/verify-R-002-{1,2,3,4}.txt`(build+vet clean / casino 87 PASS / commands TestRate・TestExchange 12 PASS / `go test ./...` 8 パッケージ ok)。

- R-002 の評価者指摘(反復 2 の `NEEDS_WORK`)= 33d6bfa。逆順で保存された履歴(`D, D+1, D`)でも確定済みの日を再抽選しない: 抽選の前に履歴**全体**を日付で検索する(末尾比較だけでは D+1 が既にあることを見落とす)。`RecentRates` は日付ではなく**返ったレートの位置**で切り詰めるので、後ろの要素が前の日付を持つ履歴でも /rate の見出しと /exchange の請求が一致する。検証出力: `.harness/runs/20260914-105906/verify-R-002-{5,6,7,8}.txt`(build+vet clean / casino 71 PASS / commands TestRate・TestExchange 16 PASS / `go test ./...` 8 パッケージ ok)。
- R-003(Interaction トークンのログ流出)= 72db222。`casino_shared.go` に `redactInteractionError(err) string` を置き、`internal/commands` の `slog` 呼び出し **28 箇所すべて**を通した。`*url.Error` は `Op` と原因だけにして URL を捨て、それ以外は `/interactions/<id>/<token>` と `/webhooks/<id>/<token>` のトークン部分を `[redacted]` に置換する。9 時掲示は本番配線(`StartCasinoAnnounceScheduler` の送信クロージャ)で包んだ — ログ行自体は `internal/casino/announce.go`(R-003 の `paths:` 外)にあるため。検証出力: `.harness/runs/20260914-105906/verify-R-003-{1,2,3}.txt`(build+vet clean / commands 141 PASS・FAIL 0 / `go test ./...` 8 パッケージ ok)。
- R-003 の評価者指摘(反復 3 の `NEEDS_WORK`)= 3da727f。(1) ハンドラの戻り値も秘匿する: `cmd/bot` が `Handle` の戻り値をそのまま `slog` に渡すので、`internal/commands` の 19 箇所の `s.InteractionRespond` を共通の `respond(s, interaction, resp)` へ寄せ、返すエラーを `redactInteractionError` に通した。(2) `*url.Error` は `Op` と原因の**型**だけにした(原因のメッセージにもトークンが入りうる)。回帰は `respond_test.go` — 戻り値をハンドラ経由で `slog` に流す検査と、`s.InteractionRespond` 直呼びを禁じるソース走査。検証出力: `.harness/runs/20260914-105906/verify-R-003fix-{1,2,3}.txt`(build+vet clean / 該当テスト 6+2+1 PASS / `go test ./...` 8 パッケージ ok)。
- R-004(配備先で `data/` に書けない)= 3d829b4。unit に `ReadWritePaths=/opt/todayistodaybot/data`、Dockerfile に `WORKDIR /app` + `botuser` 所有の `/app/data` + `VOLUME`、README に「データ保存先（配備時）」節。検証出力: `.harness/runs/20260914-105906/verify-R-004-{1,2,3}.txt`。

## In progress
- (なし)

## Next
- **C-2 開始(2026-09-14)**: 仕様 `Docs/superpowers/specs/2026-09-14-casino-c2-design.md`、タスク C2-01〜C2-08(`TASKS.md`)。ブランチ `feat/casino-c2`。順に消化する。
- **C-1 は完了**(2026-09-14: R-001〜R-004 着地、Astra 2 周目 PASS、`main` へ ff マージ)。次は稼働(トークンと実行場所はユーザー判断)か C-2(ボタン基盤+ハイ&ロー+ブラックジャック、未設計 → M1 から)。
- `TASKS.md` の未完は無し(R-001〜R-004 すべて done)。次は R-003 修正差分の評価者 2 周目(前回指摘への対応差分だけを見る)。
- Astra の C-1 レビュー(`.harness/reviews/2026-09-14-astra-casino-c1-round1.md`)の所見を R 系タスクにして消化 → 2 周目 PASS → `main` へ ff マージ(ユーザー承認済み 2026-09-14)→ 片付け。稼働(トークン・実行場所)は後日、ユーザー判断。

## Notes
- **2 周目で残った non-blocking(残課題)**: 掲示送信成功〜`MarkAnnounced` 保存の間の障害で再掲示 / 送信中 cancel の停止期限なし / 時計巻き戻り時の掲示見出しと履歴末尾のずれ(表示のみ)/ 実配備での書き込み確認と Docker ビルドは未検証(この PC に Docker 無し)。
- **応答の規律(R-003 修正で決めた)**: `internal/commands` のハンドラは `s.InteractionRespond` を直接呼ばず `respond()` を通す。`cmd/bot` が戻り値をログへ出すので、直呼びはトークンをログへ戻す。`respond_test.go` の `TestNoDirectInteractionRespond` がソース走査で禁じている(`casino_shared.go` だけ除外 — そこが実装)。
- **配備の前提(R-004)**: 保存先は作業ディレクトリ相対の `data/` 固定(上書きする環境変数は無い)。systemd は `/opt/todayistodaybot/data` を**人が先に作って chown する**必要がある(unit は作らない。`StateDirectory=` にするならコード側に保存先の環境変数が要る — C-2 の判断)。`systemd-analyze verify` はこの PC では `ExecStart` のバイナリが無いので必ず `exit=1` になる。構文だけ見るときは `ExecStart` を `/bin/true` に差し替えた写しを検証する。
- **`docker build` は未実行**(この PC に Docker が無い)。Dockerfile の変更は目視のみ — 稼働前に一度ビルドすること。
- **R-003 で `paths:` の外に残した 1 件**: `internal/casino/announce.go` の `slog.Error("casino: announcement callback failed", ...)` は、コールバックが返したエラーをそのまま出す。本番のコールバック(`internal/commands/casino_announce.go`)が中で秘匿化してから返すので実害は無いが、`internal/casino` を直に使う別の呼び出し元が現れたら素通しになる。`internal/casino` 側にも同等のヘルパーを置くかは C-2 で判断する。
- **トークン秘匿の規律**: `internal/commands` で `slog` にエラーを渡すときは必ず `redactInteractionError(err)` を通す。新しいログ行を足すときも同じ(`"error", err` を直接渡さない)。
- **時刻注入の形(R-001 で決めた)**: `Store` に非公開の `clock func() time.Time`(nil = `time.Now`)を持たせ、`nowLocked()` を `Update` のクロージャ内からだけ読む(`rng` と同じ規律)。テストは同パッケージの `st.at(tm)` で差し込む。並行テストは goroutine 起動前に 1 回だけ `at` を呼ぶ(クロージャ書き換えは競合する)。`internal/commands` からは時刻を差せない(フィールドが非公開)ので、コマンド側テストは既定の実時計のまま。
- **R-002 で `paths:` の外に残した 1 件**: 9 時掲示(`announce.go`)は `Today` と `RecentRates` を生の履歴から組むので、時計が巻き戻った日だけ見出しとスパークラインの最終日がずれうる(`RecentRates` と同じ切り詰めを入れれば消える)。掲示は 1 日 1 回・`LastAnnounced` で守られており表示だけの問題。C-2 か announce を触る次のタスクで拾う。
- **レビュー 1 周目の non-blocking で残すもの(残課題)**: 9 時掲示の「送信成功 → MarkAnnounced」の間で落ちると同日再起動で再掲示する(障害をまたぐ必ず 1 回は未保証)/ 送信中の ctx cancel に停止期限が無い。どちらも通常運転では起きず、設計判断を要するので C-2 の設計に回す。
- **開始儀式(2026-09-14、`feat/casino-c1` = 92594d9)**: `go build ./... && go vet ./... && go test ./...` → 8 パッケージ ok。`-race` は cgo 無しで不可(既知)。Git Bash に `make` と `go` が無い — `go` は `C:/Program Files/Go/bin/go`、`make build` の代わりは `go build -o bin/todayistodaybot ./cmd/bot`。
- `main` は .NET 時代(go.mod 無し)。`feat/casino-c1` はフェーズ A/B/C-1 を全部持ち、`main` へ ff 可能。
- `config.json` / `DISCORD_TOKEN` は未設定(稼働時にユーザーが置く。エージェントは触らない)。
