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
- status: done
- done-when: `internal/casino/types.go` の `UserAccount` に `Escrow` / `EscrowGame` / `EscrowOpenedAt`(`omitempty`)を足す。`store.go` に `OpenGame(guild, user, game string, bet int64, now time.Time) error`(進行中なら `ErrGameInProgress`、残高不足なら既存の `ErrInsufficientChips`、1 未満のベットは拒否)、`AddToEscrow(guild, user string, amount int64) error`、`SettleGame(guild, user string, payout int64) (SettleResult, error)`(`Escrow == 0` なら `ErrNoGameInProgress`。`SettleResult` に精算後の `Chips` と支払った `payout`)、`RefundStaleEscrows(now time.Time) (int, error)` を、それぞれ `Update` 1 回で書く(クロージャ内から公開メソッドを呼ばない)。`errors.go` に 2 つのセンチネル。`store_test.go`: 保存則(`Chips + Escrow` が Open/Add/Settle/Refund の各経路で規則どおりにしか動かない)、二重精算の拒否、進行中の重複開始の拒否、既存 JSON(escrow フィールド無し)の読み込み互換、`RefundStaleEscrows` が全ギルドを返金して件数を返す、同一ユーザーへの並行 `OpenGame` 50 本で 1 本だけ成功する。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestStore|TestOpenGame|TestSettleGame|TestAddToEscrow|TestRefundStaleEscrows" -count=1`
- verify: `go test ./internal/casino/... -count=1`
- paths: internal/casino/types.go, internal/casino/errors.go, internal/casino/store.go, internal/casino/store_test.go
- notes: 危険地帯(資金の保存則)。`EscrowOpenedAt` は JST の RFC3339。`TopAssets`(ランキング)の総資産に `Escrow` を含める(預かりは本人の資産)。`ViewAccount`(`/balance`)にも預かり額を出す(表示は C2-06/07 で)。
  検証出力 `verify-C2-02-{1,2,3,4}.txt`(build+vet clean / 対象テスト 73 PASS・FAIL 0 / casino ok / `go test ./...` 8 パッケージ ok)。`AddToEscrow` は仕様の残高不足に加えて `Escrow == 0` も拒む(進行中でないゲームへの追加ベットは返す先が無い)。`RefundStaleEscrows` の `now` は `EscrowOpenedAt` と同じ流儀の引数で、返金の判定には使わない(起動時の預かりは定義上「消えた盤面」)。

## C2-03: カードとハイ&ローの純粋ロジック(§6)
- status: done
- done-when: `internal/casino/cards.go`(`Card{Rank 2..14, Suit}`、`NewDeck(n int, rng randSource)`(n 組をシャッフル。既存の `randSource` を流用)、`Draw()`、`Remaining() []Card`)。`internal/casino/highlow.go`: `HighLowGame`(現在のカード・山・ポット・連勝・ベット・状態)、`NewHighLow(bet, rng)`、`Odds()`(残りの山から高い/低いの当たり枚数と残り枚数。同ランクは負け)、`Multiplier(winning, remaining) int64`(x100 整数 = `95 * remaining / winning` を切り捨て。`winning=0` は 0 = 選択不可。ハウス 5 %)、`Guess(high bool) (StepResult, error)`(勝ちなら `pot = floor(pot*m/100)`、次のカードを現在に、連勝 +1。連勝 10 かポット ≥ ベット×100 で自動キャッシュアウト。負けなら状態 lost・pot 0)、`CashOut() int64`(未プレイなら bet を返す)、`AutoResolve()`(= CashOut)。`highlow_test.go`: 決定的テスト(固定山で各分岐)、境界(A でハイは選択不可・2 でローは選択不可、同ランク負け、連勝 10・上限 100 倍の自動決着、未プレイのキャッシュアウトは全額)、統計テスト(seed 付き 100 万手で 1 手あたりの RTP が 95 %±1 %。数秒以内)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestDeck|TestHighLow" -count=1`
- paths: internal/casino/cards.go, internal/casino/cards_test.go, internal/casino/highlow.go, internal/casino/highlow_test.go
- notes: 倍率の整数式はここで固定し、テストの期待値もこれで計算する。乱数は既存の `randSource` インターフェースに合わせる。

