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
  反復 3 の評価者指摘(逆順履歴で確定日が再抽選される)を 33d6bfa で追加修正。検証出力 `verify-R-002-{5,6,7,8}.txt`。

## R-003: スロットの通信エラーで Interaction トークンをログへ出さない
- status: done
- done-when: 所見 3(P1)。`internal/commands/slot.go` の 122 行・130 行付近が `respond` / `edit` の通信エラー(`*url.Error`、URL に `/interactions/<id>/<token>/callback` を含む)をそのまま `slog.Error` に渡す。ログに出す前にエラーを**秘匿化**する共通ヘルパー(例 `casino_shared.go` の `redactInteractionError(err) string` — `*url.Error` は `Op` と `Err` の種類だけ、URL は捨てる。それ以外はメッセージ中の `/interactions/…/…/` と `/webhooks/…/…/` のトークン部分を `[redacted]` に置換)を通す。7 コマンド全部と掲示(`casino_announce.go`)の同種のログ行を同じヘルパーに揃える。回帰テスト: `fakeSlotResponder.respondErr` に `/interactions/1/TEST_TOKEN/callback` の `*url.Error` を設定して `runReveal` を呼び、`slog` の出力を捕捉して `TEST_TOKEN` を含まない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -count=1`
- paths: internal/commands/casino_shared.go, internal/commands/casino_shared_test.go, internal/commands/slot.go, internal/commands/slot_test.go, internal/commands/*.go, internal/commands/*_test.go
- notes: Bot 認証トークンではなく Interaction トークン(短命)だが、ログに残す理由は無い。既存の他コマンド(weather/today 等)の同種ログも見つけたら同じヘルパーへ(別コミットで可)。
  反復 3 の評価者指摘(ハンドラの戻り値が未秘匿・`*url.Error` の原因が素通し)を 3da727f で追加修正。`respond()` 経由に統一し、`*url.Error` は `Op` と原因の型だけにした。検証出力 `verify-R-003fix-{1,2,3}.txt`。

## R-004: 配備設定で保存先 `data/` に書けるようにする
- status: done
- done-when: non-blocking の運用課題(稼働の前提)。`deploy/systemd/todayistodaybot.service` は `ProtectSystem=strict` で書き込み例外が無く、相対保存先 `/opt/todayistodaybot/data/casino.json` に書けない → `ReadWritePaths=/opt/todayistodaybot/data`(または `StateDirectory=todayistodaybot` と保存先の環境変数)を足す。`deploy/Dockerfile` は非 root ユーザーで `/data` 相当の書き込み先が無い → `VOLUME` と所有者の設定、起動時の作業ディレクトリを明示。`README.md` の配備節に「`data/` の場所と権限」を 1 段落足す。判定: `docker build` はこの PC に Docker が無いので行わない — unit ファイルと Dockerfile の差分を目視し、`systemd-analyze verify` 相当の構文は WSL の `systemd-analyze verify deploy/systemd/todayistodaybot.service`(WSL Ubuntu にある)で確かめる。
- verify: `findstr /C:"ReadWritePaths=/opt/todayistodaybot/data" deploy\systemd\todayistodaybot.service`
- verify: `findstr /C:"VOLUME" deploy\Dockerfile`
- verify: `go build ./... && go vet ./...`
- paths: deploy/systemd/todayistodaybot.service, deploy/Dockerfile, README.md
- notes: 保存先のパスを決めているコード(`internal/casino/store.go` の既定パス / `internal/store`)を読んで、環境変数で上書きできるならそれを unit に書く。無ければ相対パス前提のまま `ReadWritePaths` だけにする(コードは触らない)。
  保存先を変える環境変数は無い(`casino.DefaultPath` = `data/casino.json`、`store.DefaultPath` = `data/chosei-events.json` はどちらも定数)ので、相対パス前提のまま `ReadWritePaths` を足した。検証出力 `verify-R-004-{1,2,3}.txt`。`verify-R-004-1.txt` の `exit=1` は `ExecStart` のバイナリがこの PC に無いためだけで、unit の構文は `verify-R-004-2.txt` が `verify-exit=0` で示す。

# フェーズ C-2(ボタン基盤+ハイ&ロー+ブラックジャック)

仕様は `Docs/superpowers/specs/2026-09-14-casino-c2-design.md`(ユーザー承認済み 2026-09-14)。§ 番号はその文書。
反復の中で設計を再検討せず、矛盾を見つけたら `blocked/<task>.md` に書いて止まる。R 系(C-1 レビュー対応)は完了済み。

## C2-01: ボタン基盤 — `ComponentHandler` の自己登録・dispatch・所有者検査(§4)
- status: done
- done-when: `internal/commands/components.go` に `custom_id` 規約 `casino:<game>:<sessionID>:<action>` の生成 `BuildCustomID(game, sessionID, action)` と解析 `ParseCustomID(id) (game, sessionID, action string, ok bool)`(100 文字超・要素不足は `ok=false`)、`ComponentHandler` インターフェース(`Prefix()` / `HandleComponent(s, i, sessionID, action) error`)、`RegisterComponent(h)`(重複 prefix は panic)、`DispatchComponent(s, i) error`(未知の prefix は ephemeral の「❌ このボタンは無効です」)、所有者検査ヘルパー `requireSessionOwner(i, ownerID) string`(不一致なら「❌ これはあなたのゲームではありません」を返す。ephemeral で送るのは呼び出し側)を書く。`cmd/bot/main.go` の `InteractionCreate` ハンドラに `InteractionMessageComponent` の分岐を 1 つ足して `DispatchComponent` へ渡す(`main.go` の変更はこの 1 点。起動時返金と Sweep は C2-08)。`internal/commands/components_test.go`: custom_id の往復・境界(100 文字・要素不足)、重複 prefix の panic、未知 prefix の応答文、所有者不一致の文言。テスト用の `resetComponentsForTest()` を既存の `resetForTest()` に倣って置く。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestBuildCustomID|TestParseCustomID|TestRegisterComponent|TestDispatchComponent|TestRequireSessionOwner" -count=1`
- verify: `go test ./cmd/... -count=1`
- paths: internal/commands/components.go, internal/commands/components_test.go, cmd/bot/main.go, cmd/bot/main_test.go
- notes: 既存の `registry.go` と同じ流儀(`init()` 自己登録・重複 panic)。discordgo の `MessageComponentInteractionData` から `CustomID` を取る。押した人は `i.Member.User.ID`(DM は `i.User`)。

