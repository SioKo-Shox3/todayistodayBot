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
- status: done
- done-when: H&L で盤面を進めた後のメッセージ編集が失敗すると、表示は前のカードのまま内部は次のカードになり、次の押下が表示と違う判定で決着する。直し方(親の決定): セッションに `NeedsRedraw bool` を持ち、編集失敗時に立てる。次の押下では**手を進めずに**現在の盤面でメッセージを再描画し、押した人に ephemeral で「🔄 盤面を更新しました。もう一度選んでください」を返してフラグを下ろす(再描画も失敗したら立てたまま)。BJ も同じ経路にする。`highlow_test.go` / `blackjack_test.go`: 編集失敗 → 次の押下は判定せず再描画だけ(fake responder で記録)→ その次の押下で判定される。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestHighLow|TestBlackjack" -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/session.go, internal/casino/session_test.go, internal/commands/highlow.go, internal/commands/highlow_test.go, internal/commands/blackjack.go, internal/commands/blackjack_test.go, internal/commands/casino_shared.go
- notes: 「精算が先、表示は後」の規律は変えない。再描画の失敗をログに出すときは `redactInteractionError` を通す。

## C2-12: 時間切れの決着に勝敗・手札を表示し、精算失敗時は金額行を出さない(所見 3・non-blocking 2)
- status: done
- done-when: 掃除人の「⌛ 時間切れ — 自動決着」は、各ゲームの結果描画(H&L: 最終カード・連勝・配当、BJ: ディーラーの伏せ札公開・最終点・勝敗)を使い、時間切れの説明行を添える(元の盤面 embed を置き換えるのではなく結果 embed に更新)。ボタンは無効化して残す(C2-08 の修正どおり)。H&L のキャッシュアウトで精算が失敗したときの案内は「配当: 0枚 / 残高: 0枚」を出さず、金額行を省いて「⚠️ 精算に失敗しました。次回の自動処理で精算されます」だけにする(BJ と同じ)。`casino_sessions_test.go`: 時間切れ BJ の勝ち/負け/プッシュで伏せ札と勝敗が本文にある。`highlow_test.go`: 精算失敗の案内に金額行が無い。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestRunSessionSweeper|TestHighLow|TestBlackjack" -count=1`
- paths: internal/commands/casino_sessions.go, internal/commands/casino_sessions_test.go, internal/commands/highlow.go, internal/commands/highlow_test.go, internal/commands/blackjack.go, internal/commands/blackjack_test.go
- notes: C2-10 の再試行で精算が後から成功したときも同じ結果描画で編集する。

## C2-13: README の総資産式に預かりを含め、小額ベットの RTP を文書化する(non-blocking 1・3)
- status: done
- done-when: `README.md` の総資産の説明を `コイン × レート + チップ + 預かり中のチップ` に直す(`/rank` の実装 `TopAssets` と一致)。ハイ&ローの倍率は x100 整数の切り捨てなので小額ベット(10〜20)では 1 手の RTP が 95 % を下回る(丸め損はハウス側という C-1 の規則どおり) — `highlow_test.go` に「ベット 11 で初手の全ランク・有効な方向を等確率で選んだときの期待 RTP が 93 %以上 95 %以下」を固定する期待値テストを足し、`Docs/superpowers/specs/2026-09-14-casino-c2-design.md` §6 に「RTP 95 % はポットが大きいときの値。ベット 10 台では丸めで 93〜94 %」と 1 行書く。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestHighLow" -count=1`
- paths: README.md, internal/casino/highlow_test.go, Docs/superpowers/specs/2026-09-14-casino-c2-design.md
- notes: 倍率式は変えない(仕様)。README は公開物 — 過程を書かない。

## C2-14: 精算失敗の契約(手動再試行)を設計書へ明記する(反復 3 の評価者指摘)
- status: done
- done-when: `Docs/superpowers/specs/2026-09-14-casino-c2-design.md` §9 に「決着した盤面は `WithSession(done=true)` でセッションから外れるため掃除人は二度と見ない。精算が拒まれたときの案内は `casinoSettleFailedMessage`(手動の 🔁 再試行)で、自動の再精算は次回起動の `RefundStaleEscrows` だけ」を書く — C2-12 の done-when が求めた「次回の自動処理で精算されます」は待てば済むという嘘になるため採らない、という判断を正本に残す。案内文が `Data.Content` ごと固定されていることを `highlow_test.go` / `blackjack_test.go` の既存テストで確認し、足りなければ足す。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestHighLow|TestBlackjack|TestSweepIdleBoards" -count=1`
- paths: Docs/superpowers/specs/2026-09-14-casino-c2-design.md, internal/commands/highlow_test.go, internal/commands/blackjack_test.go
- notes: 評価者の 2 点目(`casinoPayoutLine` を `casino_sessions.go` へ移す)は採らない — この関数は `highlow.go` / `blackjack.go` / `casino_sessions.go` の 3 か所から呼ばれる共有ヘルパで、`casino_shared.go` が置き場として正しい。C2-12 が `paths:` の外へ 1 関数はみ出した事実は記録として残す。

# フェーズ C-3a(ジャックポット+宝くじ)

仕様は `Docs/superpowers/specs/2026-09-14-casino-c3a-design.md`(ユーザー承認済み 2026-09-14)。§ 番号はその文書。
反復の中で設計を再検討せず、矛盾を見つけたら `blocked/<task>.md` に書いて止まる。C2 系は完了済み。

## C3-01: ジャックポットのプール — 積立・7️⃣7️⃣7️⃣ で全額・種(§2)
- status: done
- done-when: `internal/casino/types.go` の `GuildEconomy` に `Jackpot int64` と `JackpotAccum int64`(`json:"jackpot"` / `json:"jackpot_accum"`)を足す。`store.go` の `Spin` の `Update` の中で、①プールが 0 なら `JackpotSeed`(1,000)で初期化、②`JackpotAccum += bet*2; contrib = JackpotAccum/100; JackpotAccum %= 100; Jackpot += contrib`、③リールが 7️⃣7️⃣7️⃣ なら `payout += Jackpot; JackpotWon = Jackpot; Jackpot = JackpotSeed`、の順に行う(配当表 `slot.go` は変えない)。`SpinResult` に `JackpotWon` と `JackpotPool` を足す。レート生成(`ensureTodayRateLocked`)でもプールが 0 なら種で初期化する(掲示で 0 を見せない)。`store_test.go`: ベット 10 を 5 回で `Jackpot` が +1・`JackpotAccum` が 0、7️⃣7️⃣7️⃣(注入乱数)で `payout == bet*196 + pool` かつプールが 1,000 へ戻る、💎💎💎 ではプールが動かない、既存 JSON(フィールド無し)を読んで初回スピンで種が入る、通貨の保存則(`Chips` の増減 = 配当 − ベット、プールの増減 = 積立 − 発火)。既存の RTP 統計テストと並行テストが通る。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestSpin|TestStore|TestJackpot|TestPayout" -count=1`
- verify: `go test ./internal/casino/... -count=1`
- paths: internal/casino/types.go, internal/casino/store.go, internal/casino/store_test.go, internal/casino/slot.go, internal/casino/slot_test.go, internal/casino/errors.go
- notes: 危険地帯(資金の保存則)。`JackpotSeed` / `JackpotContributionPercent` は `internal/casino` の定数。`slot.go` の `IsJackpot` は 💎💎💎 / 7️⃣7️⃣7️⃣ の祝い判定のまま。

## C3-02: `/slot` の結果と 7️⃣7️⃣7️⃣ の祝い・9 時掲示にジャックポットを出す(§2 表示)
- status: done
- done-when: `internal/commands/slot.go` の結果メッセージに「🎰 ジャックポット: N チップ」(発火時は「🎰 JACKPOT!! +N チップ」)の 1 行を足し、7️⃣7️⃣7️⃣ の公開の祝いにプール額(獲得額)を入れる。`internal/casino/announce.go` の `AnnouncementJob` に `JackpotPool int64` を足し(ロールオーバーで種を入れた後の値)、`internal/commands/casino_announce.go` の embed に「🎰 ジャックポット」フィールドを足す。`slot_test.go` / `casino_announce_test.go`(commands 側): 文言の純粋関数テスト(発火あり/なし、掲示のフィールド)。`announce_test.go`(casino 側): `AnnouncementJob.JackpotPool` が入る。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestSlot|TestAnnounce|TestBuildAnnouncement" -count=1`
- verify: `go test ./internal/casino/... -run "TestAnnounce|TestCollect" -count=1`
- paths: internal/commands/slot.go, internal/commands/slot_test.go, internal/commands/casino_announce.go, internal/commands/casino_announce_test.go, internal/casino/announce.go, internal/casino/announce_test.go
- notes: 精算 → 表示の順序は変えない(表示は `SpinResult` の値を写すだけ)。