## C2-04: ブラックジャックの純粋ロジック(§7)
- status: done
- done-when: `internal/casino/blackjack.go`: `BlackjackGame`(6 デッキのシュー・両手・ベット・状態・ダブル可否)、`NewBlackjack(bet, rng)`(初期配布とナチュラル判定。両者ナチュラルはプッシュ、プレイヤーのみは即 3:2、ディーラーのみは即負け)、`Hit()`, `Stand()`, `Double()`(最初の判断でだけ。1 枚引いて自動スタンド)、ディーラーは 17 以上で止まる(ソフト 17 も止まる)、`Settle() int64`(勝ち 2×総ベット、プッシュ 総ベット、負け 0、ナチュラル `bet + floor(bet*3/2)`)、`AutoResolve()`(= Stand)、手札の値(A は 11 か 1)。`blackjack_test.go`: 固定シューでの各分岐(ナチュラル 3 通り、バースト、ディーラーバースト、プッシュ、ソフト 17 で止まる、ダブル後は 1 枚で止まる、バースト後は Hit 不可、ダブルは 2 手目以降不可)、境界(配当は総ベットの 2.5 倍を超えない)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestBlackjack|TestHandValue" -count=1`
- paths: internal/casino/blackjack.go, internal/casino/blackjack_test.go, internal/casino/cards.go
- notes: `cards.go` は C2-03 のものを使う(足りない API は足してよいが C2-03 のテストを壊さない)。

## C2-05: セッション管理と放置の自動決着(§5)
- status: done
- done-when: `internal/casino/session.go`: `Session{ID, GuildID, UserID, Game, State any, LastActionAt, MessageRef{ChannelID, MessageID}}`、`SessionManager`(`NewSessionManager(now func() time.Time, ttl time.Duration)`、`Open(guild, user, game, state, ref) (*Session, error)`(同一 guild+user に進行中があれば `ErrGameInProgress`)、`Get(id)`、`WithSession(id, fn func(*Session) (done bool, err error)) error`(ロック内で盤面に適用し、`done` なら map から消す)、`Sweep(now) []*Session`(TTL 超過を map から外して返す)、`Touch(id, now)`)、プロセス内シングルトン `DefaultSessions()`(`sync.Once`、TTL 3 分、時計は `time.Now`)。セッション ID は `crypto/rand` 16 バイトの hex。`session_test.go`: 二重開始の拒否、`WithSession` の done で消える、`Sweep` が期限切れだけを返し二度と返さない、`Touch` で延命、並行 `WithSession` 100 本で盤面の適用回数が 100。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestSession" -count=1`
- paths: internal/casino/session.go, internal/casino/session_test.go
- notes: discordgo 非依存。放置の自動決着のゲーム側処理(`AutoResolve` → 精算 → 編集)は C2-08 の Sweep goroutine が呼ぶ。

