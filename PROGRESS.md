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

- C2-01(ボタン基盤: `custom_id` の生成/解析・`ComponentHandler` の自己登録・`DispatchComponent`・所有者検査)。`internal/commands/components.go` と `components_test.go`、`cmd/bot/main.go` に `InteractionMessageComponent` の分岐 1 つ。検証出力: `.harness/runs/20260914-121315/verify-C2-01-{1,2,3,4}.txt`(build+vet clean / 対象テスト 32 PASS・FAIL 0 / `./cmd/...` ok / `go test ./...` 8 パッケージ ok)。

- C2-01 の評価者指摘(`custom_id` の 100 文字判定がバイト数)= c666f3c。`ParseCustomID` の長さ判定を `utf8.RuneCountInString` に直した。Discord の上限は文字数なので、日本語 80 文字のセッション ID を含む 100 文字・260 バイトの ID は受理されなければならない。回帰は `components_test.go` の境界表に多バイトの 100 文字(受理)と 101 文字(拒否)を追加。検証出力: `.harness/runs/20260914-121315/verify-C2-01fix-{1,2}.txt`(build+vet clean / 対象テスト 全 PASS・FAIL 0)。

- C2-02(`casino.Store` の預かり 4 メソッド)= 3331a8d。`UserAccount` に `Escrow` / `EscrowGame` / `EscrowOpenedAt`(`omitempty`)、`OpenGame` / `AddToEscrow` / `SettleGame` / `RefundStaleEscrows` をそれぞれ `Update` 1 回で。総資産は `totalAssetsLocked` に寄せ、`TopAssets` と `ViewAccount` の両方が `Escrow` を含める。検証出力: `.harness/runs/20260914-121315/verify-C2-02-{1,2,3,4}.txt`(build+vet clean / 対象テスト 73 PASS・FAIL 0 / casino ok / `go test ./...` 8 パッケージ ok)。

- C2-02 の評価者指摘(返金でチップが消失する)= 28ff79a。`creditChipsLocked` の上限判定に `Escrow` を含めた — 預かり中は Chips が空いて見えるので、日次受取や両替が Chips を `MaxChips` まで埋め、続く返金に置き場が無くなっていた。返金は `moveFromEscrowLocked`(`moveToEscrowLocked` の逆)にして切り捨てを廃止した(預かりは元から本人の金なので、返すのは通貨の生成ではない)。回帰は `store_test.go` の 3 件(預かり中の `/daily`・`/exchange` は `ErrChipCapExceeded`、手編集で上限超過の口座も全額返金)。修正前は 3 件とも落ちることを確認した: `.harness/runs/20260914-121315/verify-C2-02fix-0-baseline.txt`。検証出力: 同 `verify-C2-02fix-{1,2,3}.txt`(build+vet clean / 対象テスト 80 PASS・FAIL 0 / `go test ./...` 8 パッケージ ok)。

- C2-03(カードの山とハイ&ローの純粋ロジック)= 8c81337。`cards.go`(`Card`/`Suit`/`Deck`、`NewDeck(n, rng)` は Fisher-Yates、`Draw`/`Remaining`(コピー)/`Len`)と `highlow.go`(`HighLowGame` は全フィールド非公開+アクセサ、`Odds`/`Multiplier`/`Guess`/`CashOut`/`AutoResolve`)。検証出力: `.harness/runs/20260914-121315/verify-C2-03-{1,2,3,4}.txt`(build+vet clean / `TestDeck|TestHighLow` ok・exit=0 / `go test ./...` 8 パッケージ ok / gofmt 差分なし)。統計テストの実測は 1 手あたり RTP 0.9479(100 万手・0.7 秒)。

- C2-03 の評価者指摘(固定山でのロー勝利が未検証)= 8446bbc。`highlow_test.go` に `TestHighLow_LowGuessGrowsThePotAndAdvancesTheBoard` を追加 — 現在 7 / 引く順 `[3,9]` / ベット・ポット 1000 で `Guess(false)`、ポット 1900・現在 3♠・連勝 1・残りハイ 1 枚とロー 0 枚(`LowMultiplier == 0`)を固定。もう 1 件(「指定差分が `paths:` 内に収まっていない」)はコード変更なし: `store.go` / `store_test.go` の変更は C2-02 修正の別コミット `28ff79a`、C2-03 の比較基点は `28ff79a..8c81337`。検証出力: `.harness/runs/20260914-121315/verify-C2-04-3.txt`(`TestDeck|TestHighLow` 37 PASS・exit=0)。