## C3-03: 宝くじの永続化・購入・抽選(§3)
- status: done
- done-when: `types.go` に `Lottery` / `LotteryDraw`(§3 の形。`GuildEconomy.Lottery` は値型で `json:"lottery"`)。`internal/casino/lottery.go`(純粋): `LotteryPrize(sales, carryover) (prize, house int64)`(`prize = floor(sales*90/100) + carryover`、`house = sales − floor(sales*90/100)`)、`PickLotteryWinner(tickets map[string]int, rng) string`(枚数で重み付け。決定的な順序 = userID 昇順で累積)。`store.go`: `BuyLotteryTickets(guild, user string, count int, now) (LotteryPurchase, error)`(1〜10、1 人 1 抽選 10 枚まで → `ErrLotteryLimit`、残高不足 → `ErrInsufficientChips`。`Update` 1 回)、`LotteryStatus(guild, user, now) (LotteryView, error)`(次回賞金・枚数・購入者数・自分の枚数・次の 9:00 JST・前回の結果)、日次ロールオーバー(`ensureTodayRateLocked` と同じ `Update` の内側)に `drawLotteryLocked(economy, today, rng)`: `DrawDate < today` のとき、購入者がいれば当選者に `prize` を加算・ハウス分を `Jackpot` へ・`LastDraw` 更新、いなければ `Carryover = prize`。抽選後は `Tickets`/`Sales` を空に、`DrawDate = today`。`AnnouncementJob` に `LotteryDraw`(前回結果)と次回の賞金・枚数を足す。`store_test.go` / `lottery_test.go`: 上限・残高不足・賞金とハウス分の計算・重み付き抽選が決定的・購入者 0 の繰り越し・同じ日に二度抽選しない・`EnsureTodayRate` の起動時フォールバックで抽選される・並行購入 50 本で枚数と売上が合う・保存則(`Chips` の減少 = 売上、当選者の増加 = 賞金、`Jackpot` の増加 = ハウス分)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestLottery|TestStore|TestEnsureTodayRate|TestCollect" -count=1`
- verify: `go test ./internal/casino/... -count=1`
- paths: internal/casino/types.go, internal/casino/errors.go, internal/casino/lottery.go, internal/casino/lottery_test.go, internal/casino/store.go, internal/casino/store_test.go, internal/casino/announce.go, internal/casino/announce_test.go
- notes: 危険地帯。抽選は「今日のレートが無ければ生成」と同じトランザクションで行い、掲示チャンネルの有無に依存しない(§3 取りこぼし防止)。`randSource` は `Store` の既存の `rng` を使う。

## C3-04: `/lottery buy|status` コマンド(§4・§5)
- status: done
- done-when: `internal/commands/lottery.go`: `/lottery buy <枚数>`(ギルド専用宣言 + 実行時ガード、`EnsureCasinoAccess` → `BuyLotteryTickets`)と `/lottery status`(`LotteryStatus`)。表示は公開。文言は §3・§5。`lottery_test.go`: `Handle()` から切り出した純粋関数(引数の検証、購入結果の文言、status の文言)、上限超過と残高不足の文言。`/help` の一覧に `/lottery` を足す(あれば)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestLottery|TestHelp" -count=1`
- verify: `go test ./internal/commands/... -count=1`
- paths: internal/commands/lottery.go, internal/commands/lottery_test.go, internal/commands/help.go, internal/commands/help_test.go
- notes: サブコマンド構成は `/casino-admin` と `/exchange` の既存実装に倣う。

## C3-05: 9 時掲示の宝くじフィールドと当選者の祝い(§3 掲示)
- status: done
- done-when: `internal/commands/casino_announce.go` の embed に「🎟️ 宝くじ」フィールド(昨日の当選者と賞金 / 今日の賞金プールと購入枚数)を足し、`LotteryDraw` に当選者がいれば**別メッセージ**で公開の祝い(`<@id>` メンション、賞金額。スロットの大当たりと同じ流儀)を送る。送信失敗はログのみで掲示と抽選には影響しない(既存の掲示と同じ)。`casino_announce_test.go`: フィールドの文言(当選あり/なし/購入者 0 の繰り越し)、祝いメッセージが当選者のいるときだけ送られる(fake sender で記録)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -run "TestAnnounce|TestBuildAnnouncement|TestStartAnnounce" -count=1`
- verify: `go test ./... -count=1`
- paths: internal/commands/casino_announce.go, internal/commands/casino_announce_test.go, internal/casino/announce.go, internal/casino/announce_test.go
- notes: 祝いは掲示チャンネルへ。掲示チャンネル未設定なら祝いも送らない(結果は `/lottery status` で見える)。

## C3-06: README・設計書の実測・architecture の追記案(§4・§7)
- status: done
- done-when: `README.md` のコマンド一覧に `/lottery buy` / `/lottery status` と、`/slot` のジャックポットの説明を足す(公開物 — 過程を書かない)。設計書 §2・§3 の「実測」として、積立の端数の例と賞金の計算例を 1 段落ずつ書く。`Docs/agent-guide/architecture.md` は展開コピーなので触らず、`blocked/C3-06.md` に正本へ写す追記(レイヤー表の「宝くじ」、日次ロールオーバーで動くもの = レート生成・ジャックポットの種・宝くじの抽選、危険地帯 6 点目 = プールと賞金の保存則)を書く。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./... -count=1`
- verify: `go build -o bin/todayistodaybot ./cmd/bot`
- paths: README.md, Docs/superpowers/specs/2026-09-14-casino-c3a-design.md, blocked/C3-06.md
- notes: `cmd/bot/main.go` は C-3a で変更しない(§7)。変更が要ると分かったら `blocked/C3-06.md` に理由を書いて止まる。

# C-3a 区切りレビュー(1 周目)の対応

所見の全文は `.harness/reviews/2026-09-14-astra-casino-c3a-round1.md`。blocking 3 件を C3-07〜C3-09、non-blocking 1 件を C3-10 で扱う。
仕様 § は `Docs/superpowers/specs/2026-09-14-casino-c3a-design.md`。

## C3-07: 上限で受け取れなかった賞金を、次の当選者ではなくジャックポットのプールへ送る(所見 1)
- status: done
- done-when: `drawLotteryLocked`(`internal/casino/store.go` 1090 付近)は、当選者が `MaxChips` で賞金を受け取り切れないとき残額を `lottery.Carryover` に入れており、**次回の別の当選者へその人の賞金が移る**。親の決定(2026-09-14): 受け取れなかった残額は **ジャックポットのプールへ送る**(`economy.Jackpot += prize - paid`。ハウス分と同じ経路・`seedJackpotLocked` の後)。C-1 の「丸め損は常にハウス側」と同じ扱いで、通貨は消えず、他人の手にも渡らない(プールは 7️⃣7️⃣7️⃣ で全員に戻る)。`Carryover` は**当選者がいなかった回だけ**使う(`winner == ""` の経路のまま)。当選者がいた回は `lottery.Carryover = 0`。`LastDraw.Prize` は実際に払った `paid` のまま。設計書 §3 に「上限で受け取れなかった分はプールへ」を 1 行書く。`store_test.go`: 既存の「残額が次回へ繰り越る」期待(3174 付近)を書き換え、**残高上限の u1 が当選 → 入金は入る分だけ・残額はプールに入る・`Carryover` は 0・次回 u2 だけが買っても u1 の残額は u2 へ渡らない**を固定する。保存則(チップの増加 + プールの増加 = 売上 + 前回繰り越し)も併せて検査する。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestLottery|TestStore|TestDrawLottery" -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go, internal/casino/lottery.go, internal/casino/lottery_test.go, Docs/superpowers/specs/2026-09-14-casino-c3a-design.md
- notes: 危険地帯(資金の保存則)。`blocked/C3-06.md` の「差額は `Carryover = prize − paid` で次回へ送る」という記述も、このタスクで**新しい挙動に書き直す**(正本への反映は親が行う)。