## C2-02: `casino.Store` の預かり(escrow)4 メソッドと保存則テスト(§3)
- status: todo
- done-when: `internal/casino/types.go` の `UserAccount` に `Escrow` / `EscrowGame` / `EscrowOpenedAt`(`omitempty`)を足す。`store.go` に `OpenGame(guild, user, game string, bet int64, now time.Time) error`(進行中なら `ErrGameInProgress`、残高不足なら既存の `ErrInsufficientChips`、1 未満のベットは拒否)、`AddToEscrow(guild, user string, amount int64) error`、`SettleGame(guild, user string, payout int64) (SettleResult, error)`(`Escrow == 0` なら `ErrNoGameInProgress`。`SettleResult` に精算後の `Chips` と支払った `payout`)、`RefundStaleEscrows(now time.Time) (int, error)` を、それぞれ `Update` 1 回で書く(クロージャ内から公開メソッドを呼ばない)。`errors.go` に 2 つのセンチネル。`store_test.go`: 保存則(`Chips + Escrow` が Open/Add/Settle/Refund の各経路で規則どおりにしか動かない)、二重精算の拒否、進行中の重複開始の拒否、既存 JSON(escrow フィールド無し)の読み込み互換、`RefundStaleEscrows` が全ギルドを返金して件数を返す、同一ユーザーへの並行 `OpenGame` 50 本で 1 本だけ成功する。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestStore|TestOpenGame|TestSettleGame|TestAddToEscrow|TestRefundStaleEscrows" -count=1`
- verify: `go test ./internal/casino/... -count=1`
- paths: internal/casino/types.go, internal/casino/errors.go, internal/casino/store.go, internal/casino/store_test.go
- notes: 危険地帯(資金の保存則)。`EscrowOpenedAt` は JST の RFC3339。`TopAssets`(ランキング)の総資産に `Escrow` を含める(預かりは本人の資産)。`ViewAccount`(`/balance`)にも預かり額を出す(表示は C2-06/07 で)。