- C2-04(ブラックジャックの純粋ロジック)= 375c178。`blackjack.go`: `BlackjackGame`(全フィールド非公開+アクセサ)、`NewBlackjack(bet, rng)` は 6 デッキのシューから表順(プレイヤー→ディーラー→プレイヤー→ディーラー)に 4 枚配り、ナチュラル 3 分岐を配布時に決着。`Hit` / `Stand` / `Double`(`CanDouble` = 生きている・2 枚・未ダブル)、`playDealer` は `HandValue < 17` の間だけ引く、`Settle`、`AutoResolve`(= スタンド)、`HandValue`(A は 11、入らなければ 1 枚ずつ 1 に落とす)。検証出力: `.harness/runs/20260914-121315/verify-C2-04-{1,2,4}.txt`(build+vet exit=0 / 対象テスト 42 PASS・FAIL 0・exit=0 / `go test ./...` 8 パッケージ ok)。

## In progress
- (なし)

## Next
- **次は C2-05**(セッション管理と放置の自動決着)。`internal/casino` の純粋ロジック 2 つ(`HighLowGame` / `BlackjackGame`)は揃った — どちらも `AutoResolve() int64` を持つので、セッション管理はこの 1 メソッドだけを知っていればよい。`internal/commands` 側の配線は C2-06 以降。
- (済)~~次は C2-04~~(ブラックジャックの純粋ロジック)。`cards.go` の `Deck`/`Card` はそのまま使える(`Draw` は末尾から引く。テストの `deckOf` が引く順で並べ替える)。`internal/commands` 側の配線は C2-06 以降。
- (済)~~次は C2-03~~(`internal/casino/cards.go` + `highlow.go` の純粋ロジック)。C2-01 で置いた `RegisterComponent` はまだ登録者ゼロ — 最初の利用者はハイ&ロー(C2-06)。C2-05 の `SessionManager` はまだ無い。
- **C-2 開始(2026-09-14)**: 仕様 `Docs/superpowers/specs/2026-09-14-casino-c2-design.md`、タスク C2-01〜C2-08(`TASKS.md`)。ブランチ `feat/casino-c2`。順に消化する。
- **C-1 は完了**(2026-09-14: R-001〜R-004 着地、Astra 2 周目 PASS、`main` へ ff マージ)。次は稼働(トークンと実行場所はユーザー判断)か C-2(ボタン基盤+ハイ&ロー+ブラックジャック、未設計 → M1 から)。
- `TASKS.md` の未完は無し(R-001〜R-004 すべて done)。次は R-003 修正差分の評価者 2 周目(前回指摘への対応差分だけを見る)。
- Astra の C-1 レビュー(`.harness/reviews/2026-09-14-astra-casino-c1-round1.md`)の所見を R 系タスクにして消化 → 2 周目 PASS → `main` へ ff マージ(ユーザー承認済み 2026-09-14)→ 片付け。稼働(トークン・実行場所)は後日、ユーザー判断。