## C3-08: 掲示できなかった回の当選を、次の掲示で取りこぼさない(所見 2)
- status: done
- done-when: `CollectDailyAnnouncements`(`internal/casino/announce.go` 73 付近)は `LastDraw.Date == today` の回だけを掲示対象にするため、9 時に Bot が落ちていて翌朝 9 時前に復旧すると、起動時に精算された前日付の当選が**一度も掲示されず祝われない**。条件を「**まだ掲示していない回**」= `last.Date > economy.LastAnnounced` に変える(`MarkAnnounced` は送信成功時だけ `LastAnnounced = date` を書くので、掲示に失敗した回は次の巡回で再度対象になる)。`announce_test.go`: 12 日に掲示済み(`LastAnnounced = 12 日`)・13 日付の当選が残っている状態で 14 日の掲示を集めると、13 日付の `LotteryDraw` が job に入る / 掲示成功後(`LastAnnounced = 14 日`)は同じ回が二度入らない / 当日付の当選は従来どおり入る。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestCollectDailyAnnouncements|TestAnnounce|TestMarkAnnounced" -count=1`
- verify: `go test ./internal/commands/... -run "TestAnnounce|TestBuildAnnouncement" -count=1`
- paths: internal/casino/announce.go, internal/casino/announce_test.go, internal/commands/casino_announce.go, internal/commands/casino_announce_test.go
- notes: 掲示の文言(「昨日の当選」)は日付が今日とは限らなくなるので、`LastDraw.Date` を使う表現(例「9/13 の当選」)へ合わせる。掲示チャンネル未設定のギルドは従来どおり対象外。

## C3-09: 「次回抽選」を呼び出し時刻ではなく確定済みの抽選日から出す(所見 3)
- status: done
- done-when: `BuyLotteryTickets` と `LotteryStatus`(`internal/casino/store.go` 1181 / 1219 付近)は `nextRunAt(now)` をそのまま返すため、9 時直前に受け付けた要求が 9 時の抽選の後に処理されると、**既に済んだ 09:00 を「次回」と表示する**(券は正しく次回分に入る)。ロールオーバー後の `lottery.DrawDate` から次回を出す — `nextLotteryDrawAt(lastDrawDate string, now time.Time) time.Time`(`internal/casino/lottery.go` か `announce.go`)を足し、`nextRunAt(now)` と「`lastDrawDate` の翌日 09:00 JST」の**遅い方**を返す。両方の呼び出しをこれに差し替える。`store_test.go`: 9/14 08:59:59 の `now` で、`DrawDate` が既に 9/14 のときの購入・status がどちらも 9/15 09:00 を返す / 通常(`DrawDate` が 9/13)は 9/14 09:00 を返す / 一度も抽選していない(`DrawDate == ""`)ときは `nextRunAt(now)` のまま。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestLottery|TestNextRunAt|TestNextLotteryDraw|TestBuyLottery" -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go, internal/casino/lottery.go, internal/casino/lottery_test.go, internal/casino/announce.go, internal/casino/announce_test.go
- notes: `nextRunAt` は 9 時掲示スケジューラも使っているので**シグネチャを変えない**(新しい関数を足して宝くじ側だけ差し替える)。