## C2-03: カードとハイ&ローの純粋ロジック(§6)
- status: todo
- done-when: `internal/casino/cards.go`(`Card{Rank 2..14, Suit}`、`NewDeck(n int, rng randSource)`(n 組をシャッフル。既存の `randSource` を流用)、`Draw()`、`Remaining() []Card`)。`internal/casino/highlow.go`: `HighLowGame`(現在のカード・山・ポット・連勝・ベット・状態)、`NewHighLow(bet, rng)`、`Odds()`(残りの山から高い/低いの当たり枚数と残り枚数。同ランクは負け)、`Multiplier(winning, remaining) int64`(x100 整数 = `95 * remaining / winning` を切り捨て。`winning=0` は 0 = 選択不可。ハウス 5 %)、`Guess(high bool) (StepResult, error)`(勝ちなら `pot = floor(pot*m/100)`、次のカードを現在に、連勝 +1。連勝 10 かポット ≥ ベット×100 で自動キャッシュアウト。負けなら状態 lost・pot 0)、`CashOut() int64`(未プレイなら bet を返す)、`AutoResolve()`(= CashOut)。`highlow_test.go`: 決定的テスト(固定山で各分岐)、境界(A でハイは選択不可・2 でローは選択不可、同ランク負け、連勝 10・上限 100 倍の自動決着、未プレイのキャッシュアウトは全額)、統計テスト(seed 付き 100 万手で 1 手あたりの RTP が 95 %±1 %。数秒以内)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestDeck|TestHighLow" -count=1`
- paths: internal/casino/cards.go, internal/casino/cards_test.go, internal/casino/highlow.go, internal/casino/highlow_test.go
- notes: 倍率の整数式はここで固定し、テストの期待値もこれで計算する。乱数は既存の `randSource` インターフェースに合わせる。

## C2-04: ブラックジャックの純粋ロジック(§7)
- status: todo
- done-when: `internal/casino/blackjack.go`: `BlackjackGame`(6 デッキのシュー・両手・ベット・状態・ダブル可否)、`NewBlackjack(bet, rng)`(初期配布とナチュラル判定。両者ナチュラルはプッシュ、プレイヤーのみは即 3:2、ディーラーのみは即負け)、`Hit()`, `Stand()`, `Double()`(最初の判断でだけ。1 枚引いて自動スタンド)、ディーラーは 17 以上で止まる(ソフト 17 も止まる)、`Settle() int64`(勝ち 2×総ベット、プッシュ 総ベット、負け 0、ナチュラル `bet + floor(bet*3/2)`)、`AutoResolve()`(= Stand)、手札の値(A は 11 か 1)。`blackjack_test.go`: 固定シューでの各分岐(ナチュラル 3 通り、バースト、ディーラーバースト、プッシュ、ソフト 17 で止まる、ダブル後は 1 枚で止まる、バースト後は Hit 不可、ダブルは 2 手目以降不可)、境界(配当は総ベットの 2.5 倍を超えない)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestBlackjack|TestHandValue" -count=1`
- paths: internal/casino/blackjack.go, internal/casino/blackjack_test.go, internal/casino/cards.go
- notes: `cards.go` は C2-03 のものを使う(足りない API は足してよいが C2-03 のテストを壊さない)。

## C2-05: セッション管理と放置の自動決着(§5)
- status: todo
- done-when: `internal/casino/session.go`: `Session{ID, GuildID, UserID, Game, State any, LastActionAt, MessageRef{ChannelID, MessageID}}`、`SessionManager`(`NewSessionManager(now func() time.Time, ttl time.Duration)`、`Open(guild, user, game, state, ref) (*Session, error)`(同一 guild+user に進行中があれば `ErrGameInProgress`)、`Get(id)`、`WithSession(id, fn func(*Session) (done bool, err error)) error`(ロック内で盤面に適用し、`done` なら map から消す)、`Sweep(now) []*Session`(TTL 超過を map から外して返す)、`Touch(id, now)`)、プロセス内シングルトン `DefaultSessions()`(`sync.Once`、TTL 3 分、時計は `time.Now`)。セッション ID は `crypto/rand` 16 バイトの hex。`session_test.go`: 二重開始の拒否、`WithSession` の done で消える、`Sweep` が期限切れだけを返し二度と返さない、`Touch` で延命、並行 `WithSession` 100 本で盤面の適用回数が 100。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestSession" -count=1`
- paths: internal/casino/session.go, internal/casino/session_test.go
- notes: discordgo 非依存。放置の自動決着のゲーム側処理(`AutoResolve` → 精算 → 編集)は C2-08 の Sweep goroutine が呼ぶ。

