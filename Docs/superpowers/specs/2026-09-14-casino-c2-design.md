# フェーズC-2設計: ボタン基盤+ハイ&ロー+ブラックジャック

**状態: ユーザー承認済み(2026-09-14)。** 承認された判断は 4 つ — (1) 進行中のゲームは「ベットだけ永続化(預かり)、盤面はメモリ」、
(2) ブラックジャックはヒット/スタンド/ダブル・スプリットなし、(3) ハイ&ローは確率連動の倍率 + いつでもキャッシュアウト、
(4) 放置は 3 分で自動決着(損をさせない)。C-1 の設計(`2026-07-10-casino-c1-design.md`)の基本方針・通貨規則・
危険地帯(単一ライター)はそのまま引き継ぐ。ループ(`TASKS.md` の C2 系)はこの文書を仕様として読み、反復の中で設計を再検討しない。

## 1. 目的と範囲

C-1 のスロットは「1 回押して終わり」。C-2 は **Discord のメッセージコンポーネント(ボタン)** で
「途中で判断するゲーム」を 2 つ足し、以後のゲーム(C-3 の /duel 等)が使い回す基盤を置く。

| 入るもの | 入らないもの(C-3 以降) |
|---|---|
| ボタン配線の基盤(custom_id 規約、所有者検査、セッション管理、放置の自動決着) | セレクトメニュー・モーダル |
| `/highlow <bet>`(ハイ&ロー、連勝の積み上げとキャッシュアウト) | ジャックポット積立・宝くじ・/duel・シーズン制 |
| `/blackjack <bet>`(ヒット/スタンド/ダブル) | スプリット、保険、サレンダー |
| ベットの預かり(escrow)と再起動時の返金 | 盤面の永続化(再起動後の続行) |

## 2. 共通規則(C-1 から継承 + C-2 で追加)

- 通貨は `int64`、**端数は常に切り捨て**、丸め損はハウス側。ベット幅は **10〜1,000 チップ**(スロットと同じ)。
- ギルドごとに独立。ギルド外(DM)では「❌ サーバー内で使用してください」。口座は初回アクセスで自動開設(C-1 の `EnsureCasinoAccess`)。
- **1 ユーザーにつき 1 ギルドで同時に 1 ゲーム**。進行中があれば「❌ 進行中のゲームがあります(先に決着してください)」。
- **処理順序は「永続化 → 表示」**(スロットと同じ)。ボタン 1 回の処理は「所有者検査 → 盤面へ適用(純粋関数)→ 決着なら精算を
  `Update` 1 回で永続化 → メッセージ編集」。Discord API の失敗や編集中のクラッシュは精算に影響しない。
- 大勝ち祝い(C-1 の公開メンション投稿)は、ブラックジャックのナチュラル、ハイ&ローの **7 連勝以上でのキャッシュアウト** で流用する。

## 3. ベットの預かり(escrow)

`UserAccount` に 3 フィールドを足す(JSON は `omitempty`。既存データはそのまま読める):

```go
Escrow         int64  `json:"escrow,omitempty"`          // 進行中ゲームに預けたチップ(ベット + ダブル分)
EscrowGame     string `json:"escrow_game,omitempty"`     // "highlow" | "blackjack"
EscrowOpenedAt string `json:"escrow_opened_at,omitempty"` // RFC3339(JST)
```

`casino.Store` の公開メソッド(すべて `Update` 1 回、絶対規則 3 に従う。クロージャ内から公開メソッドを呼ばない):

- `OpenGame(guild, user, game, bet, now)` — 進行中(`Escrow > 0`)なら `ErrGameInProgress`、残高不足なら `ErrInsufficientChips`。
  `Chips -= bet; Escrow = bet`。
- `AddToEscrow(guild, user, amount)` — ダブル用。残高不足なら拒否。`Chips -= amount; Escrow += amount`。
- `SettleGame(guild, user, payout)` — `Escrow = 0; Chips += payout`(`payout` は 0 以上。預かりの返還を含む総額)。
  `Escrow == 0` の口座への精算は `ErrNoGameInProgress`(二重精算の防止)。
- `RefundStaleEscrows(now)` — **起動時に 1 回**呼ぶ。全ギルド・全口座の `Escrow > 0` を `Chips` に戻して 0 にし、件数を返す
  (再起動で盤面が消えた分の返金。盤面はメモリなので、起動直後に預かりが残っていることは「消えた盤面」と同義)。
- 通貨の保存則: 上の 4 つのどの経路でも `Chips + Escrow` の合計は「ベット時に減り、精算時に配当分だけ増える」以外に動かない。

## 4. ボタン基盤(`internal/commands/components.go`)