## C3-10: 💎💎💎 の説明を「払い出しは無いが積立は行う」に直す(non-blocking 1)
- status: done
- done-when: `README.md`(67 付近)と設計書 §2 の実測(36 付近)にある「💎💎💎 はプールを 1 チップも動かさない」は、そのスピン自身の積立を無視していて誤り(プール 5,000・ベット 100 の 💎💎💎 はプールが 5,002 になる)。「💎💎💎 はプールからの**払い出しが無い**(積立は他のスピンと同じように行われる)」へ直す。設計書の実測文も条件(ベット 10・端数 0)を明記するか、積立込みの値へ直す。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./... -count=1`
- paths: README.md, Docs/superpowers/specs/2026-09-14-casino-c3a-design.md
- notes: README は公開物 — 過程を書かない。

## C3-11b: 設計書の掲示文言を「昨日の当選」から実際の抽選日へ合わせる
- status: done
- done-when: C3-08 で 9 時掲示の見出しが `LastDraw.Date` 由来の「M/D の当選」(当選者なしは「前回の当選」)に変わったので、`Docs/superpowers/specs/2026-09-14-casino-c3a-design.md` 61 行目・69 行目の「昨日の当選」の記述を実装に合わせて直す。掲示対象が「今日精算した回」ではなく「まだ掲示していない回」になったことも 1 行で書く。コードは触らない。
- verify: `go build ./... && go vet ./...`
- paths: Docs/superpowers/specs/2026-09-14-casino-c3a-design.md
- notes: C3-08 の `paths:` の外だったので切り出した。README には該当の文言は無い(`grep 昨日の当選` は設計書と PROGRESS.md だけに当たる)。

## C3-11: ジャックポットのプールに上限を敷き、桁あふれを構造的に不可能にする(C3-07 の差し戻し)
- status: done
- done-when: `NEXT_FINDINGS.md` の 2 節(「C3-07 の区切り評価」と「反復 1 — 評価者の判定」。同じ 1 件)を閉じる。親の決定(2026-09-14): 口座の `MaxChips`(1e12)と同じ規約をプールにも敷く。(1) `internal/casino/types.go` に `MaxJackpot int64 = MaxChips` を足し、同ファイルの桁あふれ余裕の説明(126 行付近)にプールの行を加える。(2) `seedJackpotLocked` を正規化点にする — 既存の「`< JackpotSeed` なら引き上げ」「`JackpotAccum < 0` なら 0」に加えて **`> MaxJackpot` なら `MaxJackpot` へ切り下げ**(手編集・破損ファイルの値をここで必ず正常範囲に入れる)。(3) プールへの加算は `creditJackpotCappedLocked(economy, amount) int64`(実際に入った額を返す)を通し、ハウス分・上限超過の賞金・スロットの積立の 3 経路すべてを差し替える。入り切らない分は**捨てる** — `creditChipsCappedLocked` が `MaxChips` で超過分を捨てるのと同じ契約で、事故ではなく仕様。(4) 保存則の言い方を「通貨は消えない」から「**プールの上限で入り切らない分だけが消え、それ以外では消えない**」へ直す(`store_test.go` の保存則テスト・設計書 §8・`blocked/C3-06.md` の記述を揃える)。(5) 回帰テスト: `Jackpot = MaxJackpot - 10` で抽選とスピンを通しても負にならず `MaxJackpot` を超えない / 手編集で `math.MaxInt64` 相当が入っていても読み取り点で正規化される / 通常範囲では既存の期待値が 1 つも変わらない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestJackpot|TestStore|TestLottery|TestSpin" -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/types.go, internal/casino/store.go, internal/casino/store_test.go, internal/casino/lottery.go, internal/casino/lottery_test.go, internal/casino/slot.go, Docs/superpowers/specs/2026-09-14-casino-c3a-design.md, blocked/C3-06.md
- notes: 危険地帯(資金の保存則)。プール ≤ 1e12・加算 ≤ 1e12 なので、正規化点を通れば `int64` に対して十分な余裕がある(`types.go` の既存の分析と同じ論法で書く)。閉じたら `NEXT_FINDINGS.md` の当該 2 節を消す。

## C3-12: 未掲示の当選を 1 枠で上書きしない — 掲示待ちの回を並べて持つ(C3-08 の差し戻し)
- status: done
- done-when: `NEXT_FINDINGS.md` の「反復 2」の節(`LastDraw` が 1 枠なので、未掲示の回が次の抽選で上書きされて永久に掲示されない)を閉じる。親の決定(2026-09-14): **掲示待ちの回を並べて持つ**。(1) `Lottery` に `Unannounced []LotteryDraw`(`json:"unannounced,omitempty"`)を足し、`drawLotteryLocked` は当選者がいた回をここに**追記**する(`LastDraw` は `/lottery status` 用の最新 1 件として従来どおり更新)。上限 7 件、超えたら古い方から落とす(賞金は抽選時に支払い済みなので、落ちるのは掲示だけ。ファイルを無制限に太らせない)。(2) `AnnouncementJob` の `LotteryDraw *LotteryDraw` を `LotteryDraws []LotteryDraw` に変え、`CollectDailyAnnouncements` は `Unannounced` を**そのまま**渡す(日付での絞り込みはしない)。(3) `MarkAnnounced(guildID, date)` は `LastAnnounced` を書くのに加えて、`Unannounced` から `Date <= date` の要素を取り除く(送信成功時だけ呼ばれるので、失敗した回は残って次の巡回で再送される。送信中に発生した新しい回は日付が後なので消えない)。(4) 掲示 embed は待ち行列の全件を出し(複数なら日付付きで並べる)、当選者のいる回ごとに公開の祝いを送る。`announce_test.go` / `casino_announce_test.go`: 13 日の当選が未掲示のまま 14 日の抽選が起きても両方が job に入る / `MarkAnnounced` 後は消える / 送信失敗(= `MarkAnnounced` を呼ばない)なら次の巡回でまた入る / 8 回連続で溜まったら古い 1 件が落ちて 7 件になる / 当選者のいない回は溜まらない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestCollectDailyAnnouncements|TestAnnounce|TestMarkAnnounced|TestLottery|TestStore" -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/types.go, internal/casino/announce.go, internal/casino/announce_test.go, internal/casino/store.go, internal/casino/store_test.go, internal/casino/lottery.go, internal/commands/casino_announce.go, internal/commands/casino_announce_test.go, Docs/superpowers/specs/2026-09-14-casino-c3a-design.md
- notes: 既存 JSON(`unannounced` 無し)はそのまま読める(`omitempty` + nil スライス)。設計書 §3 の掲示の節に「掲示待ちは並べて持ち、送信成功で消す(最大 7 件)」を書く。閉じたら `NEXT_FINDINGS.md` の当該節を消す。

## C3-13: 永続化された数値を読み取り点で正規化し、算術の桁あふれを一箇所で断つ(C3-11 の差し戻し)
- status: done
- done-when: `NEXT_FINDINGS.md` の C3-11 差し戻し(`Carryover` が `math.MaxInt64` 付近だと `LotteryPrize` の加算があふれ、プールに空きがあっても賞金とハウス分が消える)を閉じる。個別の加算に検査を足すのではなく、**読み取り点で正規化する**規律で閉じる(`seedJackpotLocked` が `Jackpot` / `JackpotAccum` に対して既にやっていることの一般化)。親の決定(2026-09-15): (1) `normalizeLotteryLocked(lottery *Lottery)` を足し、`Sales` と `Carryover` を `[0, MaxChips]` へ、`Tickets` の各値を `[1, LotteryMaxTicketsPerDraw]` へ丸める(0 以下の要素は削除、`Tickets` が空なら nil に戻す)。(2) 日次ロールオーバー(`ensureTodayRateIndexLocked`)で、抽選・種入れの**前**に `normalizeLotteryLocked` を呼ぶ。購入(`BuyLotteryTickets`)と `LotteryStatus` はロールオーバーを通るので追加の呼び出しは要らない(通らない経路があれば、そこにも置く)。(3) `types.go` の桁あふれ余裕の説明に宝くじの行を足す — 正規化後は `Sales ≤ 1e12`・`Carryover ≤ 1e12` なので `prize = floor(Sales×90/100) + Carryover ≤ 2e12`、`int64` に対して 4.6e6 倍の余裕がある、と既存の分析と同じ論法で書く。(4) 設計書 §8 に「**永続化された数値は読み取り点で正常範囲へ正規化し、以後の算術はその範囲の内側で閉じる**。手編集・破損ファイルの値に対する防御はここ 1 箇所に集約する」を書く。(5) 回帰テスト: `Carryover = math.MaxInt64 - 40`・`Sales = 100`・`Jackpot = 5000`・当選者残高 900 で抽選を通すと、賞金もハウス分も消えず(正規化後の値で計算され)プールと当選者の残高が正しく増える / `Sales` が負・`Tickets` に 0 や負や 11 以上が混じったファイルを読んでも抽選が壊れない / 通常範囲では既存の期待値が 1 つも変わらない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestLottery|TestStore|TestNormalize|TestEnsureTodayRate" -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/lottery.go, internal/casino/lottery_test.go, internal/casino/store.go, internal/casino/store_test.go, internal/casino/types.go, Docs/superpowers/specs/2026-09-14-casino-c3a-design.md
- notes: 危険地帯(資金の保存則)。**この差し戻しは「個別の加算にガードを足す」では閉じない** — 直すたびに次の変数へ移る(ハウス分 → 残額 → 繰り越し と 2 回移った)。正規化点を 1 つ決めて、そこを通らない経路を無くすのが完了条件。閉じたら `NEXT_FINDINGS.md` の当該節を消す。

## C3-14: 当選者がいない回のハウス分を消さず、口座の空き計算のあふれも正規化で断つ
- status: done
- done-when: `NEXT_FINDINGS.md`「C3-07 の区切り評価」の所見 2・3 を閉じる。(1) **当選者なしの回でハウス分が消える**: `drawLotteryLocked` の `winner == ""` の経路は `lottery.Carryover = prize` だけを残し、`house` をどこにも入れていない(売上 100・券なしなら 90 が繰り越り、ハウス分 10 が消える)。当選者がいた回と同じ結論に揃える — `seedJackpotLocked` の後に `creditJackpotCappedLocked(economy, house)` を通す。保存則のテストを「当選者がいない回でも `Chips の増減 + プールの増減 + 繰り越しの増減 = 売上`」で固定する。(2) **`creditChipsCappedLocked` の `headroom` があふれる**: `MaxChips - account.Escrow - account.Chips` は手編集で `Chips = Escrow = MaxInt64` のとき負に回り込む。C3-13 で決めた規律どおり**読み取り点で正規化する** — 口座を読む共通点(`ensureAccountLocked`)で `Coins` / `Chips` / `Escrow` を `[0, MaxChips]`(コインは `[0, MaxCoins]`)へ丸め、`headroom` の式自体は変えない。`types.go` の桁あふれ余裕の説明に口座の行を足す。回帰テスト: `Chips = Escrow = math.MaxInt64` のファイルを読んでも入金が負にならず口座が上限で頭打ちになる / 通常範囲では既存の期待値が 1 つも変わらない(既存の口座テストが全部通る)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go, internal/casino/lottery.go, internal/casino/lottery_test.go, internal/casino/types.go, Docs/superpowers/specs/2026-09-14-casino-c3a-design.md
- notes: 危険地帯(資金の保存則)。(2) は C3-13 と同じ「正規化点を 1 つ決める」規律の口座版 — 加算側に個別ガードを足さない。閉じたら `NEXT_FINDINGS.md` の当該節の所見 2・3 を消す。

## C3-15: 未抽選の「次回抽選」テストを足し、旧挙動のコメントを消す
- status: done
- done-when: (1) C3-09 の差し戻し(`NEXT_FINDINGS.md` の「反復 3 — C3-09」の節)を閉じる — `internal/casino/store_test.go` 3604 付近の表駆動テストに **未抽選(`lastDrawDate: ""`)** のケースを足し、`now = 2026-09-14 08:59:59 JST` で購入・status の `NextDrawAt` がどちらも同日 09:00(`nextRunAt(now)`)になることを固定する。(2) `NEXT_FINDINGS.md`「C3-07 の区切り評価」の所見 4 を閉じる — `internal/commands/casino_announce.go` 108 付近の「the pot rolls forward」という**旧挙動の説明コメント**と、`internal/casino/announce_test.go` 457 付近の同じ趣旨のコメントを現在の挙動(当選者がいた回は繰り越さない。上限で入り切らなかった分と、当選者がいない回の賞金だけが次回へ)に直す。**コメントだけを直し、テストの中身は触らない**。(3) 閉じた節を `NEXT_FINDINGS.md` から消し、paths 違反の節(反復 3 — C3-12、親の判断で処理不要)も消して、ファイルを見出しだけに戻す。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -run "TestBuyLotteryTickets|TestLotteryStatus|TestAnnounce" -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/store_test.go, internal/commands/casino_announce.go, internal/commands/casino_announce_test.go, NEXT_FINDINGS.md
- notes: 旧挙動のコメントは `internal/casino/announce_test.go` ではなく `internal/commands/casino_announce_test.go`(324・475 行)にあったので `paths:` を実態へ直した。表には `now` の列を足した — 追加ケースだけ別の日付の時計を要るため(既存 2 行の値と期待値は不変)。

## C3-16: 正規化点を通らずに口座を触る経路(`RefundStaleEscrows`)を塞ぐ
- status: done
- done-when: C3-14 の差し戻し(`NEXT_FINDINGS.md` の最新節)を閉じる。`RefundStaleEscrows`(`internal/casino/store.go` 1095 付近)は口座を map から直接読んで `moveFromEscrowLocked` で加算するため、C3-14 で置いた正規化点(`ensureAccountLocked`)を通らない。手編集の `Chips = Escrow = math.MaxInt64` を読むと返金で `Chips = -2` が**保存され**、次の読み取りで 0 に丸められる(= チップが消える)。返金対象の口座も加算前に `ensureAccountLocked` を通す(既に非 nil でも通す)。`store_test.go`: `Chips = Escrow = math.MaxInt64` のファイルで `RefundStaleEscrows` → `ViewAccount` を回し、保存される値が負にならず上限で頭打ちになる / 通常の返金(C-2 の既存テスト)の期待値が 1 つも変わらない。`headroom` の式は変えない。**もし直前の反復が差し戻しの処理としてこれを既に直していたら**、検証を回して `status: done` にするだけでよい(重複作業をしない)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go
- notes: 危険地帯(資金の保存則)。これで「口座に触る経路はすべて `ensureAccountLocked` を通る」が閉じる — 他にも直接 map を引いている箇所があれば同じ扱いにし、見つけた場所を進捗に書く。

## C3-17: 実装自身が作った繰り越しを上限で消さない(2 周目 blocking 2)
- status: done
- done-when: `.harness/reviews/2026-09-15-astra-casino-c3a-round2.md` の blocking 2 を直す。`drawLotteryLocked` の当選者不在の経路は賞金全額を `Carryover` に入れるため `MaxChips` を超えうるが、`normalizeLotteryLocked` が次の読み取りでそれを `MaxChips` へ切り詰めるので**実装自身が作った通貨が消える**(再現: `Sales=100`・`Carryover=MaxChips`・券なし・プール 5,000 で `LotteryStatus` を 2 回呼ぶと 90 チップ消失)。親の決定(2026-09-15): **繰り越しに入り切らない分はジャックポットのプールへ送る**(他の全ての余りと同じ行き先。`seedJackpotLocked` の後に `creditJackpotCappedLocked`)。繰り越しを書く箇所で `MaxChips` を超える分を先に切り出してプールへ回し、`Carryover <= MaxChips` を**書き込み時点で**保証する(正規化は破損ファイル対策として残す)。併せて 2 周目の non-blocking(`seedJackpotLocked` が巨大な正の `JackpotAccum` を補正せず、積立の加算があふれうる)も直す — `JackpotAccum` を `[0, 100)` へ丸める。設計書 §8 の契約文を「通貨が消えるのは**プールの上限**で入り切らないときだけ。口座・繰り越しの上限で溢れた分はプールへ送る」に直す。回帰テスト: 上の再現手順で 2 回目の照会でも通貨が消えない(繰り越し + プール + 口座の合計が不変)/ `JackpotAccum = math.MaxInt64` のファイルを読んで積立を通してもプールが負にならない / 通常範囲の既存の期待値が 1 つも変わらない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go, internal/casino/lottery.go, internal/casino/lottery_test.go, Docs/superpowers/specs/2026-09-14-casino-c3a-design.md
- notes: 危険地帯(資金の保存則)。**切り詰めは「壊れたファイルの値」にだけ許され、実装が作った値には許さない** — この区別を設計書に 1 行で書く。

## C3-18: 旧形式の未掲示結果を待ち行列へ移行する(2 周目 blocking 1)
- status: done
- done-when: 同レビューの blocking 1 を直す。`CollectDailyAnnouncements` は `Unannounced` だけを見るため、**C3-12 より前の形式**で書かれた `data/casino.json`(`unannounced` キーが無く、未掲示の当選が `LastDraw` にだけある)を読むと、その回の掲示と祝いが永久に失われる(再現: `LastAnnounced="2026-09-13"`・`LastDraw.Date="2026-09-14"`・当選者あり・`unannounced` 無しで 14 日 10 時に起動)。読み取り点(`normalizeLotteryLocked`)で移行する: `Unannounced` が空で、`LastDraw` が非 nil・当選者あり・`LastDraw.Date > LastAnnounced` なら、`LastDraw` の写しを待ち行列へ 1 件入れる(`LastAnnounced` はギルド側にあるので、移行には `GuildEconomy` を渡す形にしてよい)。既に待ち行列に同じ日付があれば入れない(二重掲示の防止)。回帰テスト: 旧形式の JSON から掲示を集めると当選が job に入る / 同じ状態で 2 回集めても 1 件のまま / `LastDraw.Date <= LastAnnounced`(掲示済み)なら入らない / 当選者不在の `LastDraw` は入らない / 新形式(`unannounced` あり)の挙動が変わらない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/lottery.go, internal/casino/lottery_test.go, internal/casino/store.go, internal/casino/store_test.go, internal/casino/announce.go, internal/casino/announce_test.go
- notes: この形式は**このブランチの途中コミットでしか存在しない**(Bot はまだ一度も本番稼働していない)が、古いビルドで生成したファイルを持ち込む可能性はあるので閉じる。移行は読み取り点に置き、抽選で `LastDraw` を上書きする**前**に効くこと。

## C3-19: 移行の掲示チャンネル条件を外し、掲示済み境界を後退させない
- status: done
- done-when: `.harness/reviews/2026-09-15-astra-c3-18.md` の blocking 2 件を直す。(1) **[P1] チャンネル未設定のギルドで移行対象の旧当選が失われる**: 移行(`store.go` 1301 付近)が `AnnounceChannelID == ""` を除外条件にしているため、掲示先が未設定のギルドでは移行が飛び、次の抽選が `LastDraw` を上書きして当選記録が消える(掲示は後からチャンネルを設定すれば送れるはずだった)。**チャンネルの条件を外す** — 移行は掲示先の有無に関係なく行う(掲示するかどうかは `CollectDailyAnnouncements` 側の判断)。再現(レビューの手順)を回帰テストにする: チャンネル空・`LastAnnounced=2026-09-12`・`LastDraw.Date=2026-09-13`(当選者あり)・待ち行列なし・次回分の券ありで 9/14 10:00 に `EnsureTodayRate` を呼ぶと、9/13 の当選が待ち行列に残る(抽選が上書きしても失われない)。(2) **[P2] 日付が巻き戻ると掲示済みの当選が復活する**: `MarkAnnounced` は `LastAnnounced` を後退させられるため、時計が戻ると掲示済みの回が「未掲示」に見えて再掲示・再祝いされる。`LastAnnounced` を**単調非減少**にする(`date > LastAnnounced` のときだけ書く)。回帰テスト: 9/14 掲示済みの状態で 9/13 10:00 → 9/14 10:00 の順に掲示パスを回しても、9/14 の当選が二度掲示されない / 通常の前進では従来どおり更新される。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go, internal/casino/announce.go, internal/casino/announce_test.go, internal/casino/lottery.go, internal/casino/lottery_test.go
- notes: (2) は宝くじだけでなくレート掲示にも効く(時計の巻き戻しで 9 時掲示が二度出るのも同じ原因)。日付の比較は文字列の辞書順で足りる(`YYYY-MM-DD` 固定長)。

# フェーズ C-3b(/duel + 月次シーズン制)

仕様は `Docs/superpowers/specs/2026-09-15-casino-c3b-design.md`(承認済み)。§ 番号はその文書。
反復の中で設計を再検討せず、矛盾を見つけたら `blocked/<task>.md` に書いて止まる。C3 系(C-3a)は完了済み。
**資金に触るタスクは §2 の契約を先に読む**(duel はゼロサム、賞与は意図的な新規発行、`SeasonNet` は通貨ではない)。

## C3B-01: duel の純粋ロジックと預かり(§4.5・§2)
- status: done
- done-when: `internal/casino/duel.go`: `DuelState{ChallengerID, OpponentID, Bet, Stage}`(`Stage` は待機/決着)、`FlipDuel(rng randSource) (challengerWins bool)`(コイントス。既存の `randSource` を使う)、`DuelPayout(bet int64, challengerWins bool) (challengerPayout, opponentPayout int64)`(勝者 `2*bet`・敗者 0)。`store.go` に `AcceptDuel(guild, challengerID, opponentID string, bet int64, challengerWins bool) (DuelSettlement, error)`: **1 回の `Update`** の中で、受け手から `bet` を預かり(残高不足・進行中ありは既存のセンチネルで拒否。挑戦者の預かりには触らない)、両者の `Escrow` を 0 にして勝者へ `2*bet` を入れ、両者の `SeasonNet` を更新(受け取った額 − 賭けた額)。`DeclineDuel(guild, challengerID) error`(挑戦者へ `bet` を返す = `SettleGame(payout = bet)` 相当、受け手は触らない)。`duel_test.go` / `store_test.go`: コイントスの決定性、精算額、**2 人の合計が不変**(ゼロサム)、受諾失敗で挑戦者の預かりが減らない、辞退で全額戻る、上限に座った勝者は入る分だけ入り `SeasonNet` も入った額で数える、並行受諾で二重精算が起きない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/duel.go, internal/casino/duel_test.go, internal/casino/store.go, internal/casino/store_test.go, internal/casino/types.go, internal/casino/errors.go
- notes: 危険地帯。`Update` のクロージャ内から公開メソッドを呼ばない(絶対規則 3)。口座に触る経路は必ず `ensureAccountLocked`(C3-14 の正規化点)を通す。

## C3B-02: `SeasonNet` を全ゲームの決着に配線する(§3)
- status: done
- done-when: `UserAccount.SeasonNet`(C3B-01 で追加済み)を、既存の全ゲームの決着で更新する — スロット(`Spin`)、ハイ&ロー / ブラックジャック(`SettleGame`)、宝くじ(当選の入金と購入の支払い)。**1 回ごとに `SeasonNet += (実際に口座へ入った額 − 賭けた額)`**。デイリーボーナス・両替・`mint`・ウェルカムボーナス・シーズン賞与は**含めない**(§3)。`SettleGame` は「賭けた額」を知らないので、預かり(`Escrow`)を消す時点の値を使う(= 賭けた総額。ダブル込み)。読み取り点(`ensureAccountLocked`)で `SeasonNet` を `[-MaxChips, MaxChips]` へ正規化する。`store_test.go`: スロットの勝ち負け・H&L・BJ・duel・宝くじで `SeasonNet` が期待どおり動く / デイリーと両替と mint では動かない / 破損ファイルの巨大値が正規化される / 既存の期待値が 1 つも変わらない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go, internal/casino/types.go
- notes: 危険地帯。`SeasonNet` は順位のための集計値で通貨ではない(§2)— 保存則のテストに混ぜない。

## C3B-03: 月次シーズンの切り替えと賞与(§4)
- status: done
- done-when: `internal/casino/season.go`: `SeasonRanks(users map[string]*UserAccount, limit int) []SeasonRank`(純利の降順、同点は UserID 昇順、純利 0 は除く)、`SeasonBonus(rank int) int64`(1 位 10,000 / 2 位 5,000 / 3 位 2,500 / それ以外 0)。`types.go` に `SeasonMonth` / `LastSeason` / `SeasonResult` / `SeasonRank`(§4 の形)。`store.go` の日次ロールオーバー(`ensureTodayRateIndexLocked`)に `rolloverSeasonLocked(economy, month string)`: JST の月が `SeasonMonth` と違えば閉じる — 上位 3 名へ賞与を入れ(`creditChipsCappedLocked`。入り切らない分は捨てる。`SeasonRank.Bonus` は**実際に入った額**)、`LastSeason` を書き、全口座の `SeasonNet` を 0 にし、`SeasonMonth` を今月にする。未開始(`""`)なら賞与を配らず今月を開始するだけ。**月の比較は `SeasonMonth < month` の前進のみ**(時計の巻き戻しで二度閉じない。C3-19 と同じ規律)。`season_test.go` / `store_test.go`: 順位と同点の解決、賞与の額、純利 0 を数えない、月またぎで 1 回だけ閉じる、巻き戻りで閉じない、未開始ギルドは賞与なし、切り替え後に全員の `SeasonNet` が 0、賞与が上限で入り切らないときの `Bonus` の値、既存 JSON(フィールド無し)の互換。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/season.go, internal/casino/season_test.go, internal/casino/types.go, internal/casino/store.go, internal/casino/store_test.go, Docs/superpowers/specs/2026-09-15-casino-c3b-design.md
- notes: 危険地帯(全口座を触る)。賞与は**意図的な新規発行**で、保存則の例外として設計書に書いてある(§2)— テストもその前提で書く。

## C3B-04: `/duel` コマンドと受諾・辞退ボタン(§4.5・§5・§6)
- status: done
- done-when: `internal/commands/duel.go`: `/duel <相手> <bet>`(ギルド専用宣言 + 実行時ガード、ベット幅 10〜1,000、自分自身と Bot を拒否、`EnsureCasinoAccess` → `OpenGame`(挑戦者)→ `DefaultSessions().Open` → 公開メッセージに embed(挑戦者・相手・ベット)と「⚔️ 受ける」「🚫 断る」)。`ComponentHandler`(prefix `duel`): **押せるのは受け手だけ**(盤面に持たせた受け手 ID で判定。他人には ephemeral で「❌ この挑戦はあなた宛てではありません」)。受諾は `AcceptDuel` → 結果を編集(コイン・勝者・配当・両者の残高)、ボタン無効化。受け手のチップ不足は ephemeral で断り盤面を残す。辞退は `DeclineDuel` → 「🚫 挑戦は断られました」に編集。C-2 の掃除人の自動決着(3 分)は `DeclineDuel` と同じ扱い(挑戦者へ返金、「⌛ 時間切れ — 挑戦は取り下げられました」)。`duel_test.go`: 表示の純粋関数、所有者(受け手)判定、自分自身/Bot の拒否、精算が編集より先(fake responder で順序)、決着後の押下、時間切れの文言。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/commands/duel.go, internal/commands/duel_test.go, internal/commands/casino_sessions.go, internal/commands/casino_sessions_test.go, internal/commands/casino_shared.go
- notes: C-2 のセッション基盤をそのまま使う(`AutoResolve` の duel 版は「挑戦の取り下げ」)。通信エラーのログは `redactInteractionError` を通す。

## C3B-05: `/season` コマンドと `/balance` の純利表示(§5)
- status: done
- done-when: `internal/commands/season.go`: `/season`(今月の上位 10 名・自分の順位と純利・残り日数(月末まで)・前シーズンの結果)。`store.go` に `SeasonStatus(guild, user string, now time.Time) (SeasonView, error)`(ロールオーバーを通す)。`/balance` に「今月の純利」を 1 行足す。`/help` の一覧に `/duel` と `/season` を足す。`season_test.go` / `balance_test.go`: 表示の純粋関数(順位表・自分が圏外のとき・誰も遊んでいないとき・前シーズンなし)、残り日数の計算(月末・月初)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/commands/season.go, internal/commands/season_test.go, internal/commands/balance.go, internal/commands/balance_test.go, internal/commands/help.go, internal/commands/help_test.go, internal/casino/store.go, internal/casino/store_test.go
- notes: 順位表の整形は `/rank` の既存実装に倣う。

## C3B-06: 9 時掲示のシーズン欄と結果の祝い(§4 掲示)
- status: done
- done-when: `AnnouncementJob` に今月の上位 3 名と、閉じたシーズンの結果(あれば)を足し、`internal/commands/casino_announce.go` の embed に「🏆 シーズン(今月)」フィールドを足す。シーズンが閉じた回は**別メッセージ**で公開の結果発表(上位 3 名を @メンション、賞与額)。掲示チャンネル未設定なら送らない(結果は `/season` で見える)。C-3a の掲示待ち行列と同じく、**閉じたシーズンの結果も掲示に成功するまで保持する**(`LastSeason` は上書きされないので、掲示済みかどうかは `LastAnnounced` と同じ high-water 方式で判定する — 詳細は実装者が §4 と C3-19 の規律に沿って決め、選んだ方法を進捗に書く)。`casino_announce_test.go` / `announce_test.go`: フィールドの文言(遊んだ人がいない月を含む)、結果の祝いが閉じた回だけ送られる、送信失敗なら次の巡回でまた送られる。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./internal/commands/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/announce.go, internal/casino/announce_test.go, internal/casino/types.go, internal/commands/casino_announce.go, internal/commands/casino_announce_test.go
- notes: C-3a の宝くじの掲示と同じ形に揃える(取りこぼさない・二重に出さない)。

## C3B-07: README・設計書の実測・architecture の追記案
- status: done
- done-when: `README.md` のコマンド一覧に `/duel` と `/season` を足し、シーズンの説明(月次・純利順・上位 3 名に賞与・残高はリセットしない)を書く(公開物 — 過程を書かない)。設計書 §2 に実測(duel のゼロサムと賞与の発行額)を 1 段落。`Docs/agent-guide/architecture.md` は展開コピーなので触らず、`blocked/C3B-07.md` に正本へ写す追記(レイヤー表に duel / season、日次ロールオーバーで動くものに「月次シーズンの切り替え」、危険地帯 7 点目 = duel のゼロサムと賞与の意図的発行・全口座を触る切り替え)を書く。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./... -count=1`
- verify: `go build -o bin/todayistodaybot ./cmd/bot`
- paths: README.md, Docs/superpowers/specs/2026-09-15-casino-c3b-design.md, blocked/C3B-07.md
- notes: `cmd/bot/main.go` は C-3b で変更しない(§8)。変更が要ると分かったら `blocked/C3B-07.md` に理由を書いて止まる。

