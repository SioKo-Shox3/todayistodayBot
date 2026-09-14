# TASKS — todayistodayBot

フェーズ C-1(カジノ経済基盤+スロット)の別文脈レビュー(`.harness/reviews/2026-09-14-astra-casino-c1-round1.md`)の
blocking を直す。設計書 `Docs/superpowers/specs/2026-07-10-casino-c1-design.md`・計画 `Docs/superpowers/plans/2026-07-10-casino-c1.md` が仕様。
反復の中で設計を再検討せず、矛盾を見つけたら `blocked/<task>.md` に書いて止まる。
`status` は `todo | doing | done | blocked`。`done` へのフリップは検証出力を開いた後でしか許されない(verify-gate)。
環境: Git Bash(Bash ツール)には `go` が無い — `export PATH="/c/Program Files/Go/bin:$PATH"` を前置する。`-race` はこの PC で動かない(cgo 無し。既知)。
`Docs/agent-guide/` と `CLAUDE.md` / `AGENTS.md` は MyWorkflow から展開される写しなので**編集しない**。絶対規則 3(`casino.Default()` 越しの単一ライター、`Update` 内で再ロックしない)を守る。

## R-001: `/daily` の受給日を後退させない(日付境界の二重受給)
- status: done
- done-when: 所見 1(P1)。`internal/casino/store.go` の `ClaimDaily` が「受給日と一致」しか拒まないため、`ClaimDaily(D)→(D+1)→(D)→(D+1)` が 4 回とも成功する。受給日が最後の受給日より**前**の要求も拒む(`ErrDailyAlreadyClaimed` 相当のセンチネルで。受給日は単調非減少)。`internal/commands/daily.go` が**ロック取得前**に時刻を取っている点も直し、時刻はロックの内側(`Update` のクロージャ内、または `Store` に注入した clock)で取る。回帰テスト: 上の 4 回のシーケンスで後半 2 回が拒否され支給合計が 450 で止まる。既存の daily テストは全て通る。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestStore|TestClaimDaily|TestDaily" -count=1`
- verify: `go test ./internal/commands/... -run "TestDaily" -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go, internal/casino/errors.go, internal/commands/daily.go, internal/commands/daily_test.go
- notes: 資金の保存則に関わる(危険地帯)。拒否の文言は既存の「本日は受給済み」系に揃える。

## R-002: 確定済みの日次レートを再抽選しない(`EnsureTodayRate` の順序逆転)
- status: done
- done-when: 所見 2(P1)。`internal/casino/store.go` の `EnsureTodayRate` が履歴末尾との一致しか見ず、`D→D+1→D→D+1` で履歴が `D,D+1,D,D+1` になり同じ日を再抽選する。渡された日付が履歴に**既にある**ならその日のレートを返して追加しない、履歴末尾より**前**の日付は追加しない(末尾のレートを返すか、専用エラー。設計書に従う — 無ければ「履歴にある日はその値、無い過去日は末尾の値」)。回帰テスト: 注入乱数 `Float64()=0.9` で上のシーケンスを回し、履歴が `D,D+1` の 2 件のままでレートが変わらない。両替・スパークライン(`/rate`)が同じ日に同じ値を返すことも 1 件で確かめる。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestEnsureTodayRate|TestNextRate|TestStore" -count=1`
- verify: `go test ./internal/commands/... -run "TestRate|TestExchange" -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go, internal/casino/economy.go, internal/casino/economy_test.go
- notes: 9 時掲示スケジューラ(`announce.go`)も `EnsureTodayRate` を呼ぶ。掲示テストが通ることを R-002 の verify の範囲で確かめる(`go test ./internal/casino/...` が丸ごと通る)。

## R-003: スロットの通信エラーで Interaction トークンをログへ出さない
- status: todo
- done-when: 所見 3(P1)。`internal/commands/slot.go` の 122 行・130 行付近が `respond` / `edit` の通信エラー(`*url.Error`、URL に `/interactions/<id>/<token>/callback` を含む)をそのまま `slog.Error` に渡す。ログに出す前にエラーを**秘匿化**する共通ヘルパー(例 `casino_shared.go` の `redactInteractionError(err) string` — `*url.Error` は `Op` と `Err` の種類だけ、URL は捨てる。それ以外はメッセージ中の `/interactions/…/…/` と `/webhooks/…/…/` のトークン部分を `[redacted]` に置換)を通す。7 コマンド全部と掲示(`casino_announce.go`)の同種のログ行を同じヘルパーに揃える。回帰テスト: `fakeSlotResponder.respondErr` に `/interactions/1/TEST_TOKEN/callback` の `*url.Error` を設定して `runReveal` を呼び、`slog` の出力を捕捉して `TEST_TOKEN` を含まない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -count=1`
- paths: internal/commands/casino_shared.go, internal/commands/casino_shared_test.go, internal/commands/slot.go, internal/commands/slot_test.go, internal/commands/*.go, internal/commands/*_test.go
- notes: Bot 認証トークンではなく Interaction トークン(短命)だが、ログに残す理由は無い。既存の他コマンド(weather/today 等)の同種ログも見つけたら同じヘルパーへ(別コミットで可)。

## R-004: 配備設定で保存先 `data/` に書けるようにする
- status: todo
- done-when: non-blocking の運用課題(稼働の前提)。`deploy/systemd/todayistodaybot.service` は `ProtectSystem=strict` で書き込み例外が無く、相対保存先 `/opt/todayistodaybot/data/casino.json` に書けない → `ReadWritePaths=/opt/todayistodaybot/data`(または `StateDirectory=todayistodaybot` と保存先の環境変数)を足す。`deploy/Dockerfile` は非 root ユーザーで `/data` 相当の書き込み先が無い → `VOLUME` と所有者の設定、起動時の作業ディレクトリを明示。`README.md` の配備節に「`data/` の場所と権限」を 1 段落足す。判定: `docker build` はこの PC に Docker が無いので行わない — unit ファイルと Dockerfile の差分を目視し、`systemd-analyze verify` 相当の構文は WSL の `systemd-analyze verify deploy/systemd/todayistodaybot.service`(WSL Ubuntu にある)で確かめる。
- verify: `wsl -d Ubuntu -- bash -lc "cd /mnt/c/Users/KINGkawamura/Documents/todayistodayBot && systemd-analyze verify deploy/systemd/todayistodaybot.service"`
- verify: `go build ./... && go vet ./...`
- paths: deploy/systemd/todayistodaybot.service, deploy/Dockerfile, README.md
- notes: 保存先のパスを決めているコード(`internal/casino/store.go` の既定パス / `internal/store`)を読んで、環境変数で上書きできるならそれを unit に書く。無ければ相対パス前提のまま `ReadWritePaths` だけにする(コードは触らない)。