- `custom_id` の規約: `casino:<game>:<sessionID>:<action>`(例 `casino:highlow:8f3a…:high`)。100 文字以内。
- `ComponentHandler` インターフェース `{ Prefix() string; HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate, sessionID, action string) error }`。
  `RegisterComponent(h)` で `init()` 自己登録(コマンドと同じ流儀)。重複 prefix は起動時 panic。
- `cmd/bot/main.go` の `InteractionCreate` ハンドラに **`InteractionMessageComponent` の分岐を 1 つ足す**
  (`commands.DispatchComponent(s, i)`)。これが C-2 での `main.go` の変更 1 点目(2 点目は §3 の起動時返金)。
- **所有者検査**: セッションの所有者と押した人(`i.Member.User.ID`)が違えば、本人だけに見える(ephemeral)
  「❌ これはあなたのゲームではありません」を返し、盤面に触らない。
- 決着したメッセージのボタンは **無効化(disabled)** して残す(押しても「このゲームは終了しています」)。

## 5. セッション管理(`internal/casino/session.go`、discordgo 非依存)

- `SessionManager`: `map[sessionID]*Session` と `map[guild+user]sessionID` を 1 つの `sync.Mutex` で守る。
  `Session{ID, GuildID, UserID, Game, State any, LastActionAt, MessageRef}`。
- **放置**: 最終操作から **3 分**で自動決着。`SessionManager` は `now func() time.Time` を注入され、
  `Sweep(now)` で期限切れを返す。常駐 goroutine(30 秒周期、C-1 の掲示スケジューラと同じ ctx で止まる)が
  `Sweep` → 各ゲームの `AutoResolve`(ハイ&ローはその時点の得をキャッシュアウト、未プレイなら全額返金。
  ブラックジャックは自動スタンド)→ 精算を永続化 → メッセージを「⌛ 時間切れ — 自動決着」に編集。
- ボタン処理と Sweep の競合: セッション単位の処理は `SessionManager` のロックを取ってから盤面に適用する。
  精算済みセッションは即座に map から消す(二重決着の構造的防止。さらに `SettleGame` の `ErrNoGameInProgress` が二重目を弾く)。

## 6. ハイ&ロー(`/highlow <bet>`、`internal/casino/highlow.go`)

- 52 枚 1 組をゲームごとにシャッフル(乱数源は注入可能)。**引いたカードは戻さない**(残りの山から確率を計算する)。
  ランクは 2〜14(A が最高)。
- 表示: 現在のカード、現在の得(ポット)、連勝数、「⬆️ ハイ」「⬇️ ロー」「💰 キャッシュアウト」の 3 ボタン。
  それぞれの当たる確率と倍率をボタンのラベルか本文に出す(**読める**こと)。
- 判定: ハイは次のカードのランクが現在より**大きい**とき勝ち、ローは**小さい**とき勝ち。**同ランクは負け**。
  次のカードが現在のカードになる。
- 倍率(確率連動): `p = 勝ちになる残りカード枚数 / 残り枚数`。`m = floor((95 / p_percent) * 100) / 100`
  (整数演算: `m_x100 = 9500 / p_permille … ` は計画で確定。ハウス 5 %)。`pot = floor(pot × m)`(通貨の切り捨て規則)。
  `p = 0` の選択肢(A で「ハイ」など)はボタンを無効化する。
- 連勝の上限 **10**(到達で自動キャッシュアウト)。ポットの上限はベットの **100 倍**(超えたら上限で自動キャッシュアウト)。
- キャッシュアウトはポットを支払う(`SettleGame(payout = pot)`)。**未プレイ(0 連勝)のキャッシュアウトは全額返金**。
  外したら `SettleGame(payout = 0)`。
- 期待値: 1 手あたり RTP 95 %(統計テストで確認)。
- RTP 95 % はポットが大きいときの値。倍率もポットも切り捨て(丸め損はハウス側)なので、ベット 10 台では丸めで 93〜94 %。

## 7. ブラックジャック(`/blackjack <bet>`、`internal/casino/blackjack.go`)

- 6 デッキのシューをゲームごとにシャッフル。プレイヤー 2 枚(表)、ディーラー 2 枚(1 枚伏せ)。
- ナチュラル: プレイヤーのみ → **3:2**(`payout = bet + floor(bet × 3 / 2)`)。両者 → プッシュ(返金)。ディーラーのみ → 負け。
- ボタン: 「🃏 ヒット」「✋ スタンド」「⏫ ダブル」。ダブルは**最初の判断でだけ**有効で、追加ベット分を `AddToEscrow`
  (残高不足なら「❌ チップが足りません」で無効)。ダブル後は 1 枚だけ引いて自動スタンド。