## C3B-08: 未掲示のシーズン結果を月替わりで失わない(所見 1、P1)
- status: done
- done-when: 掲示できなかった `LastSeason` の結果が、次の月替わり(`rolloverSeasonLocked` の上書き)で消えない。**親の決定(2026-09-15): C-3a の宝くじ(C3-12)と同じ待ち行列にする** — 機構を実装者に委ねたのが今回の取りこぼしの原因なので、ここで固定する。`GuildEconomy` に `UnannouncedSeasons []SeasonResult`(`json:"unannounced_seasons,omitempty"`、上限 3 件、超えたら古い方から落とす)を足し、`rolloverSeasonLocked` が閉じた結果を**追記**する(`LastSeason` は `/season` の表示用に最新 1 件として残す)。掲示側は待ち行列をそのまま渡し、**結果の送信に成功した月だけ**取り除く(`MarkAnnounced` と同じく、成功が確認できたときだけ)。既存 JSON(キー無し)はそのまま読める。回帰テスト: 7/31 の巡回で結果送信だけ失敗 → 8/1 の巡回で 6 月の結果が再送される(掲示チャンネル未設定のまま月をまたいだ場合も同じ)。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./internal/commands/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/announce.go, internal/casino/announce_test.go, internal/casino/store.go, internal/casino/store_test.go, internal/casino/types.go, internal/commands/casino_announce.go, internal/commands/casino_announce_test.go
- notes: 評価者(反復 2)の所見 1。`NEXT_FINDINGS.md` の該当節を、このタスクを閉じるときに消す。