## C2-06: `/highlow` コマンド・ボタン・表示(§6・§8・§9)
- status: todo
- done-when: `internal/commands/highlow.go`: `/highlow <bet>`(ギルド専用宣言 + 実行時ガード、ベット幅 10〜1,000、`EnsureCasinoAccess` → `Store.OpenGame` → `DefaultSessions().Open` → 公開メッセージに embed(現在のカード・ポット・連勝・各選択肢の確率と倍率)と 3 ボタン `⬆️ ハイ` / `⬇️ ロー` / `💰 キャッシュアウト`(選択不可の側は disabled)。`ComponentHandler`(prefix `highlow`): 所有者検査 → `WithSession` で `Guess`/`CashOut` を適用 → 決着なら `SettleGame` を先に永続化 → メッセージ編集(結果・配当・残高、ボタン無効化)。7 連勝以上のキャッシュアウトは公開の祝いメッセージ(`slot.go` の流儀)。通信エラーのログは `redactInteractionError` を通す。`/balance` に預かり額を 1 行足す。`highlow_test.go`: 表示の純粋関数(embed 本文・ボタンの disabled)、custom_id の往復、所有者不一致、決着後のボタンは「終了しています」、精算が編集より先(fake responder で順序を記録)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestHighLow|TestBalance" -count=1`
- verify: `go test ./internal/commands/... -count=1`
- paths: internal/commands/highlow.go, internal/commands/highlow_test.go, internal/commands/casino_shared.go, internal/commands/casino_shared_test.go, internal/commands/balance.go, internal/commands/balance_test.go
- notes: 文言は §9。ボタンのラベルに確率と倍率を出す(例 `⬆️ ハイ 61% ×1.55`)。

## C2-07: `/blackjack` コマンド・ボタン・表示(§7・§8・§9)
- status: todo
- done-when: `internal/commands/blackjack.go`: `/blackjack <bet>`(C2-06 と同じガード) → `OpenGame` → `NewBlackjack`。ナチュラルで即決着なら精算して結果を出す。それ以外は embed(プレイヤーの手と値、ディーラーの表 1 枚 + 🂠)と 3 ボタン `🃏 ヒット` / `✋ スタンド` / `⏫ ダブル`(ダブルは最初の判断だけ有効・残高不足なら disabled)。`ComponentHandler`(prefix `blackjack`): 所有者検査 → ダブルは `AddToEscrow` を先に永続化 → 盤面適用 → 決着なら `SettleGame` → 編集(ディーラーの伏せ札を公開)。ナチュラルは公開の祝い。`blackjack_test.go`: 表示の純粋関数、ダブルの disabled 条件、精算が編集より先、二重押し(決着後)の応答。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestBlackjack" -count=1`
- verify: `go test ./internal/commands/... -count=1`
- paths: internal/commands/blackjack.go, internal/commands/blackjack_test.go, internal/commands/casino_shared.go
- notes: ダブルの追加ベットは `AddToEscrow` が失敗したら盤面に適用しない(順序: 永続化 → 適用)。

## C2-08: 起動時の返金・放置の Sweep goroutine・help・文書(§3・§5・§8)
- status: todo
- done-when: `cmd/bot/main.go`: 起動時(セッション接続後、掲示スケジューラの起動と同じ場所)に `casino.Default().RefundStaleEscrows(now)` を呼んで件数をログ、Sweep goroutine(30 秒周期、掲示スケジューラと同じ ctx で終了)を起動 — goroutine 本体は `internal/commands/casino_sessions.go`(`RunSessionSweeper(ctx, s, mgr, store, interval)`: `Sweep` → 各ゲームの `AutoResolve` → `SettleGame` → メッセージを「⌛ 時間切れ — 自動決着」に編集。編集失敗はログのみ)。`RunSessionSweeper` のテスト(fake clock + fake responder: 期限切れ 1 件が精算され編集される、ctx cancel で戻る)。`/help` にコマンド一覧があれば `/highlow` `/blackjack` を足す。`README.md` のコマンド一覧に 2 本を足す。`Docs/agent-guide/architecture.md` は展開コピーなので触らず、`blocked/C2-08.md` に正本(MyWorkflow)へ写す追記(レイヤー表・所有権(SessionManager)・依存方向・危険地帯 5 点目 = escrow の保存則と二重決着)を書く。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./... -count=1`
- verify: `go build -o bin/todayistodaybot ./cmd/bot`
- paths: cmd/bot/main.go, cmd/bot/main_test.go, internal/commands/casino_sessions.go, internal/commands/casino_sessions_test.go, internal/commands/help.go, internal/commands/help_test.go, README.md, blocked/C2-08.md
- notes: `main.go` の変更は C2-01 の分岐と合わせて 3 点(§12)。README は公開物 — 過程を書かない。