- ディーラーは **17 以上でスタンド(ソフト 17 もスタンド)**。バーストは即負け(ディーラーは引かない)。
- 精算: 勝ち `payout = 2 × 総ベット`、プッシュ `payout = 総ベット`、負け `0`。
- 期待値の検証は「ルールどおりに動くこと」のシナリオテストで行う(戦略に依存するので RTP の統計テストはしない)。
  ただし「配当は総ベットの 2.5 倍を超えない」「バースト後に引けない」「ソフト 17 で止まる」は境界テストで固定する。

## 8. コマンドと表示

| コマンド | 内容 | 表示 |
|---|---|---|
| `/highlow <bet>` | ハイ&ローを開始。盤面は公開メッセージ、ボタンは所有者のみ | 公開 |
| `/blackjack <bet>` | ブラックジャックを開始 | 公開 |

- 盤面は embed。決着時に結果(勝ち/負け/プッシュ、配当、残高)を同じメッセージへ編集し、ボタンを無効化する。
- `/help` の一覧に 2 本を足す。

## 9. エラーハンドリング(C-1 の文言に揃える)

- 残高不足: 「❌ チップが足りません(現在: N枚)」/ ベット範囲外: 「❌ ベットは10〜1,000チップです」
- 進行中あり: 「❌ 進行中のゲームがあります(先に決着してください)」
- 他人のボタン: 「❌ これはあなたのゲームではありません」(ephemeral)
- 通信エラーのログは C-1 の R-003 で入れた秘匿ヘルパーを通す(Interaction トークンをログに出さない)。

### 精算が拒まれたとき(手動の再試行が唯一の出口)

決着した盤面は `WithSession(done=true)` でセッションから外れる。**掃除人はそれを二度と見ない** —
`Sweep` が回るのは期限切れの生きた盤面だけで、外れた盤面はどの巡回にも現れない。したがって精算が
拒まれた盤面を進めるものは、押した人自身か、次回起動の `RefundStaleEscrows`(預かりを全額返す)しかない。

- 案内は `casinoSettleFailedMessage`(「❌ 精算に失敗しました。もう一度お試しください。」)で、
  盤面には 🔁 の 1 個(`casinoActionSettle`)だけを残す。押下は**手を繰り返さず支払いだけ**やり直す。
- 金額行(`casinoPayoutLine`)は呼ばない。拒まれた精算は Payout も Balance も生んでいないので、
  0/0 を整形すると「配当 0 枚」という嘘になる。
- **自動の再精算は次回起動の `RefundStaleEscrows` だけ**。これは配当ではなく預かりの返金で、
  プロセスが再起動するまで走らない。

このため C2-12 の done-when が求めた「⚠️ 精算に失敗しました。次回の自動処理で精算されます」は**採らない**。
待てば精算される自動処理は存在せず、その文言は待てば済むという嘘になる。文言は手動の再試行を求めるまま据え置き、
契約をここに残す。回帰は `highlow_test.go` / `blackjack_test.go` が `Data.Content` ごと固定する
(値の一致に加えて「自動/次回/お待ち」を約束しないことを検査する)。

## 10. テスト方針

- 純粋関数(カード・ハイ&ローの確率と倍率・ブラックジャックの進行と精算): 乱数注入の決定的テスト + 統計テスト(ハイ&ロー 1 手の RTP 95 %±1 %、100 万手)。
- Store: 預かりの保存則(`Chips + Escrow` の合計)、二重精算の拒否、進行中の重複開始の拒否、`RefundStaleEscrows` の返金、並行ベットの回帰(`-race` はこの PC で不可 = 既知。非 race の並行テストで代替)。
- セッション: TTL の期限切れ・二重決着なし・所有者検査。時計は注入。
- コマンド: `Handle()` から純粋関数を切り出してテスト(実 Discord をモックしない)。ボタンの `custom_id` の生成と解析は往復テスト。

## 11. アーキテクチャ(依存方向)

```
internal/casino/cards.go       デッキ・シュー(乱数注入)
internal/casino/highlow.go     確率・倍率・進行(純粋)
internal/casino/blackjack.go   進行・ディーラー・精算(純粋)
internal/casino/session.go     メモリ上のセッションと期限切れ(discordgo 非依存)
internal/casino/store.go       escrow の 4 メソッド(単一ライター)
internal/commands/components.go   custom_id 規約・所有者検査・ComponentHandler の自己登録と dispatch
internal/commands/highlow.go / blackjack.go   コマンド + ボタン + 表示
cmd/bot/main.go                InteractionMessageComponent の分岐、起動時の RefundStaleEscrows、Sweep goroutine の起動
```

`internal/casino` は引き続き標準ライブラリのみ。`internal/commands` → `internal/casino`。逆流禁止。

## 12. 危険地帯

- `casino.Store` の escrow(資金の保存則)と、セッションの二重決着 — 別文脈の評価者を通す(`--evaluate feature`)。
- `cmd/bot/main.go` の変更は上記 3 点だけ。