## C3B-09: 掲示済みの日でも未掲示のシーズン結果だけを再送する(所見 2、P2)
- status: done
- done-when: embed 成功・結果送信失敗のあと、同じ日の次の巡回で結果だけが再送される。日次掲示の除外条件(`LastAnnounced`)と結果の収集条件を分け、embed を二重に出さずに結果だけ運ぶ。回帰テスト: 7/10 10 時に embed 成功・結果失敗 → 同日 11 時の巡回で「embed 累計 1 件・結果 1 件」。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./internal/commands/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/announce.go, internal/casino/announce_test.go, internal/commands/casino_announce.go, internal/commands/casino_announce_test.go
- notes: 評価者(反復 2)の所見 2。C3B-08 と同じ経路を触るので、C3B-08 を先に閉じる。

## C3B-10: 月末に結果送信だけ失敗した場合の再送を回帰テストで押さえる(反復 4 の所見 1、P2)
- status: done
- done-when: C3B-08 の done-when が名指しした経路(送信の失敗を実際に起こし、embed 成功による `LastAnnounced` 更新も通す)を掲示側のテストで再現する。7/31 の巡回で結果送信だけ失敗 → 8/1 の巡回で `LastSeason` が 7 月へ移っても 6 月の結果が送られ、成功後は再送されない。既存の送信失敗テストにケースを足す形でよい。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/commands/casino_announce_test.go, internal/casino/announce_test.go
- notes: 評価者(反復 4)の所見 1。実装の不具合は見つかっておらず、足りないのは検証。閉じるときに `NEXT_FINDINGS.md` の反復 4 節を消す。