## C2-06: `/highlow` コマンド・ボタン・表示(§6・§8・§9)
- status: done
- done-when: `internal/commands/highlow.go`: `/highlow <bet>`(ギルド専用宣言 + 実行時ガード、ベット幅 10〜1,000、`EnsureCasinoAccess` → `Store.OpenGame` → `DefaultSessions().Open` → 公開メッセージに embed(現在のカード・ポット・連勝・各選択肢の確率と倍率)と 3 ボタン `⬆️ ハイ` / `⬇️ ロー` / `💰 キャッシュアウト`(選択不可の側は disabled)。`ComponentHandler`(prefix `highlow`): 所有者検査 → `WithSession` で `Guess`/`CashOut` を適用 → 決着なら `SettleGame` を先に永続化 → メッセージ編集(結果・配当・残高、ボタン無効化)。7 連勝以上のキャッシュアウトは公開の祝いメッセージ(`slot.go` の流儀)。通信エラーのログは `redactInteractionError` を通す。`/balance` に預かり額を 1 行足す。`highlow_test.go`: 表示の純粋関数(embed 本文・ボタンの disabled)、custom_id の往復、所有者不一致、決着後のボタンは「終了しています」、精算が編集より先(fake responder で順序を記録)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestHighLow|TestBalance" -count=1`
- verify: `go test ./internal/commands/... -count=1`
- paths: internal/commands/highlow.go, internal/commands/highlow_test.go, internal/commands/casino_shared.go, internal/commands/casino_shared_test.go, internal/commands/balance.go, internal/commands/balance_test.go
- notes: 文言は §9。ボタンのラベルに確率と倍率を出す(例 `⬆️ ハイ 61% ×1.55`)。

## C2-07: `/blackjack` コマンド・ボタン・表示(§7・§8・§9)
- status: done
- done-when: `internal/commands/blackjack.go`: `/blackjack <bet>`(C2-06 と同じガード) → `OpenGame` → `NewBlackjack`。ナチュラルで即決着なら精算して結果を出す。それ以外は embed(プレイヤーの手と値、ディーラーの表 1 枚 + 🂠)と 3 ボタン `🃏 ヒット` / `✋ スタンド` / `⏫ ダブル`(ダブルは最初の判断だけ有効・残高不足なら disabled)。`ComponentHandler`(prefix `blackjack`): 所有者検査 → ダブルは `AddToEscrow` を先に永続化 → 盤面適用 → 決着なら `SettleGame` → 編集(ディーラーの伏せ札を公開)。ナチュラルは公開の祝い。`blackjack_test.go`: 表示の純粋関数、ダブルの disabled 条件、精算が編集より先、二重押し(決着後)の応答。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestBlackjack" -count=1`
- verify: `go test ./internal/commands/... -count=1`
- paths: internal/commands/blackjack.go, internal/commands/blackjack_test.go, internal/commands/casino_shared.go
- notes: ダブルの追加ベットは `AddToEscrow` が失敗したら盤面に適用しない(順序: 永続化 → 適用)。

## C2-08: 起動時の返金・放置の Sweep goroutine・help・文書(§3・§5・§8)
- status: done
- done-when: `cmd/bot/main.go`: 起動時(セッション接続後、掲示スケジューラの起動と同じ場所)に `casino.Default().RefundStaleEscrows(now)` を呼んで件数をログ、Sweep goroutine(30 秒周期、掲示スケジューラと同じ ctx で終了)を起動 — goroutine 本体は `internal/commands/casino_sessions.go`(`RunSessionSweeper(ctx, s, mgr, store, interval)`: `Sweep` → 各ゲームの `AutoResolve` → `SettleGame` → メッセージを「⌛ 時間切れ — 自動決着」に編集。編集失敗はログのみ)。`RunSessionSweeper` のテスト(fake clock + fake responder: 期限切れ 1 件が精算され編集される、ctx cancel で戻る)。`/help` にコマンド一覧があれば `/highlow` `/blackjack` を足す。`README.md` のコマンド一覧に 2 本を足す。`Docs/agent-guide/architecture.md` は展開コピーなので触らず、`blocked/C2-08.md` に正本(MyWorkflow)へ写す追記(レイヤー表・所有権(SessionManager)・依存方向・危険地帯 5 点目 = escrow の保存則と二重決着)を書く。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./... -count=1`
- verify: `go build -o bin/todayistodaybot ./cmd/bot`
- paths: cmd/bot/main.go, cmd/bot/main_test.go, internal/commands/casino_sessions.go, internal/commands/casino_sessions_test.go, internal/commands/help.go, internal/commands/help_test.go, README.md, blocked/C2-08.md
- notes: `main.go` の変更は C2-01 の分岐と合わせて 3 点(§12)。README は公開物 — 過程を書かない。

## C2-09: README のコマンド一覧に C-1 のカジノ 7 コマンドを足す
- status: done
- done-when: `README.md` の「カジノ」節に `/balance` `/daily` `/rate` `/exchange` `/slot` `/rank` `/casino-admin` を、各コマンドの `Definition()` の説明・オプションと矛盾しない形で足す(C2-08 で `/highlow` `/blackjack` だけが載っている状態を解消する)。文書のみで実装は変えない。
- verify: `go build ./... && go vet ./...`
- paths: README.md
- notes: C2-08 で見つけた取りこぼし。README は公開物 — 過程を書かない。

# C-2 区切りレビュー(1 周目)の対応

所見の全文は `.harness/reviews/2026-09-14-astra-casino-c2-round1.md`。blocking 3 件を C2-10〜C2-12、non-blocking のうち安く直せる 2 件を C2-13 で扱う。
設計判断は親が決めた(下記 notes)。仕様 §番号は `Docs/superpowers/specs/2026-09-14-casino-c2-design.md`。

## C2-10: 掃除人の精算失敗で配当を失わない(所見 1、P1)
- status: done
- done-when: `Sweep` で map から消した後に `SettleGame` が失敗すると、確定した配当(H&L のポット)が失われ、口座は `Escrow` が残って新規ゲームも始められない。直し方(親の決定): `SessionManager.Sweep(now)` は期限切れセッションを**消さずに `Expired` 状態にして返す**(以後の押下は「終了しています」、`Open` は進行中として拒否)。掃除人は `AutoResolve` → `SettleGame` が**成功したときだけ** `Remove(id)` で消す。失敗したら状態と確定配当(`PendingPayout int64`)をセッションに残し、次の Sweep が `Expired` のものを再度精算する(30 秒ごとの再試行)。再試行が成功したらメッセージ編集も行う。`session_test.go`: `Sweep` が同じセッションを `Expired` として再度返す、`Remove` 後は返さない。`casino_sessions_test.go`: 精算を 1 回失敗させると次の Sweep で同じ配当(例: ポット 173)が精算され、`Escrow` が 0 になる。`Expired` 中の押下が「終了しています」を返す。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestSession" -count=1`
- verify: `go test ./internal/commands/... -run "TestRunSessionSweeper|TestHighLow|TestBlackjack" -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/session.go, internal/casino/session_test.go, internal/commands/casino_sessions.go, internal/commands/casino_sessions_test.go, internal/commands/highlow.go, internal/commands/blackjack.go
- notes: 精算の再試行は「同じ payout を二度払わない」こと — `SettleGame` は `Escrow == 0` なら `ErrNoGameInProgress` を返すので、成功後の二重呼び出しはそこで止まる(その場合も `Remove` する)。

## C2-11: 盤面更新の通信失敗後、古い表示のまま操作を受け付けない(所見 2、P1)
- status: todo
- done-when: H&L で盤面を進めた後のメッセージ編集が失敗すると、表示は前のカードのまま内部は次のカードになり、次の押下が表示と違う判定で決着する。直し方(親の決定): セッションに `NeedsRedraw bool` を持ち、編集失敗時に立てる。次の押下では**手を進めずに**現在の盤面でメッセージを再描画し、押した人に ephemeral で「🔄 盤面を更新しました。もう一度選んでください」を返してフラグを下ろす(再描画も失敗したら立てたまま)。BJ も同じ経路にする。`highlow_test.go` / `blackjack_test.go`: 編集失敗 → 次の押下は判定せず再描画だけ(fake responder で記録)→ その次の押下で判定される。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestHighLow|TestBlackjack" -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/session.go, internal/casino/session_test.go, internal/commands/highlow.go, internal/commands/highlow_test.go, internal/commands/blackjack.go, internal/commands/blackjack_test.go, internal/commands/casino_shared.go
- notes: 「精算が先、表示は後」の規律は変えない。再描画の失敗をログに出すときは `redactInteractionError` を通す。

## C2-12: 時間切れの決着に勝敗・手札を表示し、精算失敗時は金額行を出さない(所見 3・non-blocking 2)
- status: todo
- done-when: 掃除人の「⌛ 時間切れ — 自動決着」は、各ゲームの結果描画(H&L: 最終カード・連勝・配当、BJ: ディーラーの伏せ札公開・最終点・勝敗)を使い、時間切れの説明行を添える(元の盤面 embed を置き換えるのではなく結果 embed に更新)。ボタンは無効化して残す(C2-08 の修正どおり)。H&L のキャッシュアウトで精算が失敗したときの案内は「配当: 0枚 / 残高: 0枚」を出さず、金額行を省いて「⚠️ 精算に失敗しました。次回の自動処理で精算されます」だけにする(BJ と同じ)。`casino_sessions_test.go`: 時間切れ BJ の勝ち/負け/プッシュで伏せ札と勝敗が本文にある。`highlow_test.go`: 精算失敗の案内に金額行が無い。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestRunSessionSweeper|TestHighLow|TestBlackjack" -count=1`
- paths: internal/commands/casino_sessions.go, internal/commands/casino_sessions_test.go, internal/commands/highlow.go, internal/commands/highlow_test.go, internal/commands/blackjack.go, internal/commands/blackjack_test.go
- notes: C2-10 の再試行で精算が後から成功したときも同じ結果描画で編集する。

## C2-13: README の総資産式に預かりを含め、小額ベットの RTP を文書化する(non-blocking 1・3)
- status: todo
- done-when: `README.md` の総資産の説明を `コイン × レート + チップ + 預かり中のチップ` に直す(`/rank` の実装 `TopAssets` と一致)。ハイ&ローの倍率は x100 整数の切り捨てなので小額ベット(10〜20)では 1 手の RTP が 95 % を下回る(丸め損はハウス側という C-1 の規則どおり) — `highlow_test.go` に「ベット 11 で初手の全ランク・有効な方向を等確率で選んだときの期待 RTP が 93 %以上 95 %以下」を固定する期待値テストを足し、`Docs/superpowers/specs/2026-09-14-casino-c2-design.md` §6 に「RTP 95 % はポットが大きいときの値。ベット 10 台では丸めで 93〜94 %」と 1 行書く。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestHighLow" -count=1`
- paths: README.md, internal/casino/highlow_test.go, Docs/superpowers/specs/2026-09-14-casino-c2-design.md
- notes: 倍率式は変えない(仕様)。README は公開物 — 過程を書かない。