## Notes
- **ブラックジャックの設計(C2-04 で決めた)**: 状態は「進行中 / 終了」の 2 値で、勝敗は `BlackjackResult`(未決・プレイヤー勝ち・ディーラー勝ち・プッシュ・ナチュラル)に分けた。バーストは独立の結果にしない — 相手の勝ちであり、バーストした合計は手札に残るので表示側が読める。`Settle` は状態遷移ではない純粋な読み取り(`CashOut` と違い 2 回呼んでも同じ数を返す)で、二重決済を止めるのはセッション削除と `SettleGame` の `ErrNoGameInProgress`。
- **ディーラーの 17 はソフト判定を持たない(C2-04)**: `HandValue(dealer) < 17` の間だけ引く。A+6 は `HandValue` が 17 を返すので止まる — 「この 17 はソフトか」という分岐を書かないことがソフト 17 スタンドの実装そのもの。テストは結果で見分ける(誤ってヒットすると A+6+4 = 21 でプッシュになる山を渡す)。
- **ダブルの順序の危険(C2-04)**: `Double` は `CanDouble` が偽なら `ErrDoubleUnavailable` を返すが、コマンド層は**先に `CanDouble` を見てから `AddToEscrow`** を呼ぶこと。逆順にすると古いメッセージの ⏫ で追加ベットだけ預かられ、手札が数えない預かりが残る(C2-06 以降の配線で守る)。
- **3:2 の丸め(C2-04)**: `payout = bet + floor(bet*3/2)`。ベット 1 枚なら 2 枚(`floor(1.5) = 1`)。配当の上限は総ベットの 2.5 倍で、ナチュラルはダブルできない(配布時に終わる)ので 2 つの倍率が掛け合わさることはない。境界テストは `2*payout <= 5*totalBet` で見る(除算の丸めを入れない)。
- **配布をテストから駆動する seam(C2-04)**: `newBlackjackFromShoe(bet, shoe)` は `NewBlackjack` からシャッフルを抜いたもの。ナチュラルの決着は配布の規則なので、終了済みの手札を手で組むテストでは検証にならない。固定山は `cards_test.go` の `deckOf`(引く順に並べる)を使う。
- **シューは切れない(C2-04)**: 312 枚の中で 1 手は終わるので `mustDraw` の空シューは panic。`Draw` の `ok=false` を無視してゼロ値の `Card` を配ると rank 0 のカードとして描画され 0 点で数えられるため、壊れたシューには黙って続けるより落ちる方を選んだ(`errDeckExhausted` と同じ理由、扱いだけ違う)。
- **ハイ&ローの数式(C2-03 で確定)**: 倍率は x100 整数の `Multiplier(winning, remaining) = 95*remaining/winning` 一式で、切り捨ては最後に 1 回だけ(常にハウス有利)。ポットは `floor(pot*m/100)`。**同ランクは分母にだけ入る**(高い/低いのどちらの分子にも数えない)ので、ハウス取り分は 5 % + 同ランク分。上限 100 倍は**上限で**支払う(設計書 §6「超えたら上限で自動キャッシュアウト」)— `growPot` が clamp し、オーバーフロー時も cap を返すので clamp は全域。1 手あたり RTP の実測は 0.9479(95 %±1 % に収まる)。
- **`errDeckExhausted` は到達不能**(C2-03): `Odds` が数える山と `Guess` が引く山は同じなので、当たり枚数 > 0 なら必ずカードが残る。将来この結合が壊れたときに大声で落ちるための防御で、非公開・テスト無し。
- **預かりと上限の関係(C2-02 の指摘で確定)**: チップ上限 `MaxChips` は **Chips + Escrow** に掛かる。付与(`creditChipsLocked`)は預かりを数え、口座内の移動(`moveToEscrowLocked` / `moveFromEscrowLocked`)は数えない。返金は「返せない」があってはならない — 切り捨てもエラーもチップの消失か永久ロックになる。
- **預かりの規律(C2-02 で決めた)**: 保存則は `Chips + Escrow`。預け入れ(`OpenGame` / `AddToEscrow`)は同一口座の 2 フィールド間の移動なので合計を動かさず、合計が動くのは `SettleGame` に渡した `payout` のときだけ。だから預け入れは `creditChipsLocked` を通さない(通貨を作らないので上限判定の対象外)。`payout` は**預かりの返還を含む総額** — 呼び出し側でベットを足さない。二重決着は二重に守る: セッションを map から消す(C2-05)+ `SettleGame` の `ErrNoGameInProgress`。`AddToEscrow` も `Escrow == 0` を拒む(仕様は残高不足しか書いていないが、進行中でないゲームへの追加は返す先が無い)。
- **総資産に預かりを含める(C2-02)**: `totalAssetsLocked(account, rate)` 1 箇所に寄せた。`TopAssets` と `ViewAccount` の両方がこれを使う — 含めないとゲーム中だけランキングと `/balance` から掛け金が消えて、決着で復活する。
- **`RefundStaleEscrows` は年齢を見ない**: 盤面はメモリなので、起動時に残っている預かりは定義上「二度と決着しない盤面」。`now` 引数は `OpenGame` との対称性のために取るだけで判定には使わない。手編集で `Chips + Escrow > MaxChips` になっているファイルは `MaxChips` まで返して預かりを消す(中断すると全ギルドの口座が永久に「進行中」で固まる方が悪い)。
- **ボタン基盤の規律(C2-01 で決めた)**: `custom_id` は `BuildCustomID/ParseCustomID` 以外で組み立てない。`ParseCustomID` は 100 文字超・要素数 4 以外(過不足とも)・名前空間違い・空要素をすべて `ok=false` にする。`DispatchComponent` は `i.MessageComponentData()` ではなく `i.Data` のカンマ ok 型アサーションを使う(前者は component 以外の Interaction で panic する)。未知のボタンに無言で返さない — 無応答は押した人に「この操作は失敗しました」と出る。
- **所有者検査の形**: `requireSessionOwner(i, ownerID)` は不一致の文言を返すだけ(`requireGuildContext` / `requireAdministrator` と同じ流儀)。ephemeral で送るのは呼び出し側。押した人の ID は既存の `resolveUserID(i)`(guild は `Member.User`、DM は `User`)。どちらかが空文字なら不一致扱い(`"" == ""` で他人に盤面を渡さない)。
- **`custom_id` の長さは文字数(C2-01 の指摘)**: Discord の 100 上限は文字数なので、長さ判定は `utf8.RuneCountInString`。境界テストは ASCII だけだと素通りする — 多バイトの行を必ず入れる。
- **ボタンのテストの取り方**: 応答の中身は、204 を返して直近のリクエストボディを保持する `capturingTransport`(`components_test.go`)で実際に送った JSON を読む。ビルダ関数を単体で見るより配線ごと確かめられる。
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