## C3B-11: duel のゼロサムと全口座を触る経路の説明を実装に合わせる(反復 3 の所見 1・2、P2)
- status: done
- done-when: (1) `README.md` の duel が「勝者はベットの 2 倍を必ず受け取る」と読めないようにし、残高上限による切り捨てを明記する(`TestAcceptDuel_WinnerAtTheCapTakesOnlyWhatFitsAndSeasonNetCountsThat` が実測)。(2) `blocked/C3B-07.md` の危険地帯 7 点目を「上限による切り捨てが無い場合にゼロサム」に直し、「全口座を触る唯一の書き込み」「他はすべて 1〜2 口座」の断定を消す(`SeasonStatus` は月替わりが無くても全口座を正規化して保存し、`RefundStaleEscrows` は全ギルドの対象口座をまとめて返金する)。公開物なので過程を書かない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./... -count=1`
- paths: README.md, blocked/C3B-07.md
- notes: 評価者(反復 3)の所見 1・2。コードは変えない — 文書が実装に追いついていないだけ。閉じるときに `NEXT_FINDINGS.md` の反復 3 節を消す。

# C-3b 区切りレビュー(1 周目)の対応

所見の全文は `.harness/reviews/2026-09-15-astra-casino-c3b-round1.md`。blocking 5 件を C3B-12〜16 に割る。
設計判断は親が決めた(各 notes)。仕様 § は `Docs/superpowers/specs/2026-09-15-casino-c3b-design.md`。

## C3B-12: 精算の入金を他の経路と同じ「上限で切り詰める」形に揃える(blocking 3、P1)
- status: done
- done-when: 月次賞与が口座の空きを埋めた直後の `SettleGame` が `creditChipsLocked`(厳格版)で `ErrChipCapExceeded` を返し、**トランザクション全体(月次切り替えを含む)が巻き戻って何度やっても失敗する**(再現: 7 月首位を `Chips=MaxChips-300`・`Escrow=100`・`SeasonNet=500` にして 8 月の時計で `SettleGame(..., 200)`)。§2 の契約は「配当が口座の上限で入り切らない分は**捨てる**」なので、`SettleGame` の入金を `creditChipsCappedLocked` に変え、`SeasonNet` は**実際に入った額**で数える(§2 の既存規則)。`SettleResult` に実際に入った額が分かる情報を残す(表示が「配当 N」と嘘をつかないこと)。同じ理由で上限に当たりうる他の精算経路(`AcceptDuel` の勝者への入金など)も同じ扱いか確認し、違えば揃える。併せて設計書 §2 冒頭の「合計は不変」「消えない」という無条件の書き方に**上限の例外**を足す(non-blocking 1。実測段落・README・`blocked/C3B-07.md` とは既に整合しているので冒頭だけ)。回帰テスト: 上の再現手順で精算が成功し、入った分だけ増え、`SeasonNet` も入った額で動く / 通常範囲の既存の期待値が 1 つも変わらない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go, internal/casino/types.go, internal/commands/highlow.go, internal/commands/blackjack.go, internal/commands/duel.go, internal/commands/highlow_test.go, internal/commands/blackjack_test.go, internal/commands/duel_test.go, Docs/superpowers/specs/2026-09-15-casino-c3b-design.md
- notes: 危険地帯。**「入金が拒否されて取引全体が巻き戻る」形を残さない** — 上限は行き先の問題であって、精算を止める理由にしない(§2)。

## C3B-13: 預かりに持ち主の印を付け、別の盤面の預かりで精算できないようにする(blocking 2 の構造、P1)
- status: done
- done-when: `AcceptDuel`(`store.go` 1570 付近)の確認がゲーム種別と金額だけなので、**別のセッションの預かりで精算できる**(再現: S1 の受諾を `AcceptDuel` 直前で止め、S1 を後始末で閉じて返金 → 同額で S2 を開始 → 止めていた S1 の受諾を再開すると、S2 の預かりで A と B を精算する)。親の決定(2026-09-15): **預かりに持ち主の印を持たせる** — `UserAccount` に `EscrowSession string`(`json:"escrow_session,omitempty"`)を足し、`OpenGame` / `AddToEscrow` がセッション ID を書き、`SettleGame` / `AcceptDuel` / `DeclineDuel` は**渡されたセッション ID と一致するときだけ**精算する(不一致は `ErrNoGameInProgress` 相当の新しいセンチネル `ErrEscrowMismatch`)。呼び出し側(C-2 の H&L / BJ、C-3b の duel、掃除人)はセッション ID を渡す。既存 JSON(印なし)は「印が空なら従来どおり通す」で後方互換を保つ(印が付くのは C3B-13 以降に開かれた盤面だけ)。回帰テスト: 上の再現手順で S1 の受諾が `ErrEscrowMismatch` で拒否され S2 の預かりが動かない / 正常な H&L・BJ・duel の精算が通る / 印の無い既存データの精算が通る。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go, internal/casino/types.go, internal/casino/errors.go, internal/commands/highlow.go, internal/commands/blackjack.go, internal/commands/duel.go, internal/commands/casino_sessions.go, internal/commands/highlow_test.go, internal/commands/blackjack_test.go, internal/commands/duel_test.go, internal/commands/casino_sessions_test.go
- notes: 危険地帯。**この印は C-2 の全ゲームを同じ穴から守る**(duel だけの問題ではない)。`Update` のクロージャ内から公開メソッドを呼ばない。

## C3B-14: 未送達時の後始末を盤面ロックの内側で行い、返金が成功するまで盤面を閉じない(blocking 1、P1)
- status: todo
- done-when: `duel.go` 464 付近の後始末(初回 `InteractionRespond` が失敗したときの取り下げ)が、(a) **盤面ロック(`Hold`)を取らずに `Close` する**ため受諾と競合し、(b) **返金の保存より先に `Close` する**ため返金に失敗すると預かりが取り残される(辞退も掃除人の自動返金も効かない)。両方直す: 後始末は受諾・辞退と**同じ per-board ロック**の内側で行い、`DeclineDuel`(返金)が**成功したときだけ** `Close` する。失敗したら盤面を残す(掃除人が 3 分後に再試行する)。C2-10 で掃除人に入れた「精算が成功するまで消さない」規律と同じ形。回帰テスト: 初回応答を失敗させ、返金も 1 回失敗させると盤面が残り、次の掃除人の巡回で返金されて `Escrow` が 0 になる / 後始末と受諾を競わせても二重精算が起きない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/commands/duel.go, internal/commands/duel_test.go, internal/commands/casino_sessions.go, internal/commands/casino_sessions_test.go
- notes: 危険地帯。C3B-13 の印が入っていれば競合しても誤精算は起きないが、**預かりが取り残される穴はこのタスクで塞ぐ**(印は誤精算を防ぐだけで返金はしない)。

## C3B-15: 挑戦の開始時に受け手の進行中ゲームも断る(blocking 4、P2)
- status: todo
- done-when: `/duel` の開始(`duel.go` 430 付近)が挑戦者の進行中しか見ないため、受け手が既にブラックジャック等で預けていても挑戦が公開され、挑戦者のチップが 3 分間拘束される(§4.5 は「どちらかに進行中のゲームがあれば断る」)。開始時に**受け手の `Escrow` も検査**して断る(文言は既存の「❌ 進行中のゲームがあります(先に決着してください)」に相手を示す形へ。例「❌ <@相手> は進行中のゲームがあります」)。受諾時の再検査は**残す**(3 分の間に相手が別のゲームを始めうるため)。検査は `Store` 側に読み取りメソッドを足して 1 回の `Update`/`Snapshot` で行う(コマンド層で口座を直接触らない)。回帰テスト: 受け手が預かり中なら挑戦が始まらず挑戦者のチップも減らない / 受け手が空いていれば従来どおり / 受諾時の再検査が生きている。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/commands/duel.go, internal/commands/duel_test.go, internal/casino/store.go, internal/casino/store_test.go
- notes: 受け手が「口座を持っていない」場合は進行中ではない(拒否しない)。

## C3B-16: 掲示済みの記録に失敗したときの再送を減らし、契約を明記する(blocking 5、P2)
- status: todo
- done-when: `casino_announce.go` 293 付近は結果の送信に成功した後の `MarkSeasonAnnounced` の失敗をログに残して続行するため、**送信済みなのに未送信として次の巡回で再送**される(メンション付きの結果が二度出る)。親の決定(2026-09-15): 完全な一度きりは二相コミットが要るので狙わない — **(a) 記録を短い間隔で数回(3 回まで)再試行し、(b) それでも失敗したら「送信済みだが記録できなかった」ことを警告としてログに残し、(c) 契約を `at-least-once`(記録に失敗した回は再送されうる)として設計書 §4 と `blocked/C3B-07.md` に明記する**。同じ形の記録(`MarkAnnounced`・宝くじの待ち行列)にも同じ再試行を入れるかは実装者が判断し、入れないなら理由を進捗に書く。回帰テスト: 記録が 1 回失敗 → 2 回目で成功すると再送されない / 3 回とも失敗すると警告が出て(再送はされうる)、次の巡回で記録が成功すれば以後は再送されない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/commands/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/commands/casino_announce.go, internal/commands/casino_announce_test.go, Docs/superpowers/specs/2026-09-15-casino-c3b-design.md, blocked/C3B-07.md
- notes: **嘘を書かない** — 「必ず一度だけ」とは書かず、再送されうる条件をそのまま書く(C-3a の「精算失敗は手動再試行だけが出口」と同じ扱い)。

## C3B-17: スロットの配当も上限で切り詰める(C3B-12 で見つけた最後の不揃い、P2)
- status: todo
- done-when: C3B-12 で他の精算経路を確認した結果、`Store.Spin`(`store.go` の `creditChipsLocked(account, result.Payout)`)だけが**上限で拒否する**形のまま残っている。設計書 §2 は「上限で入り切らない分は捨てる(**スロット等の配当と同じ**)」と書いており、スロットこそがその例として名指しされているのに実装が唯一の例外になっている。`SettleGame` と同じく `creditChipsCappedLocked` に変え、`SeasonNet` は入った額で数え、`SpinResult` に入った額と払うはずだった額の両方を残す。**ジャックポットは別の判断が要る** — 当たりで `economy.Jackpot = JackpotSeed` と一緒にリセットされるので、上限で入り切らない分をそのまま捨てるとプールの分まで消える。宝くじ(`drawLotteryLocked`)は溢れた分をプールへ戻しており、こちらも同じ形にできるか実装者が決めて理由を進捗に書く。回帰テスト: `MaxChips` 近くの口座がスロットを回すと拒否ではなく入る分だけ入る / `SeasonNet` が入った額で動く / 通常範囲の既存の期待値が 1 つも変わらない。
- verify: `go build ./... && go vet ./...`
- verify: `go test ./internal/casino/... -count=1`
- verify: `go test ./... -count=1`
- paths: internal/casino/store.go, internal/casino/store_test.go, internal/commands/slot.go, internal/commands/slot_test.go, Docs/superpowers/specs/2026-09-15-casino-c3b-design.md
- notes: `SettleGame` と違い**行き詰まりはしない**(取引ごと巻き戻るので賭け金は戻り、預かりも残らない)ので P1 ではない。`slot.go` の `ErrChipCapExceeded` の分岐は変更後に到達不能になるので一緒に消す。`ClaimDaily` と両替は精算ではない(進行中のものが無く、拒否しても利用者は元手を保ったまま)ので厳格なままでよい — 揃えない。
