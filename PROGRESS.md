# PROGRESS — todayistodayBot

セッション/反復の引き継ぎ。毎回の開始儀式で最初に読み、反復の終わりに更新する。
`git log` が第二の記録。ここには git に無いこと(判断・未解決・次に見るべき場所)を書く。

## Done
- C3B-P2(盤面枠を先に取り、資金は後から動かす)。証拠 `.harness/runs/20260915-101429/verify-C3B-P2-{1,2,3,4}.txt`(4 本とも exit=0、8 パッケージ ok)。
  **直したのは順序ひとつで、返金の作り込みではない。** 開始は「預かる → 盤面を登録する」だった。登録が失敗したときの
  返金は best-effort(失敗したらログだけ)で、**どの盤面にも紐づかない預かり**が残りうる — 2 周目レビューの再現は
  S1 の後始末が**返金を済ませて `Close` する前**の区間に S2 の開始が入り、`OpenGame(S2)` は空の預かりを見て成功、
  `OpenWithID(S2)` は残っている S1 に阻まれて `ErrGameInProgress`、その返金も失敗する、というもの。
  順序を **(1) ID を作る →(2) `OpenWithID` で盤面を登録 →(3) `OpenGame` で預かる →(4) (3) が失敗したら盤面を消す**
  に変えた。**(2) はメモリだけで資金を動かさないので、(2) が断ったときに返金すべきものが存在しない**。
  (4) も `OpenGame` がエラー時に何も永続化しない契約なので、消すのは盤面だけで返金は要らない。
  つまり**「預かりがあるのに盤面が無い」が構造的に作れなくなる**(印 C3B-13 と対。印は「この預かりは誰のものか」、
  こちらは「預かりより先に持ち主を置く」)。
  **再現区間そのものも、同じ順序で閉じる** — S1 の返金と `Close` の間に入った S2 は `OpenWithID` で断られ、
  **1 枚も預けないまま**引き返す。利用者には「ゲーム中」の断りが一瞬出るが、資金は動かない。
  **`/highlow` の未送達経路も「返金 → 成功したら `Close`」に揃えた**(`withdrawUndeliveredBoard`)。板ごとのロック +
  `Hold` の下で走るのは `/duel` と同じ理由(押下との三つ巴)。返金が失敗した盤面は**残す** — 手つかずの H&L は
  `AutoResolve` がポット(= 賭け金)のキャッシュアウトなので、3 分後の掃除人が同じ額を返す。従来の「先に `Close`」は
  返金が失敗すると再起動まで預かりが残り、その間その利用者はカジノ系を一切遊べなかった。
  **ブラックジャックだけ揃えきれていない(意図的、`withdrawUndeliveredHand` にコメント有り)** — 返金の前後関係は
  揃えたが、返金が失敗しても手を閉じる。BJ の `AutoResolve` は**スタンド**なので、盤面を残すと
  **利用者が一度も見ていない手を掃除人が打ち、賭け金を失わせうる**。「遅いが必ず全額戻る(再起動)」と
  「3 分で解放されるが負けうる」のどちらを取るかは製品判断なので、**C3B-21** として起こした(資金が失われる穴では
  ないので P2)。
  **効き目の確認**(3 本、いずれも狙ったテストだけが落ちる):
  `mutation-C3B-P2-stake-first.txt` = 順序を元に戻すと 4 件。うち再現テストは
  `challenger: 900 chips / 100 escrow, want 1000 / 0` で落ちる — **レビューの再現がそのまま数字で出る**。
  `mutation-C3B-P2-board-left-behind.txt` = (4) の `Close` を外すと 3 件(預かりに失敗した開始が盤面を残す)。
  `mutation-C3B-P2-close-before-refund.txt` = H&L を「先に `Close`」に戻すと 1 件(掃除人が再試行できる盤面が消える)。
  **テストの決め手は `flakyBank.opens`(`OpenGame` の呼ばれた回数)**。「預かりが 0」では弱い — 資金が動いてから
  返金がたまたま成功した場合にも成り立ってしまう。**一度も呼ばれていない**ことが順序の主張そのもの。
  既存の契約テスト 2 件(`...StakesBeforeTheBoard/Hand...`)は**契約ごと差し替えた**(`...TakesTheBoard/HandBeforeTheChipsMove`)
  — 主張が反転したので期待値の書き換えではない。通常の開始・精算・辞退・時間切れの期待値は 1 つも変えていない。
  **レビューは 1 差分 2 周の上限に達しているので、今回は評価者を呼んでいない**(2 周目の blocking への対応がこれ)。
- C3B-16(掲示済みの記録に失敗したときの再送を減らし、契約を明記する)。証拠 `.harness/runs/20260915-080323/verify-C3B-16-{1,2,3}.txt`(3 本とも exit=0、8 パッケージ ok)。
  **再試行は重複を減らすだけで無くさない。それを隠さずに書くのが本題だった。** 送信(Discord)と記録(`MarkSeasonAnnounced`)は
  別々の操作でトランザクションが無く、記録は**送信成功の後**にしか取れない(逆順だと送信に失敗した回の結果が「掲示済み」になって永久に消える)。
  だから「送信済み・未記録」の窓は原理的に残る。親の決定どおり二相コミットは狙わず、(a) 3 回までの再試行 / (b) 警告 / (c) 契約の明記の 3 点だけ入れた。
  **再試行の待ちを `SleepFunc` に相乗りさせず `markRetryWaiter` という別の引数にした** — 既存のテストの stub は
  `sleep` が false を返すことで巡回そのものを止めている。再試行の待ちを同じ関数で表すと、stub の戻り値は
  「再試行するな」と「巡回を止めろ」の両方を意味してしまい、どちらを言っているのか区別できない。別の口にすると
  テストが**2 つの試行の間に**割り込める — これが回帰テストの成立条件で、実際 `retry` の中でストアを直して
  「1 回目は失敗・2 回目は成功」を決定的に作っている(実時計に依存しない)。
  **ログは `Error` ではなく `Warn`** — 失われたものは無い(結果はチャンネルに届いている)。代償は後の巡回で出る 2 通目だけなので、
  重さが違う。文面も `MarkSeasonAnnounced failed` から「送信済みだが記録できなかった / 後の巡回で再掲示されうる」に変えた。
  重複がチャンネルに出る前に、その理由がログに残っている状態にするのが狙い。
  **記録の失敗をテストで起こす seam は「パスにディレクトリを置く」**(`breakableStore`)。ファイルを消すのでは駄目 —
  欠損は初回起動前の状態で、`readLocked` は空の `Data` を返し `writeLocked` はディレクトリを作り直すので、
  記録は**空の経済に対して成功してしまう**。ディレクトリなら `os.ReadFile` が全 OS で失敗する
  (Linux は EISDIR、Windows は `Access is denied.`)ので `Update` ごと失敗する。
  **効き目の確認**: `mutation-C3B-16-no-retry.txt`(再試行を消すと新規 2 件が「retried after 0 gaps」で落ちる)/
  `mutation-C3B-16-bare-error-log.txt`(警告を元の `slog.Error("casino: MarkSeasonAnnounced failed")` に戻すと
  `AnUnrecordedResultWarnsAndIsPostedAgain` が「no at-least-once warning」で落ちる)。既存 13 件の掲示テストは
  引数が 1 つ増えただけで期待値は 1 つも変えていない。
  **`MarkAnnounced`(日次 embed + 宝くじの待ち行列)には再試行を入れていない。理由は範囲だけ** — 呼び出しが
  `internal/casino/announce.go` にあり C3B-16 の `paths:` の外。**穴の形も重さも同じ**で、こちらが書けないと
  次の巡回で embed と**当選者へのメンション**が両方もう一度出る。設計上の理由ではないので `TASKS.md` に
  **C3B-19** として起こした(P2)。設計書 §4 と `blocked/C3B-07.md` にもその通り書いてある(「重複しない」とは書いていない)。
  **反復 4 の評価者所見(P2)も処理した** — C3B-15 の `paths:` に `internal/commands/casino_shared.go` を足した。
  親が `NEXT_FINDINGS.md` に「置き場所は妥当、狭すぎたのは `paths:` 記載。この節は消してよい」と判断済みだったので、
  記載を実態へ広げてコードは動かさず、節を消した。
- C3B-12(精算の配当を口座の上限で切り詰め、精算そのものは必ず成功させる)。証拠 `.harness/runs/20260915-080323/verify-C3B-12-{1,2,3}.txt`(3 本とも exit=0、8 パッケージ ok)。
  **拒否が壊していたのは入金ではなく取引だった** — `SettleGame` は `ensureSeasonMonthLocked` を**同じ `Update` の中で**先に呼ぶ。月次切り替えは表彰台へ賞与を払い、その賞与が口座を上限まで埋める。そのあとの入金が `creditChipsLocked` で `ErrChipCapExceeded` を返すと、閉じたばかりの月次切り替えごと巻き戻る — つまり**再試行が拒否された状態を自分で作り直す**。預かり(`Escrow`)を抱えた手は永久に閉じない。上限は行き先の性質であって取引を失敗させる条件ではない、というのが §2 の読み。
  **入金は `creditChipsCappedLocked` に変え、`SeasonNet` は `credited-staked`**(払うはずだった額ではなく入った額。§2 の既存規則)。`SettleResult` は `Payout` を**実際に入った額**にし、払うはずだった額を新しい `Owed` に移した — 表示側(`casinoPayoutLine` / `blackjackResultEmbed` / 連勝の祝い)は全部 `Payout` を読むので、**フィールドの意味を入れ替える形にすると表示が自動的に本当のことを言う**。`Owed` を足したのは差が観測できるようにするため。
  **預かりを先に解放してから上限を測る**のは意図 — 引き分け(`payout == staked`)は上限に座った口座でも必ず全額入る。自分のチップが二重に数えられて自分の賭け金が消える、という形にはならない。
  **効き目の確認**: `mutation-C3B-12-strict-credit.txt`(入金を `creditChipsLocked` に戻すと新規 2 件が落ちる)/ `mutation-C3B-12-seasonnet-from-owed.txt`(`SeasonNet` を `payout-staked` に戻すと同じ 2 件が `SeasonNet = 1000, want 500` / `100, want 0` で落ちる)。
  **既存テストのうち上限の期待値 2 つは契約ごと差し替えた** — `TestSettleGame_PayoutOverTheChipCapPersistsNothing` → `..._KeepsOnlyWhatFits`、`TestSettleGame_RefusalsLeaveSeasonNetUntouched` の後半は上限ではなく負の配当の拒否に置き換えた(拒否の一覧から上限が外れたため)。**通常範囲の期待値は 1 つも変えていない**(`go test ./... -count=1` が 8 パッケージ ok)。
  **他の精算経路を確認した結果**: `AcceptDuel`・シーズン賞与・宝くじは既に `creditChipsCappedLocked`。`ClaimDaily` と両替は**精算ではない**(進行中のものが無く、拒否しても利用者は元手を保つ)ので厳格なままでよい。残った不揃いは `Store.Spin` ひとつ — §2 が「スロット等の配当と同じ」と名指しする当の経路が唯一拒否する側に残っている。**ジャックポットの溢れをどこへ送るかという別の判断が要る**ので今回の `paths:` の外とし、`TASKS.md` に **C3B-17** として起こした(行き詰まりはしないので P2)。
  設計書 §2 は冒頭の「湧かない・消えない」に**上限の例外**を足し、「上限は配当を止める理由にしない」を項として明示した(non-blocking 1)。
  **危険地帯なので別文脈の評価を通した。1 周目が blocking 1 件** — 本文を `settled.Payout` にしただけでは、ハイ&ローの**無効化されたキャッシュアウトのボタン**が `board.Pot` を名乗り続けるので「配当: 100枚」と「キャッシュアウト 200枚」が同居する。**負けは `g.pot = 0`** なのでこのボタンは従来ずっと精算結果と一致していた = 上限だけが唯一の嘘になる、が対応を決めた根拠。**金額を名乗るのは押せる間だけ**にした(生きている行は「押せばこれだけ取れる」という申し出なのでポットを名乗る / 決着した行は金額を出さず、金額を言うのは本文ひとつだけ)。これで `DisabledComponents` 経由の**時間切れ経路も同時に塞がる** — あちらは `SettleResult` を一切受け取らないので、ボタンの側で閉じるしかない。`SettleTimedOutBoard` を実装して盤面のポットを書き換える案は、掃除人の `PayoutResolved`(一度きりの AutoResolve)の規律を写すことになるので採らなかった。効き目: `mutation-C3B-12-cashout-label.txt`。
  **2 周目 PASS**(対応差分だけを見る規約どおり)。ただし向こうは sandbox で `go test` を起動できていない(`mkdir: Access is denied.`)ので、合格の根拠は `verify-C3B-12-{4,5,6}.txt`(修正後の再実行、3 本とも exit=0)。
- C3B-09(掲示済みの日でも未掲示のシーズン結果だけを再送する)。証拠 `.harness/runs/20260915-064206/verify-C3B-09-{1,2,3,4}.txt`(4 本とも exit=0、8 パッケージ ok)。
  **分けたのは収集の条件であって、送信の形ではない** — `AnnouncementJob.DailyOwed` は「今日の embed はまだか」だけを答える。`collectDailyAnnouncements` は `AnnounceChannelID == ""` だけで切り上げ、そのあと `dailyOwed := today > LastAnnounced` と `len(UnannouncedSeasons) == 0` の**両方**が成り立つときにだけギルドを飛ばす。ギルドを起こす理由が 2 つあり、2 つ目が 1 つ目の印に縛られていなかった、というのが所見 2 の中身。
  **`DailyOwed == false` の巡回が抑えるのは 3 つ**: embed 本体・宝くじの祝い(`LotteryDraws` を収集側で空にする)・`MarkAnnounced`。宝くじを抑えるのは当選者を二度 @メンションしないため、`MarkAnnounced` を抑えるのは、この巡回がしていない掲示を根拠に宝くじの待ち行列を剪定させないため。結果を取り除くのは従来どおり `MarkSeasonAnnounced` だけで、これは送信の直後に月ごとに取る。
  **効き目の確認**: `mutation-C3B-09-no-results-only-job.txt`(収集のゲートを `!dailyOwed` だけに戻すと新規 2 件が落ちる)/ `mutation-C3B-09-embed-not-suppressed.txt`(送信側の `if job.DailyOwed` を `if true` にすると同日再送のテストが落ちる)。既存の「翌日に再送される」テスト(`AFailedSeasonResultIsSentAgainNextPass`)は日をまたぐので、どちらの変異でも落ちない — 同日の巡回を見る新しいテストが必要だった理由がこれ。
  **`NEXT_FINDINGS.md` の反復 2 節は節ごと消した。** 同居していた「paths 確認」(`2910ca0` に `internal/casino/store.go` の空白整形 4 行が混ざっている)は、既に着地したコミットの話で今から分離できないため、対応不要として一緒に閉じた。
  **評価者の未処理所見はタスクへ起こした** — 反復 4 の所見 1 = C3B-10(月末の送信失敗の回帰テスト)、反復 3 の所見 1・2 = C3B-11(duel のゼロサムと全口座を触る経路の説明)。どちらも `paths:` が今回の外なので手は付けていない。
- C3B-08(未掲示のシーズン結果を月替わりで失わない)。証拠 `.harness/runs/20260915-064206/verify-C3B-08-{1,2,3,4}.txt`(4 本とも exit=0)。
  **待ち行列は宝くじと同じ形にした(親の決定)** — `GuildEconomy.UnannouncedSeasons []SeasonResult`(上限 3、溢れたら**前から**落とす)。`rolloverSeasonLocked` が閉じた結果を**追記**し、取り除くのは `MarkSeasonAnnounced` = 送信が確認できた月だけ。`LastSeason` は `/season` の表示用に最新 1 件として残るが、**掲示はもう読まない** — 月替わりごとに無条件で上書きされる値だったのが所見 1 の原因そのもの。
  **上限を 3 にしたのは宝くじの 7 と理由が違う**。1 件 1 件が表彰台を @メンションするので、溜めて一度に出すこと自体が費用。1 か月に 1 件しか増えないので 3 = 四半期分。
  **表彰台が空の月は待ち行列に入れない**。祝いの本文が `""`(= 送らない)になる結果で、入れると静かな月ごとに 3 枠のうち 1 枠を食い、**本物の表彰台を前から押し出す**。`LastSeason` には従来どおり入るので `/season` の表示は変わらない。
  **既存 JSON(キー無し)を落とさないための移行を入れた** — `migrateUnannouncedSeasonsLocked` は `rolloverSeasonLocked` の**先頭**で走る(早期 return より前)。上書きの直前に拾うのが要点で、月をまたいで止まっていたボットが 1 回の呼び出しで「6 月を待ち行列へ → 7 月を閉じる」の順に進む。判定は宝くじの移行と同じ `LastSeason.Month > LastSeasonAnnounced`、二度入れない保証は「待ち行列が空でなければ何もしない」。`LastSeasonAnnounced` の役目はこれだけになった(収集はもう見ない)。
  **掲示側は月ごとに 1 通・古い順で、失敗したらそこで止める**。次の月を先に出すと回が前後して見えるので、`return nil`(embed は載っている)で残りを次の巡回へ回す。印は月ごとに `MarkSeasonAnnounced` で、`MarkAnnounced` と同じく `Month <= month` で剪定する(飛行中に閉じた回は後ろの月なので生き残る)。
  **効き目の確認**(`.harness/runs/20260915-064206/mutation-C3B-08-no-queue.txt`): 追記 1 行を外すと新規 3 件が落ちる(7/31 の巡回で結果が運ばれない / 2 か月分が溜まらない / 上限の検査が 1 件しか見ない)。`NoChannelKeepsASeasonAcrossTheMonthBoundary` は移行だけでも通るので、この変異では落ちない。
  **前の反復が未コミットで残していた `NEXT_FINDINGS.md` の反復 3 節は、先に単独でコミットした**(`395acca`)。混ぜると 1 コミット 1 論理変更が崩れる。
- C3B-07(README・設計書の実測・`architecture.md` の追記案)。証拠 `.harness/runs/20260915-064206/verify-C3B-07-{1,2,3,4}.txt`(4 本とも exit=0。4 本目は実測の根拠にした 5 テストの `-v` 出力)。
  **実測の数字は全部テストから採った**(書いてから走らせたのではなく、走っているテストの期待値を写した): duel は 1,000+1,000 の 2 人が 200 を賭けて 1,200/800、保存ファイル上の `Chips + Escrow` 合計は 2,000 のまま(`TestAcceptDuel_MovesThePotToTheWinnerAndKeepsTheTwoAccountsSummedUnchanged`)。上限の例は `MaxChips − 100` の挑戦者が 400 ではなく 300 を受け取り `SeasonNet` は +100(`..._WinnerAtTheCapTakesOnlyWhatFitsAndSeasonNetCountsThat`)。賞与は 1 ギルド 1 回の切り替えで最大 17,500 チップ、6 人の例で残高が 11,000/6,000/3,500(`TestStore_SeasonRollover_ClosesMonthPaysPodiumAndZeroesEveryAccount`)、上限に当たると `Bonus` は 3,000 / 0(`..._BonusCappedAtMaxChips_RecordsWhatLanded`)。
  **README の `/duel` は「どちらも3分間…」の行の後ろに置いた**。その行はハイ&ローとブラックジャックの 2 本を指しており、間に 3 本目を挟むと「どちらも」が誰を指すか壊れる。duel の 3 分は**受諾を待つ時間**で、他 2 本の「操作が無い時間」とは別物なので、duel 自身の箇条書きで書いた。
  **`Docs/agent-guide/architecture.md` は触っていない**(展開コピー)。追記案は `blocked/C3B-07.md` — レイヤー表の 1 行(`duel.go` / `season.go`)、日次ロールオーバーの項目を 3 つ → 4 つ(シーズンの切り替えを**宝くじの抽選の前**に。逆順だと月境界と同じ呼び出しに当たった抽選の純利が毎月 1 日に消える)、危険地帯 7 点目(duel のゼロサム・賞与の意図的発行 17,500・全口座を触る唯一の書き込み)。正本へ写して再展開するのは**人**。
- C3B-06(9 時掲示のシーズン欄と閉じた回の結果発表)= コミット `2910ca0`。証拠 `.harness/runs/20260915-064206/verify-C3B-06-{1,2,3,4}.txt`(4 本とも exit=0)。
  **掲示済みの判定は `GuildEconomy.LastSeasonAnnounced`(月 `"2006-01"` の high-water)**。宝くじが**待ち行列**(`Unannounced`)を持つのは、掲示が止まっている間に抽選が毎日積み上がるから。シーズンは**月に1回しか閉じない**ので、保持するのは `LastSeason` 1 件で足り、必要なのは「その1件を channel が既に見たか」だけ = 境界1本。判定は `LastSeason.Month > LastSeasonAnnounced`(`!=` ではない — C3-19 と同じ理由で、時計が戻ったギルドが済んだ月を開き直して上位3名を二度 @メンションするのを断つ)。空文字の月は `MarkSeasonAnnounced` の no-op(`"" > x` は常に偽)なので、閉じた回を持たない巡回が境界を白紙に戻せない。
  **`LastAnnounced` と分けたのが要点**。embed と結果発表は**別メッセージで別々に失敗する**。1本の境界にすると、結果発表だけが落ちた朝に (a) 境界を進める = 祝いを失う、(b) 進めない = 翌朝 embed ごと二重掲示(宝くじ当選者も再 @メンション)、のどちらかしか選べない。2本あるので「embed は載った、結果は次の巡回へ」が言える。
  **`MarkSeasonAnnounced` は `runAnnouncePass` ではなく `internal/commands` のコールバック内で呼ぶ**。`AnnounceCallback` は `error` 1 本しか返さないので、「embed は成功・祝いは失敗」を `runAnnouncePass` へ伝える経路が無い。送信の直後に印を付けるのが唯一の場所(宝くじの祝いが `sendText` の失敗を**ログのみ**にしているのと同じ判断。ただしこちらは**印を付けずに `return nil`** する = 次の巡回が同じ結果をまた運ぶ)。
  **上位3名は `SeasonRanks` を `topAssetsLocked` の後で呼ぶ**。`SeasonRanks` は保存値を**丸めずに**比較するだけなので、手編集の `season_net` を弾くのは `topAssetsLocked` が通りがけに回す `normalizeAccountLocked`(`SeasonStatus` が同じ理由で明示ループを持つ)。件数は `seasonRankLimit`(3)で、リテラルの 3 は使わない — 掲示の表彰台と精算の表彰台がずれない。
  **祝いの本文は `Bonus`(実際に入った額)を出す**。上限に座った勝者は表の 10,000 より少ない額しか受け取らないので、`SeasonBonus(rank)` を出すと残高が裏付けない数字になる(`LotteryDraw.Prize` と同じ規律)。表彰台は**メダル3枚で切る** — 手編集の `last_season` が 50 行持っていても 50 人を @メンションしない。`Ranks` が空の月(誰も遊ばなかった)は本文が `""` = 送らないが、**印は付ける**(再送しても誰も居ない)。
  **掲示チャンネル未設定なら何も起きない**。`collectDailyAnnouncements` がそのギルドを飛ばすので job 自体が出ず、印も付かない。結果は `LastSeason` に残るので `/season` から読め、後でチャンネルを設定した巡回がそのまま運ぶ(`TestCollectDailyAnnouncements_NoChannelKeepsTheClosedSeasonPending` が両方を固定)。
  **既存テストの期待値を 1 か所だけ直した**: embed のフィールド数 3 → 4(`len(embed.Fields)`)。主張は「どのフィールドが在るか」で、弱めていない。
  **「コピーを返す」テストは書かなかった**。`Update` は毎回ファイルから読み直し `Store` は Data を持たない(`Snapshot` の doc)ので、`job.SeasonClosed` にストア側のポインタを渡す変異を入れても**ファイルは壊れない** = 落ちないテストになる。実装のコピーは残す(`LotteryDraws` と同じ防御)が、落ちない検査は証拠ではないので消した。
  **効き目の確認**(`mutation-C3B-06.txt`、3 本。いずれも狙ったテストだけが落ちる): M1 high-water 判定を外す → 掲示済みの回が毎朝よみがえる 4 件 / M2 送信失敗でも印を付ける → 失敗した結果が二度と送られない 1 件 / M3 job から今月の表彰台を落とす → 1 件。
- NEXT_FINDINGS(反復 1 の NEEDS_WORK: 月替わりの競合で表示月と残り日数が食い違う)= コミット `c79bad2`。証拠 `.harness/runs/20260915-064206/findings-C3B-05-1.txt`(exit=0)。
  **月は永続値・残り日数は引数と、出所が違った**。`SeasonStatus` の `Month` は `economy.SeasonMonth`(ロールオーバーが**前進のみ**で書く)、`DaysLeft` は呼び出し側がロック前に読んだ `now`。JST 8/01 00:00 と 7/31 23:59:59 が**逆順で**ストアに着くと、後者は 8 月の表を渡されたまま 7 月の最終日を数えて「8月・残り1日」と出す。
  **直し方は `seasonDaysLeftIn(month, now)`** — 遅れて着いた `now` を**その月の初日まで引き上げる**。逆向きに食い違うことは無い(ロールオーバーが同じ `Update` の中で走るので、`SeasonMonth` が `now` の月より後ろにはならない)。読めない `season_month` は `now` にそのまま落とす(`lotteryDrawDateLabel` と同じ — 手編集の値から月の長さを推測しない)。
  **効き目の確認**: `DaysLeft` を `SeasonDaysLeft(now)` に戻すと `TestStore_SeasonStatus_LateArrivalCountsTheMonthItIsShown` が評価者の再現どおり `DaysLeft = 1, want 31` で落ちる。
- C3B-05(`/season` と `/balance` の今月の純利)= コミット `bc14bec`。証拠 `.harness/runs/20260915-064206/verify-C3B-05-{1,2,3}.txt`(3 本とも exit=0)。
  **`SeasonStatus` はロールオーバーを通す読み取り**。`ensureTodayRateLocked` を呼ぶので、月初の `/season` は前月を閉じて賞与を払ってから今月の(空の)表を返す — 通さないと「もう誰かの手で閉じられる運命の表」を現役として見せることになる。順位表は `SeasonTopLimit`(10)で切るが**自分の順位は切らない**: `SelfRank` は全体順位なので 12 位の人には「12位」と出る(0 = 純利 0 の圏外)。`Players` は純利が動いた人数で、`SeasonResult.Players` と同じ定義。
  **`Last` はスライスごとコピーして返す**。`economy.LastSeason` のポインタを渡すと、表示側の書き込みが次の `Update` のスナップショットに載る。テストは返った `Last` を書き換えてからファイルを読み直して確かめている。
  **残り日数は今日を含める**(`SeasonDaysLeft`)。月末は「残り1日」= 今日で終わり。切り替えは次の深夜なので今日はまだ遊べる、という意味に合わせた。月の長さは**翌月1日の前日**から取るので 2 月も閏年も分岐が要らない。JST 固定(`now.In(jst)`)— UTC で数えると 7/31 15:00 UTC(= JST 8/01)が 7 月の最終日に見える。
  **`/help` は何もしていない**。一覧はレジストリそのもので、`/duel` と `/season` は `init()` の自己登録で既に載る。`help_test.go` に 2 本を足したのは、`init()` が消えたときに気づくのが `/help` のテストだけだから。
  **コマンド側テストの時計**: `internal/commands` からストアの時計は差せない(非公開)。決着は**ストアの時計**の月へ計上され、`SeasonStatus` は**引数の `now`** を使うので、固定 `now` と実決着を混ぜると月が食い違う。ストア越しの 1 件は実時計で回している(純粋関数側は固定値)。
- NEXT_FINDINGS(反復 4 の NEEDS_WORK 2 件)= コミット `ac6a88f`。証拠 `.harness/runs/20260915-064206/findings-C3B-04-{1,2}.txt`(2 本とも exit=0)。
  **[P1] 受諾の失敗で盤面を閉じる条件を反転した**。`duelAcceptKeepsTheChallenge`(残す側を列挙)は、列挙しなかった**保存 I/O の失敗**を「預かりは消えた」と誤読していた。`Update` は書けなければ何も永続化しないので預かりは口座に残り、そこで `Close` すると 🚫 も掃除人も届かず**再起動まで口座が「進行中」で固まる**。`duelAcceptDropsTheChallenge` は落とす側だけを列挙する(`ErrNoGameInProgress` = 預かりが無いか別ゲームのもの)。既定は**残す** — 残しすぎても 3 分で掃除人が返すが、閉じすぎると出口が無い。
  **[P2] 受け手判定を `WithSession` の中へ移した**。`WithSession` はエラーを返さなかった呼び出しで必ず `LastActionAt` を更新するので、判定が外にあると**第三者の押下でも失効期限が延びる**(拒否され続ける人がボタンを叩くだけで返金を止められる)。クロージャ内で拒否し `errDuelNotOpponent` を返す = 期限を触らない。文言は `requireDuelOpponent` のものを外へ持ち出す(エラー値は経路の制御だけを持つ)。
  **効き目の確認**: 2 つの修正をそれぞれ単独で戻すと、対応する新規テストだけが落ちる — (1) を戻すと `0 challenges are live, want 1`、(2) を戻すと accept / decline 両方で `1 challenges survived the sweep`(= 返金されない)。
- C3B-04(`/duel` コマンドと受諾・辞退ボタン)= コミット `95cc63c`。証拠 `.harness/runs/20260915-033205/verify-C3B-04-{1,2,3}.txt`(3 本とも exit=0)。
  **所有者検査がこのゲームだけ逆向き**。`Session.UserID` は挑戦者で、ボタンを押してよいのは**受け手**(§4.5)。共有の `requireSessionOwner` は使えない(文言も §6 で別)ので `requireDuelOpponent(i, board.OpponentID)` を別に置いた。受け手 ID は盤面(`casino.DuelState.OpponentID`)が持つ — マネージャは知らない。挑戦者自身の押下も**他人と同じく弾く**(テスト `TestDuelOnlyTheReceiverCanPress` が挑戦者と無関係の 2 人で見ている)。
  **失効は「決着」ではなく「取り下げ」なので、掃除人の既定経路に乗せられない**。C-2 の掃除人は `AutoResolve() int64` → `SettleGame(guild, session.UserID, payout)` 一本で、duel をそこに通すと (a) 返金が `creditChipsLocked` の上限に当たって**掛け金の一部が消える**(`DeclineDuel` が `moveFromEscrowLocked` を選んだのと同じ理由)、(b) タイトルが「⌛ 時間切れ — 自動決着」になる。そこで `casino_sessions.go` に**3 つ目の任意インターフェース** `timedOutBoardSettler{ SettleTimedOutBoard(*casino.Session) (SettleResult, error) }` を足し、`sweepIdleBoards` の精算部分を `settleSweptBoard` へ切り出した。実装しないゲームは従来どおり `AutoResolve` → `SettleGame`(既存テストは 1 件も変えていない)。**`DuelState` は `casino.AutoResolver` を実装しない** — 「この盤面が 1 人の player に何を負っているか」という 1 つの数に、2 人分の帰結は入らない。
  **効き目の確認**: `settleSweptBoard` のインターフェース分岐を無効化する変異で `TestDuelTimedOutChallengeIsWithdrawnAndRefunded` が 3 行(返金されない / Stage が pending のまま / 編集が出ない)落ち、ログに `a swept board cannot resolve itself game=duel` が出ることを確認した。テストは `bank.settles != 0` も見ている — 返金が `SettleGame` を通っていないことの証拠。
  **受諾の失敗は 2 種類に割れる**。受け手のチップ不足・受け手が別ゲーム中は「まだ答えていない」だけなので ephemeral で断って**盤面を残す**(他の誰かが受けることはできない。§4.5)。それ以外(`ErrNoGameInProgress` / `ErrDuelStakeMismatch`)は**挑戦の裏にある預かりが既に無い**ので盤面ごと閉じる — 押しても必ず失敗するボタンを残さない。この分岐は `duelAcceptKeepsTheChallenge` 1 か所。
  **`AcceptDuel` が成功したら編集より先に `Close`**。失効と受諾が競うと掃除人が「取り下げ」で上書きしうるが、押下は `Hold` を握っているので `Sweep` はこの盤面を返さない(C2-08 の busy 判定)。`Close` はその `Hold` の中で呼ぶ。
  **`UserValue` / `IntValue` を使わない**。どちらも値の形が想定外だと panic し、discordgo はハンドラ goroutine に recover を張らない(= bot ごと落ちる)。`duelTarget` / `duelBet` はカンマ ok で読む。Bot 判定は option の値ではなく `data.Resolved.Users[id].Bot` から取り、`Resolved` が無ければ **false**(欠けたフィールドで実在の相手を断るより、Bot への挑戦を 3 分で返金する方が安い)。
  **`casinoBank` に `AcceptDuel` / `DeclineDuel` を足した**。`flakyBank` は `*casino.Store` を埋め込んでいるのでテスト側の変更は不要。
- NEXT_FINDINGS(反復 3 の Astra 判定 NEEDS_WORK: 月またぎの精算が前月へ混入する)= コミット `6304801`。証拠 `.harness/runs/20260915-033205/recheck-C3B-03-4-{1,2,3}.txt`(3 本とも exit=0)。
  **月次ロールオーバーは読み取り経路の住人だったが、精算は読み取りではない**。`SettleGame` / `Spin` / `AcceptDuel` は呼び出し側から `now` を受け取らず、`ensureTodayRateIndexLocked` を**一度も通らずに** `addSeasonNetLocked` に到達する。7/31 に配られて 8/01 00:01 に払われた手は 7 月の純利に足され、次の表示がその 7 月を**8 月の結果込みで**締めて 8 月を 0 から開く(= 誤った月に計上され、正しい月からは消える)。
  **直し方は `ensureSeasonMonthLocked(economy)` を 3 つのクロージャの先頭へ**。`rolloverSeasonLocked(economy, jstMonth(s.nowLocked()))` だけを呼ぶ(レート履歴と抽選は表示側の仕事で、呼び出し側の `now` を要る)。冪等かつ**前進のみ**なので、月の変わらない日は何もしない。**credit の前**に呼ぶこと — 切り替えは全口座の `SeasonNet` を 0 にするので、逆順は計上しようとしている結果そのものを消す。
  **効き目の確認**: 3 つの呼び出しを外した木で新規 3 テスト(`SettleGame` / `Spin` / `AcceptDuel` の月またぎ)が `LastSeason = nil` で落ちる。`BuyLotteryTickets` は先頭で `ensureTodayRateLocked` を呼ぶので元から通っており、`drawLotteryLocked` はロールオーバーの**後ろ**にある(順序の理由は `ensureTodayRateIndexLocked` のコメント)。
- C3B-02(`SeasonNet` を全ゲームの決着へ配線)= コミット `0a775b6` + `292b86a`。証拠 `.harness/runs/20260915-033205/verify-C3B-02-{4,5,6}.txt`(3 本とも exit=0)。
  **計上点は 5 つ**: `Spin`(`result.Payout - bet`)・`SettleGame`(`payout - staked`)・`BuyLotteryTickets`(`-cost`)・`drawLotteryLocked`(`+paid`)・`AcceptDuel`(C3B-01 で既出)。どれも**入金が成功したあと**に呼ぶ。`Spin` と `SettleGame` は `creditChipsLocked` が全か無かなので「入った額」と「払うはずの額」が一致するが、宝くじと duel は `creditChipsCappedLocked` なので一致しない — 上限に座った当選者は入る分しか取れず、**計上するのは `paid` であって `prize` ではない**(§2)。ここを `prize` にする変異はテストが落とす。
  **`SettleGame` が「賭けた額」を知る唯一の手段は `Escrow`**。`clearEscrowLocked` の**前**に `staked := account.Escrow` を読む。この値は bet + ダブルの総額なので、ダブルした手は `bet` ではなく総額が引かれる — 200 賭けて 400 戻る行(`+200`)が、bet だけを引く実装で落ちる行。
  **宝くじだけ賭けと受けが別の日に起きる**。購入は `BuyLotteryTickets` で即計上する — 券には返金経路が無く、抽選が走らなくても(bot が落ちたまま等)チップは戻らないため。預かり(`Escrow`)を持つ他のゲームと非対称なのはこの一点。結果として、単独購入者が自分の壺を当てても**ハウスの 10% だけ負けている**のが盤に出る(2 枚 100 → 当選 90 → 純利 -10)。
  **返金は結果ではない**: `DeclineDuel` と `RefundStaleEscrows` は `SeasonNet` に触らない。掛け金は `SettleGame` を通らなかったので損として計上されておらず、返金側で足すと逆に得になる。
  **正規化は `normalizeAccountLocked`**(`ensureAccountLocked` 経由)。`Chips`/`Coins` と違い**両端クランプ**(`[-MaxChips, MaxChips]`)で、0 で床を張らない — 負けている人の負の値が正しい読みだから。ファイルの `math.MaxInt64` は次の加算でラップし、**最下位の人が盤の首位に来る**。
  **反証**: 変異 7 件すべてをテストが検出(計上の削除 ×3、賭け額の引き忘れ、`paid`→`prize`、購入代金の未計上、正規化の削除)。評価者 Astra は `PASS`、非 blocking の指摘 1 件(除外系テストがゼロ始まりで「既存の純利をゼロに戻す」誤実装を見逃す)を `292b86a` で解消 — `ClaimDaily` に `account.SeasonNet = 0` を挿す変異で検出を確認した。
- C3B-01(duel の純粋ロジックと 1 トランザクションのゼロサム精算)= コミット `e6a6f8f`。C-3b の最初の 1 件。
  **`duel.go`(純粋)**: `GameDuel`(`GameKind`)・`DuelStage`(`DuelPending` / `DuelSettled`)・`DuelState{ChallengerID, OpponentID, Bet, Stage}`・`FlipDuel(rng) bool`(`rng.Intn(2) == 0`)・`DuelPayout(bet, challengerWins) (challenger, opponent int64)`(勝者 `2*bet`・敗者 0)。`DuelState` に `OpponentID` を持たせたのは**所有者検査が他の全ゲームと逆向き**だから — `Session.UserID` は挑戦者なのに、ボタンを押してよいのは受け手だけ(§4.5)。盤面にこのフィールドが無いと C3B-04 は誰も弾けない。`DuelPayout` は `bet` が `[1, MaxChips]` の外なら **(0, 0)** を返す(`AcceptDuel` が先に弾くので観測不能。`2*bet` が wrap して**負の配当**になるのを構造的に断つだけの防御)。
  **`AcceptDuel(guild, challenger, opponent, bet, challengerWins) (DuelSettlement, error)` は 1 回の `Update`**。2 回に割ると、受け手を預かってから精算するまでの間に**その預かりを解放できる盤面が存在しない**窓ができる(落ちれば起動時返金まで塩漬け、並行受諾ならその窓に滑り込んで壺を二度払う)。`challengerWins` は呼び出し側が `FlipDuel` で引いて渡す(注入。store は日次レート以外の乱数を持たない)。
  **拒否は 6 種で、どれも何も永続化しない**: `ErrDuelSelf`(両席が同一ユーザー = `ensureAccountLocked` が**同じポインタ**を返し、1 口預かって 2 口払う = `2*bet` の発行)/ `ErrInvalidAmount`(`bet` が範囲外)/ `ErrNoGameInProgress`(挑戦者が duel の預かりを持っていない = **二重精算のガード**。受諾が最初にその預かりを 0 にするのはこのため)/ `ErrDuelStakeMismatch`(預かりが `bet` と違う = 払う壺 `2*bet` と預かり総額がずれ、**ゼロサムが崩れる**)/ `ErrGameInProgress`・`*ErrInsufficientChips`(受け手側)。**受け手の失敗で挑戦者へ返金しない**のは §4.5 の明示 — 盤面は生きたままで、受け手は入金してやり直すか断るか失効を待つ。
  **`EscrowGame` を額と一緒に見る**のが今回効いた点。duel は**他人の操作(受諾・掃除人)で決着する最初のゲーム**なので、「挑戦が閉じたあとに挑戦者が始めたブラックジャックの預かり」と区別が要る。`Escrow > 0` だけだと、古い duel ボタンがその BJ の掛け金を精算・返金してしまう(mutation `M2` で `DeclineDuel` 側と 2 件同時に落ちる)。
  **`DeclineDuel` は `moveFromEscrowLocked`**。§4.5 の文言は `SettleGame(payout = bet)` 相当だが、credit は `MaxChips` で切り詰めるので**手編集で上限超の挑戦者が自分の掛け金を取り戻すだけでチップを失う**し、拒否(`ErrChipCapExceeded`)なら預かりが永久に残って全ゲームから締め出される。逆向きの move なら `Chips + Escrow` が出発点にぴったり戻る(`RefundStaleEscrows` と同じ論法)。
  **`UserAccount.SeasonNet`(§3)を足した**(`json:"season_net,omitempty"` — 既存 JSON はキーが無いので 0 で読める)。受け取ったのは**実際に口座へ入った額**で数える(§2): 上限に座った勝者は入る分しか入らないので、払うべき額で数えると**残高が裏付けない順位**が出る。読み取り点の正規化は C3B-02 の担当なので、ここでは書き込み側だけを閉じた — `addSeasonNetLocked` が**加算前に現在値を ±MaxChips へ寄せてから**足す(ファイル由来の `season_net` は無界なので、結果だけ丸めても加算自体が wrap する)。
  **効き目の確認**(`mutation-C3B-01.txt`、6 本。いずれも狙ったテストだけが落ちる): M1 預かり額の一致検査を外す → 受諾が通ってしまう / M2 `EscrowGame` 検査を外す → 他ゲームの掛け金を触る 2 件 / M3 `SeasonNet` を払うべき額で数える → 上限のケースが `200, want 100` / M4 自分自身のガードを外す → `ErrGameInProgress` が返り発行を止められない / M5 コインを `Intn(2) == 1` へ倒す → 決定性 / M6 敗者に掛け金を残す → 配当表とゼロサムとストアの合計。
  検証出力: `.harness/runs/20260915-033205/verify-C3B-01-{1,2,3}.txt`(build+vet exit=0 / casino ok exit=0 / `go test ./...` 8 パッケージ ok exit=0)、`verify-C3B-01-4-duel-subtests.txt`(新規 10 テスト・サブテスト 19 件すべて PASS)。既存テストの期待値は 1 つも変えていない。
- C3-19(移行の掲示チャンネル条件を外し、掲示済み境界を後退させない = 3 周目レビューの blocking 2 件)= コミット `ccf1aa6`。
  **(1) [P1] チャンネル未設定のギルドで旧当選が消える**: C3-18 で置いた `AnnounceChannelID == ""` の除外を**外した**(下の C3-18 の項の「宛先が無いギルドは移行しない」は**この項で覆っている** — 当時の判断が見落としていたのは、待つ間に窓が閉じることだった)。「後からチャンネルを設定すればそのとき移行される」は成り立たない — 移行の窓は**次の抽選まで**で、`drawLotteryLocked` が `LastDraw` を上書きした瞬間に旧当選は**どこにも残らない**。移行の条件は「保存する価値があるか」だけで、「今日どこかへ送れるか」は `collectDailyAnnouncements` 側の別の問いとして残る(`drawLotteryLocked` がチャンネルを見ないのと同じ理由)。
  **(2) [P2] 時計の巻き戻りで掲示済みの当選が復活する**: `MarkAnnounced` の `LastAnnounced` を**単調非減少**にした(`date > LastAnnounced` のときだけ書く)。読み手が 2 つともこれを上限線として使っている — `collectDailyAnnouncements` の「今日はもう掲示した」判定と、移行の「`LastDraw.Date > LastAnnounced` なら未掲示」。後退させると両方の判定が済んだ日に対して開き直り、9/14 が二度掲示され当選者が二度祝われる。**待ち行列の刈り取りは同じ扱いにしない** — 刈るのは「今出ていったメッセージに載っていた回」で、これは今日の日付が何であれ事実。
  **効き目の確認**(`mutation-C3-19.txt`): 2 つの修正をそれぞれ単独で戻すと、対応する回帰テストだけがレビューの再現どおりに落ちる — (1) を戻すと `Unannounced[0]` が 9/13 の u9 ではなく 9/14 の u1(= 旧当選が抽選に上書きされて消えた)、(2) を戻すと 9/14 の当選を載せた job が 2 回出る。
  **既存テストを 1 件直した**(`TestLotteryDraw_NoBuyersRollsThePrizeForwardAndKeepsTheLastResult`)。これは C3-18 で条件を絞る根拠に使ったテストで、今回は**期待値ではなく前提を直した**: 種として置いていた 7/01 の `LastDraw` に対して `LastAnnounced` を設定していなかったため、ファイルの姿が「未掲示の当選を持つ旧形式」と**区別できず**、移行が(契約どおり)それを待ち行列に入れていた。`LastAnnounced = older.Date`(= 掲示済み)を種に足して、「閑散日の抽選は何も待ち行列に入れない」という本来の主張だけを測るようにした。テストの主張も閾値も弱めていない。
  検証出力: `.harness/runs/20260915-031759/verify-C3-19-{4,5,6}.txt`(build+vet exit=0 / casino ok exit=0 / `go test ./...` 8 パッケージ ok exit=0。いずれも mutation を戻した最終ツリーで取り直し済み。`-{1,2,3}` は既存テスト 1 件が赤だった修正前の記録として残してある)。
- C3-18(旧形式が残した未掲示の当選を待ち行列へ移行する = 2 周目 blocking 1)= コミット `056ad7b`。`NEXT_FINDINGS.md` の反復 1 の節(設計書 §8 の契約の矛盾)は先に処理して削除し、その文言直しは別コミット `3447f50` に分けた。
  **穴**: `collectDailyAnnouncements` は `Unannounced` だけを読むので、C3-12 より前の形式(`unannounced` キーが無く、未掲示の当選が `LastDraw` にだけある)のファイルを読むと、その回の掲示と祝いが**誰からも上がらない**。再現は `last_announced="2026-09-13"` / `last_draw.date="2026-09-14"`(当選者あり)/ `unannounced` 無しで 9/14 10 時。
  **直し方**: 読み取り点(`normalizeLotteryLocked`)に `migrateUnannouncedLocked` を足した。条件は「待ち行列が空」かつ「`LastDraw` が非 nil・当選者あり・`LastDraw.Date > LastAnnounced`」で、写しを 1 件だけ入れる。`LastAnnounced` はギルド側にあるので `normalizeLotteryLocked` の引数を `*Lottery` → `*GuildEconomy` に変えた(既存の表テストは `GuildEconomy{Lottery: tc.in}` に包み替えただけで、ケースも期待値も不変)。**空の待ち行列という条件そのものが冪等性**で、移行した 1 件が次の巡回で自分を止める。位置が要件 — 正規化点は `drawLotteryLocked` より**前**に走るので、その日の抽選が `LastDraw` を上書きする前に写しが取れる(`TestCollectDailyAnnouncements_MigratesBeforeTodaysDrawOverwritesLastDraw` が 2 件・古い順を固定)。
  **掲示チャンネルが無いギルドは移行しない** — ここは既存テストに教わった点で、最初の実装はチャンネルの有無を見ずに移行し、`TestLotteryDraw_NoBuyersRollsThePrizeForwardAndKeepsTheLastResult`(掲示設定の無いギルドに古い `LastDraw` を置いて閑散日を回すテスト)を落とした。**テストの期待値ではなく条件の方を絞った**: 待ち行列は掲示を養うためだけに在り、宛先が無いギルドは `collectDailyAnnouncements` 自身が飛ばすので、書いても誰も読まない。取りこぼしでもない — 移行は読み取り点なので、後からチャンネルを設定した時点で同じ条件が成立する(`TestMigrateUnannounced_WaitsForAnAnnouncementChannel` が両方を固定)。`drawLotteryLocked` がチャンネルを見ずに追記するのは正しいまま — あちらは**起きた出来事の記録**で、こちらは**古いファイルの修復**。
  **効き目の確認**: `migrateUnannouncedLocked` の呼び出しを外すと新テスト 3 件が落ちる(`mutation-C3-18-no-migration.txt`: job が空 / 9/13 の当選者が消えて 1 件だけ / チャンネル設定後も空)。既存テストは 1 件も期待値を変えていない。
  検証出力: `.harness/runs/20260915-025434/verify-C3-18-{1,2,3}.txt`(build+vet exit=0 / casino ok exit=0 / `go test ./...` 8 パッケージ ok exit=0。いずれも mutation を戻したあとの最終ツリーで取り直し済み)。
- C3-17(実装自身が作った繰り越しを上限で消さない = 2 周目 blocking 2 + non-blocking 1)= このコミット。
  **(1) 繰り越しの上限**: `drawLotteryLocked` の当選者不在の経路が賞金全額を `Carryover` に入れていたので、`Carryover` が `MaxChips` に座った状態で売上があると `prize = MaxChips + 90` が**上限を超えたまま永続化**され、次の読み取りの `normalizeLotteryLocked` がそれを `MaxChips` へ切り詰めて 90 チップが消えた(入力は全部正常範囲 — 手編集ファイルの修復ではなく、**実装が作った値を実装の正規化点が消す**形)。親の決定どおり**入り切らない分はプールへ送る**(ハウス分と同じ行き先): `overflow, prize = prize-MaxChips, MaxChips` を切り出して `creditJackpotCappedLocked(economy, house+overflow)` に合流させ、`Carryover <= MaxChips` を**書き込み時点で**保証した。正規化点のクランプは残す — ただし役割が変わったので、`normalizeLotteryLocked` のコメントに**「上限側の切り詰めが直すのはファイルが持ち込んだ値だけ。ここに自分の書いた値が届いたらそれは writer のバグ」**と書いた。
  **(2) `JackpotAccum` の正規化**(2 周目の non-blocking): `seedJackpotLocked` は負値しか直していなかったので、手編集の `math.MaxInt64` が `JackpotAccum += bet*2` を**負へ折り返し**、以後の積立が何回かプールに 1 チップも入らない。`[0, jackpotAccumScale)` へ丸めるようにした(0 代入ではなく `%=` — 端数は壊れていないので残す)。併せて `accrueJackpotLocked` の順序を変え、**加算する前に**端数を切り出すようにした(`creditJackpotCappedLocked` が内部で `seedJackpotLocked` を呼ぶので、後から `%=` すると結果が入れ子の正規化に依存する)。
  **効き目の確認**: mutation 2 本で、それぞれ**新テスト 1 件だけ**が落ちる — `mutation-C3-17-no-carryover-split.txt`(`Carryover = 1000000000090` が永続化される)/ `mutation-C3-17-no-accum-clamp.txt`(プールが 5,000 のまま = 積立が消える)。既存テストの期待値は 1 つも変えていない。
  **設計書 §8** の契約文を直した: 通貨が消えるのは**プールの上限**だけ。口座・繰り越しの上限で溢れた分はプールへ送る。**切り詰めてよいのは壊れたファイルが持ち込んだ値だけ**。
  証拠: `.harness/runs/20260915-025434/verify-C3-17-1..3.txt`(build・vet 診断なし / casino ok / 全 8 パッケージ ok)。
- C3-16(正規化点を通らずに口座を触る経路を塞ぐ)= コミット `64f28e4`。`NEXT_FINDINGS.md` を見出しだけに戻した(反復 2 の節を削除)。
  **(1) `RefundStaleEscrows`**: 口座を `economy.Users` から直接引いて `moveFromEscrowLocked` に渡していたので、C3-14 で置いた正規化点を通らなかった。`moveFromEscrowLocked` は `Chips += Escrow` なので、手編集の `Chips = Escrow = math.MaxInt64` で**和が折り返して `Chips = -2` が保存される** — 次の読み取りがそれを 0 に丸め、「チップを絶対に消さない」が契約の唯一の操作が全部消す。ループを `for userID, account := range` にして、nil と `Escrow <= 0` を弾いたあと **(既に非 nil でも)** `ensureAccountLocked` を通してから足す。
  **(2) 掃いて見つけたもう 1 か所 = `topAssetsLocked`**(`store.go` 558 付近)。ここも `economy.Users` を直接回すので、手編集の `MaxInt64` 口座で `Chips + Escrow + Coins*rate` が折り返し、**その口座が首位ではなく最下位に並ぶ**(エラーも panic も出ない)。ここは `ensureAccountLocked` ではなく **`normalizeAccountLocked` を使った** — 既にある項目を丸めるのと口座を**作る**のは別で、作ると「順位を第三者が見た」だけで未プレイの人に 1,000 チップの初回ボーナスが湧く(nil を repair せず skip しているのと同じ理由)。
  **`grep -n "economy.Users" internal/casino/store.go` は 246/247/249(= `ensureAccountLocked` 自身)・564/565(順位)・1112(返金)の 3 か所に落ち着いた** — map を直接回すのは後ろの 2 つだけで、どちらも要素を正規化してから使う。
  **効き目の確認**: 正規化を外す mutation 2 本で、それぞれ**新テスト 1 件だけ**が落ちる — `mutation-C3-16-no-normalise.txt`(`persisted Chips -2`)/ `mutation-C3-16-rank-no-normalise.txt`(折り返した口座が 2 位に沈む)。既存の C-2 返金テスト 3 件は期待値を 1 つも変えていない(`MaxChips+500` のケースは両フィールドとも上限内なので正規化が素通りする)。
  **(3) 差し戻しの最後の 1 件**(`NEXT_FINDINGS.md` の所見 1、親が「C3-16 と一緒に」と判断済み): 未抽選ギルドの `LotteryStatus` を**購入を挟まずに**呼ぶテストを足した(`TestLotteryStatus_NeverDrawnGuildNamesThisMorningsDraw`)。C3-15 の表のケースは `BuyLotteryTickets` 経由なので、status が読む時点では購入自身のロールオーバーが `DrawDate` を**既にスタンプ済み** — 本当に未抽選のギルドには一度も聞いていなかった。当選者なしの `DrawDate` スタンプを暦日へ変える mutation で落ちることを確認(`mutation-C3-16-status-calendar-day.txt`: `NextDrawAt = 2026-09-15 09:00`、期待は 9/14 09:00)。
  検証出力: `.harness/runs/20260915-022052/verify-C3-16-{1,2,3}.txt`(build+vet exit=0 / casino ok / `go test ./...` 8 パッケージ ok、いずれも exit=0)、`verify-C3-16-4-refund-subtests.txt`(返金 4 件 PASS)、`pre-fix-C3-16-red.txt`(修正前の赤)。
- C3-15(未抽選の「次回抽選」テストを足し、旧挙動のコメントを消す)= コミット `95a6347`。`NEXT_FINDINGS.md` を見出しだけに戻した(C3-09 の差し戻し・所見 4・C3-12 の paths 違反の 3 節を削除)。
  **(1) 未抽選のケース**: `TestBuyLotteryTickets_ReportsTheDrawTheTicketsAreActuallyIn` の表に「the guild has never drawn」を足した。**既存 2 ケースは購入の中で抽選が走らない** — どちらも `DrawDate` が `lotteryDrawDate(08:59:59)` 以上で `drawLotteryLocked` が即 return する。`DrawDate == ""` だけが `"" < "2026-09-13"` でロールオーバーを実際に走らせる経路で、そこが表に無かった。売上も券も 0 なので `PickLotteryWinner` は `rng` を読まずに `""` を返し(`total <= 0` の早期 return)、rolls 空の `lotteryRand` のままで足りる。
  **効き目の確認**: 当選者なしの経路の `DrawDate` スタンプを抽選日(9/13)から暦日(9/14)へ変えると、**新ケースだけが** `NextDrawAt = 2026-09-15 09:00` で落ち、既存 2 ケースは通る(`mutation-C3-15-never-drawn.txt`)。9 時間のずれ(`lotteryDrawDate` の `Hour() < 9` 補正)を踏み抜く唯一のケースになっている。
  **表に `now` の列を足した**のは、追加ケースだけ別の日付の時計(2026-09-14 08:59:59)が要るため。既存 2 行は `now: justBeforeNine` を明示しただけで、`lastDrawDate` も `want` も不変。
  **(2) 旧挙動のコメント**: C3-07 で「上限で入り切らなかった賞金はプールへ」に変わったのに、3 か所が「the pot rolls forward」のままだった — `casino_announce.go` の `lotteryAnnounceCelebration` の doc(134 行)、`casino_announce_test.go` の 324・475 行。**`casino_announce.go` 85 行と `..._test.go` 287 行は直していない** — こちらは「購入者 0 の日」の話で、当選者がいない回の繰り越しは今も本当。
  検証出力: `.harness/runs/20260915-022052/verify-C3-15-{1,2,3}.txt`(build+vet exit=0 / casino 対象テスト ok / `go test ./...` 8 パッケージ ok、いずれも exit=0)、`verify-C3-15-4-subtests.txt`(3 サブテスト PASS)、`mutation-C3-15-never-drawn.txt`。
- C3-14(当選者がいない回のハウス分をプールへ送り、口座の空き計算のあふれを正規化で断つ)= コミット `6b2f696`。`NEXT_FINDINGS.md`「C3-07 の区切り評価」の所見 2・3 を閉じた(節から削除済み。残りは所見 4 = C3-15 のみ)。
  **(1) 当選者がいない回のハウス分**: `drawLotteryLocked` の `winner == ""` の経路は `lottery.Carryover = prize` だけを残していた。**通常の閑散日は売上 0 なのでハウス分も 0 で、この穴は見えない** — 券が 1 枚も無いのに売上が載っているポット(手編集、またはチップの引き落としと `Tickets` の間に落ちた部分書き込み)でだけ、繰り越しに乗らない `sales - floor(sales*90/100)` が消える。当選者がいた回とまったく同じ `creditJackpotCappedLocked(economy, house)` を通すようにした(この関数が内部で `seedJackpotLocked` を先に呼ぶので、順序の注意は C3-11 のときと同じく関数の中で閉じている)。
  **(2) `creditChipsCappedLocked` の `headroom`**: `MaxChips - account.Escrow - account.Chips` は `Chips = Escrow = math.MaxInt64` の手編集で**正の大きな値へ回り込む** — 入金が通り、口座が負に落ちる(mutation で `credited 1000` を再現済み)。C3-13 と同じ規律で、引き算ではなく**読み取り点**を直した: `normalizeAccountLocked(account *UserAccount)` を `store.go` に足し、`ensureAccountLocked` が返す直前に必ず通す。`Chips` / `Escrow` → `[0, MaxChips]`、`Coins` → `[0, MaxCoins]`。`headroom` の式も `creditChipsLocked` も**1 文字も変えていない** — 正規化後の演算数が 1e12 以下になることで安全になる。
  **正規化点は `ensureAccountLocked` 1 つ**(プールの `seedJackpotLocked`、宝くじの `normalizeLotteryLocked` と同じ位置づけ)。`topAssetsLocked` は `nil` 口座を**直さずに飛ばす**読み取り専用経路のままで、ここに正規化を足していない — 口座を開くと歓迎ボーナスが出るので、他人がランキングを読んだだけで通貨が生まれる(既存コメントの判断を維持)。
  **大きさの切り詰めは意図的な取引**: 上限を超えた手編集の残高は、最初にそれを読んだ書き込み経路で超過分を失う。`TestRefundStaleEscrows_HandEditedOverTheCapKeepsEveryChip`(`Chips = MaxChips`・`Escrow = 500` を丸ごと返す)は返金の**その回**を見ているので通るが、次にその口座を読むと `MaxChips` へ丸められる。範囲外の数はこのパッケージの不変条件の外側にあり、そのまま読むと編集していない口座(プール・ランキング)まで壊れる方を避けた。`types.go` の桁あふれ余裕の説明に口座の段落を足し、設計書 §3 にハウス分の無条件性を 2 行足した。
  検証出力: `.harness/runs/20260915-022052/verify-C3-14-{1,2,3}.txt`(build+vet exit=0 / casino ok exit=0 / `go test ./...` 8 パッケージ ok exit=0)。**mutation も取った**(`mutation-C3-14.txt`): `creditJackpotCappedLocked(economy, house)` と `normalizeAccountLocked(account)` の 2 行を消すと新テスト 3 本が落ち、保存則は `chips +0 + pool +0 + carryover +90 = 90, want 100`、入金は `credited 1000`(= 回り込んだ `headroom`)を再現する。
  テストは 3 本 — `TestLotteryDraw_NoWinnerConservesEveryChipOfTheSales`(`Δ全チップ + Δプール + Δ繰り越し = 消費した売上` を券なしの回で固定。`rolls` を空にした `lotteryRand` を差すので、券が無いのに抽選が rng へ触れば落ちる)、`TestEnsureAccount_NormalisesHandEditedBalances`(表 5 件。範囲内と上限ちょうどは**動かないこと**も固定する)、`TestCreditChipsCapped_HandEditedMaxInt64DoesNotWrapTheHeadroom`。範囲外の値は `seedAccounts`(map へ直接書く)で作る — `seedLotteryChips` は `ensureAccountLocked` を通るので、seed の時点で正規化されてテストにならない。
- C3-13(永続化された数値を読み取り点で正規化し、算術の桁あふれを一箇所で断つ)= コミット `b02581e`。C3-11 の差し戻し(`NEXT_FINDINGS.md` の反復 2)。**個別の加算にガードを足す直し方をやめた** — プールへの加算 → ハウス分 → 残額 → 繰り越し と 3 度「直した次の変数」で破綻していたのは、検査の場所が算術の側にあったから。`normalizeLotteryLocked(lottery *Lottery)` を `store.go` の宝くじ節に足し、日次ロールオーバー(`ensureTodayRateIndexLocked`)で **`seedJackpotLocked` と `drawLotteryLocked` の両方より前に**呼ぶ。`Sales` / `Carryover` → `[0, MaxChips]`、`Tickets` の各値 → `[1, LotteryMaxTicketsPerDraw]`(0 以下は `delete`、空になったら map ごと nil)。
  **順序が要件**: 後ろに置くと「既にあふれた値を丸める」ことになり何も直らない。これは mutation で確かめてある(正規化を `drawLotteryLocked` の後ろへ移すとテスト 2 件が落ちる)。読み取り経路は `BuyLotteryTickets` / `LotteryStatus` / `collectDailyAnnouncements` の 3 つで、いずれも `ensureTodayRateLocked` を通るので追加の呼び出しは不要 — `MarkAnnounced` だけは通らないが `Unannounced` しか触らないので正規化の対象外。
  正規化後の余裕は `types.go` のコメントに既存の `MaxChips`/`MaxCoins` 分析と同じ論法で書いた: `sales*90 = 9e13`、`prize = floor(Sales×90/100) + Carryover < 2e12` で `math.MaxInt64` の 4.6e6 分の 1。`LotteryPrize` の「桁あふれしない」というコメントは**正規化済み入力が前提**である旨へ直した(符号の修復はその場に残し、大きさの上限は二重に書かない — `MaxChips` と歩調を合わせる場所を 2 つにしないため)。設計書 §8 に規律そのものを書いた。
  検証出力: `.harness/runs/20260915-014154/verify-C3-13-{1,2,3}.txt`(build+vet exit=0 / casino の絞り込み ok exit=0 / `go test ./...` 8 パッケージ ok exit=0)。mutation は `mutation-C3-13.txt`(呼び出しを消す → 3 件落ちる / 抽選の後ろへ移す → 2 件落ちる)。評価者が「壊れた状態を合格させている」と指摘した既存テストは、当選者残高 900・`Carryover = MaxInt64-40`・`Sales = 100`・プール 5000 で **当選者が `MaxChips` に到達し、プールが 5000 → 6000(残額 990 + ハウス分 10)になる**ことを見るテストへ書き換えた。
- C3-12(未掲示の当選を 1 枠で上書きしない — 掲示待ちの回を並べて持つ)= コミット `2395498`。C3-08 の差し戻し(`NEXT_FINDINGS.md` の反復 2)。`Lottery.Unannounced []LotteryDraw`(`json:"unannounced,omitempty"`)を足し、`drawLotteryLocked` は当選者が出た回をここへ**追記**する。`LastDraw` は据え置き — **この 2 つは違う問いに答える**: `LastDraw` は `/lottery status` の「直近の結果は」で 1 枠が正しく、掲示は「まだ知らせていない回は」なので待ち行列が要る。1 枠しか無いと、9 時に落ちていて復旧後に前日分を精算し、その日の購入を挟んで翌朝もう 1 回抽選が走る経路で**先の当選者が上書きされて永久に掲示されない**(評価者の再現手順そのまま)。
  待ち行列から消えるのは `MarkAnnounced`(= 送信成功時だけ呼ばれる)で、消すのは **`Date <= date` の回だけ** — 送信中に発生した回は日付が後なので残り、次の巡回で掲示される。空になったら `nil` に戻す(`omitempty` が空配列をファイルに書かないように)。上限は `lotteryUnannouncedLimit = 7` で、超えたら**古い方から**落とす(賞金は抽選時に支払い済みなので落ちるのは掲示だけ。掲示チャンネルを消したギルドがファイルを無限に太らせない)。`collectDailyAnnouncements` は `Unannounced` を**そのまま**(日付で絞らずコピーして)job に載せる — ここで `LastAnnounced` と突き合わせ直すと「未掲示は 1 件で最終掲示日より後」という壊れた前提が戻ってくる。既存 JSON は `unannounced` キーが無いので nil = 空の待ち行列として読める(移行処理なし)。
  掲示側は `AnnouncementJob.LotteryDraw *LotteryDraw` → `LotteryDraws []LotteryDraw`。embed の 🎟️ フィールドは待ち行列の全件を**各回の日付付きで 1 行ずつ**並べ、最後に今開いている壺の行。祝いは**回ごとに 1 通**送る(当選者が別人なので、まとめると片方のメンションが落ちる)。1 通の送信失敗は従来どおりログのみで `continue` — 掲示は既に上がっているので job を失敗させても失った 1 通は戻らない。
  検証出力: `.harness/runs/20260915-014154/verify-C3-12-{1,2,3}.txt`(build+vet exit=0 / casino の絞り込み ok exit=0 / `go test ./...` 8 パッケージ ok exit=0)。**mutation も取った**(`mutation-C3-12.txt`): 追記を 1 枠の代入へ戻すと待ち行列・掲示・祝いのテスト 4 件が落ち、`MarkAnnounced` を全消しにすると残す側のテストが落ち、上限を 7 → 100 にすると 8 件溜まって上限のテストが落ちる。上限のテストは **7 を直書き**した(定数を読んで比べるテストは、そこに何を書いても通る)。
- C3-11(ジャックポットのプールに上限を敷き、桁あふれを構造的に不可能にする)= コミット `ca7a839`。C3-07 の差し戻し(`NEXT_FINDINGS.md` の所見 1 と反復 1 の判定。同じ 1 件)。`MaxJackpot`(= `MaxChips` = 1e12)を `types.go` に足し、**`seedJackpotLocked` を正規化点にした** — 従来の「`< JackpotSeed` なら引き上げ」「`JackpotAccum < 0` なら 0」に `> MaxJackpot` なら切り下げを加え、返った時点で `Jackpot` が必ず `[JackpotSeed, MaxJackpot]` に入るようにした。
  プールへの加算は `creditJackpotCappedLocked(economy, amount) int64` 1 か所に集約し、3 経路(スロットの積立 / 宝くじのハウス分 / 上限超過で払えなかった賞金)を全部これに通した。**あふれないのは値が小さいからではなく、余裕 `MaxJackpot - Jackpot` を先に求めて加算前に切り詰めるから** — 正規化点を通った `Jackpot` に対して余裕は `[0, 1e12]` なので、addend が `math.MaxInt64` でも上流で既に wrap していても、出てくるプールは範囲内。`amount <= 0` は 0 を返す(加算経路の addend は `house + (prize - paid)` という**差**で、手編集の `Carryover` で負になりうる。負のまま足すとプールが減る)。
  **保存則の言い方を変えた**: 「通貨は消えない」ではなく「**プールの上限で入り切らない分だけが消え、それ以外の経路では消えない**」。口座側の `creditChipsCappedLocked` が `MaxChips` で超過分を捨てるのと同じ契約で、事故ではなく仕様。`store_test.go` の保存則テストのコメント・設計書 §8・`blocked/C3-06.md` (a)(b)(c) を同じ言い方に揃えた。**上限が削るのはハウスの取り分だけで、当選者の払い出しは満額のまま**(回帰テストが 1150 チップで固定している)。
  検証出力: `.harness/runs/20260915-014154/verify-C3-11-{1,2,3}.txt`(build+vet exit=0 / casino の絞り込み ok exit=0 / `go test ./...` 8 パッケージ ok exit=0)。**mutation も取った**: `mutation-C3-11-1.txt` — 上限の切り下げと余裕の切り詰めを外すと新テスト 6 件が落ち、評価者が報告したのと同じ `pool = -9223372036854775789` が再現する(トートロジーでないことの証拠)。
  `lottery.go` / `lottery_test.go` / `slot.go` は `paths:` にあったが変更不要だった — 積立も抽選も加算の実体は `store.go` にあり、上限は加算点だけで閉じる。
- C3-11(設計書の掲示文言を「昨日の当選」から実際の抽選日へ合わせる)= コミット `edde435`。C3-08 で 9 時掲示の見出しが `LastDraw.Date` 由来の「M/D の当選」(当選者なしは「前回の当選」)に変わったのに、設計書 §3 の掲示仕様(61)と実測(69)だけが「昨日の当選」のままだった。**掲示するのは「今日精算した回」ではなく「まだ掲示していない回」**なので、Bot が 9 時に落ちていた日の当選は翌朝の掲示に**その回の日付のまま**出る — 「昨日」と書くと数字がどの回のものか嘘になる。実測の文字列は `casino_announce_test.go:399` が実際に固定している `🏆 7/10 の当選: ...` へ合わせた。文書のみで、コードとテストは触っていない。
  検証出力: `.harness/runs/20260915-014154/verify-C3-11b-1.txt`(`go build ./... && go vet ./...` exit=0)。開始儀式の `go test ./... -count=1` は 8 パッケージ ok。**元は `verify-C3-11-1.txt`** — 同名タスクの片方が C3-11b へ改名されたので、プールの上限の証拠に上書きされる前に複製して指し直した。
- C3-10(💎💎💎 の説明を「払い出しは無いが積立は行う」に直す)= コミット `e35fe8b`。`README.md` 67 と設計書 §2 の実測(36)・§6 のテスト方針(86)が「💎💎💎 はプールを 1 チップも動かさない」と書いていたが、**プールへの積立は当たり外れに関係なく毎スピン走る**(`store.go:768` の `accrueJackpotLocked` は `spin()` より前、7️⃣7️⃣7️⃣ の判定より前)。プールから払い出す(= 種へ戻す)のは 7️⃣7️⃣7️⃣ だけで、💎💎💎 は積立ぶんだけプールを増やす。文書のみの修正で、コードとテストは触っていない。
  実測文は条件つきの数字へ直した — 端数 0・ベット 100 の 💎💎💎 なら `JackpotAccum += 100×2 = 200` → 2 チップが入り、プール 5,000 は **5,002**。同じ誤りが §6 のテスト方針の一覧にもあったので揃えた(`paths:` 内)。
  検証出力: `.harness/runs/20260914-222248/verify-C3-10-{1,2}.txt`(build+vet exit=0 / `go test ./...` 8 パッケージ ok exit=0)。文書のみの変更なので mutation は取っていない。
- C3-09(「次回抽選」を確定済みの抽選日から出す)= コミット `7e7f48f`。`BuyLotteryTickets` / `LotteryStatus` が返す `NextDrawAt` は `nextRunAt(now)` そのままで、**要求が拾った `now` と、ロックが空くまでに進んだ状態の二つの時計がずれると過去の 09:00 を指した** — 08:59:59 に読まれた購入が 9 時掲示スケジューラの抽選の後に処理されると、`DrawDate` は既に当日で `drawLotteryLocked` は正しく再抽選を拒む(券は翌日分に入る)のに、画面は済んだ 09:00 を「次回」と出していた。
  `internal/casino/lottery.go` に `nextLotteryDrawAt(lastDrawDate string, now time.Time) time.Time` を足し、`nextRunAt(now)` と「`lastDrawDate` の翌日 09:00 JST」の**遅い方**を返す。**`nextRunAt` のシグネチャは変えていない**(9 時掲示スケジューラが同じ関数を使っている)。渡すのは**ロールオーバー後の** `lottery.DrawDate` — `ensureTodayRateLocked` の前に読むと当日分の抽選が反映されず、直そうとしたずれがそのまま残る。
  `DrawDate == ""`(未抽選)と**パースできない文字列**(手編集)は `nextRunAt(now)` へ落とす。翌日の算出は `time.Date(y, m, day+1, 9, ...)` で、月末は `time.Date` の正規化に任せる(9/30 → 10/1)。
  テストは 2 本 — `lottery_test.go` の `TestNextLotteryDrawAt_NeverAnswersADrawThatHasAlreadyRun`(表駆動 6 件: 当日抽選済み / 通常 / 未抽選 / 何日も古い `DrawDate` が答えを過去へ引き戻さない / 月末 / 壊れた文字列)と、`store_test.go` の `TestBuyLotteryTickets_ReportsTheDrawTheTicketsAreActuallyIn`(購入と status の**両方**が同じ答えを返すことを固定。片方だけ直すと画面が自己矛盾する)。後者は `rolls` を空にした `lotteryRand` を差すので、想定外の抽選が走ればその場で落ちる。
  検証出力: `.harness/runs/20260914-222248/verify-C3-09-{1,2,3}.txt`(build+vet exit=0 / 絞り込み `-v` で新テスト両方 PASS・exit=0 / `go test ./...` 8 パッケージ ok exit=0)、`mutation-C3-09.txt`(両方の呼び出しを `nextRunAt(now)` へ戻すと `NextDrawAt = 07-11 09:00, want 07-12 09:00` で落ちる)。
- C3-07(上限で受け取れなかった賞金をプールへ)= 次のコミット。`drawLotteryLocked` は当選者が `MaxChips` で賞金を受け取り切れないとき残額を `lottery.Carryover` へ入れていた — これは**その人の賞金を次回の別の当選者へ渡す**経路だった。親の決定どおり `economy.Jackpot += house + (prize - paid)` に変え、当選者がいた回の `Carryover` は必ず 0 にした。`Carryover` を使うのは `winner == ""` の回だけになった(型注釈どおり)。`LastDraw.Prize` は実際に払った `paid` のまま(C3-06 で祝いが `Prize == 0` でも出るようにしてあるので、上限に張り付いた当選者も名前は出る)。`seedJackpotLocked` の後で足す順序は据え置き(C3-01 の落とし穴)。
  **`prize - paid` が負にならないことが前提** — `creditChipsCappedLocked` は `headroom` が `amount` より小さいときだけ切り詰め、`amount` を超えて払う経路が無いので `paid <= prize`。ここが崩れるとプールが減る。
  テストは `TestLotteryDraw_WinnerAtTheChipCapCarriesTheRemainderForward` を `..._SendsTheRemainderToTheJackpot` に書き換えた。固定したのは 4 点 — 入金は入る分だけ(`u1` は `MaxChips` のまま・`LastDraw.Prize` は 50)、プールは 50 増える(ハウス 10 + 残額 40)、`Carryover` は 0、翌日 `u2` だけが 1 枚買った回の当選金は 45(= `floor(50×0.9)`。繰り越しが生きていれば 85 になる)。保存則(チップの増加 + プールの増加 == 売上 + 前回繰り越し)も同じテストで検査する。既存の `totalChips` ヘルパ(`store_test.go` 2655)を使う — **同名のヘルパを書き足すと `redeclared` でコンパイルが落ちる**。
  検証出力: `.harness/runs/20260914-222248/verify-C3-07-{1,2,3}.txt`(build+vet exit=0 / casino の絞り込み ok exit=0 / `go test ./...` 8 パッケージ ok exit=0)、`verify-C3-07-4.txt`(新テスト単体 `-v` で PASS)、`mutation-C3-07.txt`(`Carryover = prize - paid` に戻すと `Carryover = 40, want 0` で落ちる)。
  `blocked/C3-06.md` の保存則 6(b) の記述も新しい挙動へ書き直した(**正本へ写すのはユーザー**。C2-08.md・C3-06.md と同じ扱い)。設計書 §3 に 1 行追記。
- C3-06(README・設計書の実測・architecture の追記案)= 7cba582。README は公開物なので `/slot` の 3 行(積立 2 %・7️⃣7️⃣7️⃣ でプール全額と種 1,000 へのリセット・表示場所)と `/lottery buy|status` の各 3 行を**規則だけ**書いた(過程・判断の経緯は書かない)。`/help` はレジストリから自動生成なので変更不要(C3-04 で確認済み)。設計書 §2・§3 の「実測」は**通っているテストの値だけ**から書いた — 積立は 10 チップ×5 スピンで 1,001・端数 0(`TestStore_Spin_AccruesTheSmallestBetWithoutLosingTheRemainder`)、発火はプール 37,500 + 自分の積立 2 = 37,502(`TestStore_Spin_TripleSevenPaysTheWholePoolAndResetsToTheSeed`)、賞金は 500→450/50・1,234→1,110/124(`TestLotteryPrize_SplitsSalesAndAddsTheCarryover`)。`Docs/agent-guide/architecture.md` は展開コピーなので触らず、`blocked/C3-06.md` に正本へ写す 3 節(レイヤー表の「宝くじ」行、日次ロールオーバーの 3 つ = レート生成・種・抽選、危険地帯 6 点目 = プールと賞金の保存則)を書いた。**これはユーザー(人)が MyWorkflow の正本へ写して再展開する作業**(C2-08.md と同じ)。`cmd/bot/main.go` は §7 どおり変更していない。検証出力: `.harness/runs/20260914-204154/verify-C3-06-{1,2,3}.txt`(build+vet exit=0 / `go test ./...` 8 パッケージ ok exit=0 / `go build -o bin/todayistodaybot ./cmd/bot` exit=0)。
  **README は行末が混在している**(231 行中 217 行が CRLF、残り 14 行が LF)。テキストモードで読み書きすると全行 CRLF に揃ってしまい `git diff --numstat` が 226/217 の全面書き換えになる — バイナリで読み、挿入行に `
` を付けて書き戻した(`--numstat` と `--ignore-cr-at-eol --numstat` が 9/0 で一致)。
- 反復 5 の評価者指摘(NEEDS_WORK 1 件)= 5e56d17。**C3-05 の「賞金 0 は祝わない」を撤回した** — `lotteryAnnounceCelebration` の除外条件は `draw == nil || WinnerID == ""` の 2 つだけにし、`Prize == 0` の当選者にもメンション付きの祝いを送る。done-when は「`LotteryDraw` に当選者がいれば別メッセージで祝う」で、賞金による除外はそこに無い。`MaxChips` に張り付いた当選者は実際に `Prize 0` で記録される(`creditChipsCappedLocked`)ので、抑止すると**本物の当選者へのメンションが落ちる**。金額は記録どおり 0 と出す(`/lottery status` と掲示フィールドに揃う)。検証は fake sender 経由の `TestStartAnnounceScheduler_CelebratesAWinnerWhoWasCreditedNothing`(実ストアで 2 枚買わせ、`Update` で残高を `MaxChips` にしてから 1 パス回す)。検証出力: `.harness/runs/20260914-204154/verify-C3-06-finding-{1,2}.txt`(build+vet exit=0 / `go test ./...` 8 パッケージ ok exit=0)、`mutation-C3-06-finding.txt`(`Prize <= 0` を戻すと新旧 2 件が落ちる)。
- C3-05(9 時掲示の 🎟️ フィールドと当選者の祝い)= 16dabfb。`AnnouncementJob` は C3-03/C3-04 で既に `LotteryDraw` / `LotteryPrize` / `LotteryTickets` を運んでいたので、`internal/casino` 側は 1 行も触っていない — 変更は `internal/commands/casino_announce.go` だけ。**祝いのために `startAnnounceScheduler` の引数を 1 つ増やした**(`sendText func(channelID, content string) error`)。埋め込みと同じ 1 つの sender にまとめなかったのは失敗時の扱いが逆だから: 埋め込みの失敗は**エラーを返す**(`LastAnnounced` が立たず翌朝の掃き出しが掲示ごとやり直す)が、祝いの失敗は**ログだけで nil を返す**(抽選はもうストアに確定し、掲示も上がっている。ここで job を失敗させても失った 1 通は戻らず、翌朝に掲示が二重に出るだけ。結果は `/lottery status` で見える)。**祝いは埋め込みの成功後にだけ送る** — 先に送ると、埋め込みが落ち続ける間リトライのたびに当選者へメンションが飛ぶ。
  `lotteryAnnounceCelebration` は **`Prize <= 0` を「祝わない」に倒した**。`drawLotteryLocked` は `creditChipsCappedLocked` 経由で払うので、当選者が `MaxChips` に張り付いていると `LastDraw.Prize` が 0 のまま記録され、壺は丸ごと繰り越される —「0 チップ 獲得!!」は祝いではない。埋め込みのフィールド側は記録どおり出す(`/lottery status` と食い違わせない)。
  フィールドの 2 行目「本日の賞金 / 売れた枚数」は**通常の 09:00 では必ず 0/0**(抽選が壺を空けた直後)。0 以外になるのは遅れて出る追いつき掲示(`announce.go` の「上限を切らない」)のときだけで、そのケースもテストにした。
  検証出力: `.harness/runs/20260914-204154/verify-C3-05-{1,2,3,4}.txt`(build+vet exit=0 / 指定パターンで 18 PASS・FAIL 0 / 祝いの単体 5 PASS / 全体 ok、いずれも exit=0)、`mutation-C3-05-no-celebration.txt`(祝いの送信を潰すと 2 件が落ちる)。
  **C3-04 の教訓が再発した**: `TASKS.md` の `verify:` にある `-run "TestAnnounce|TestBuildAnnouncement|TestStartAnnounce"` は **`TestLotteryAnnounceCelebration_*` を 1 件も拾わない**。証拠は別に `verify-C3-05-3.txt` を取った。以後このファイルへテストを足すときは名前を `TestBuildAnnouncement…` / `TestStartAnnounce…` で始めるか、`verify:` のパターンを直す。
- C3-04(`/lottery buy|status`)= 069c691。`/help` はレジストリから自動生成なので `help.go` は変更不要 — `/lottery` は `init()` の `Register` だけで一覧に載る(逆に `Register` が消えると一覧から静かに落ちるので、`help_test.go` の一覧検査へ `lottery` を足し、テスト名を `ListsTheButtonGames` → `ListsTheCasinoGames` に直した)。時刻表示は `internal/commands` に JST が無かったので `jstZone`(`time.FixedZone`)をこのファイルに置いた — `time.Local` はホストの地域に依存し、`time.LoadLocation` は最小構成のコンテナに tzdata が無いと失敗する。`lotteryPotLines` は購入確認と `status` の**両方**が呼ぶ 1 つの関数にした(同じ壺について 2 つの文面が食い違えない)。購入確認は `Count`(今回買った枚数)と `UserTickets`(抽選での持ち枚数)を別々に出す — 混同すると当たる確率を読み違える。前回結果の当選者だけ `<@id>` で出す(設計書 §3 の掲示と同じ流儀)。`count` の範囲検査は Discord の `MinValue`/`MaxValue` に加えてコマンド側にも置いた(`/slot` のベット範囲と同じ多重防御 — API を直接叩かれると client 側の制約は効かない)。検証出力: `.harness/runs/20260914-204154/verify-C3-04-{1,2,3,4}.txt`(build+vet exit=0 / `TestLottery|TestHelp` 7 PASS・FAIL 0 / commands 全体 ok / 実際に help を拾う広い pattern で 25 PASS・FAIL 0、いずれも exit=0)、`mutation-C3-04-unregistered.txt`(`Register` を外すと登録検査と `/help` の一覧検査が落ちる)。
  **`TASKS.md` の `verify:` にある `-run "TestLottery|TestHelp"` の `TestHelp` は 1 件も拾わない** — help のテストは `TestFormatHelpText_*` という名前。証拠は別に `verify-C3-04-4.txt` を取った。以後 help の検証を書くときは `TestFormatHelpText` を使う。
- 反復 3 の評価者指摘(NEEDS_WORK 3 件)= 51d6417。**抽選時刻**: 抽選日は `jstDate(now)`(0 時に変わる)ではなく `lotteryDrawDate(now)`(直近の 9:00 JST。9 時前は前日)で決める — 日付だけで判定すると壺が 9 時間早く精算され、12:00 に買った券が翌 0:00 に引かれ、8:00 に買った券は精算済みの壺に入って当日 9 時の抽選を素通りする。レートは 0 時・抽選は 9 時と**境界が違う**ので、`ensureTodayRateIndexLocked` の引数を `today string` から `now time.Time` に変えて 2 つの日付を中で作る(呼び出し側 10 か所は `now` をそのまま渡す)。**購入拒否での引き直し**: 拒否は `refused` に入れてクロージャからは `nil` を返す — クロージャからエラーを返すと `Update` が書き込みを捨てて確定した抽選ごと巻き戻るのに、`rng` は `Data` ではなく `Store` にあるので位置だけ進む。残高 0 の利用者が当たるまで再実行できてしまう(拒否の検査は最初の変更より前なので、確定するのはロールオーバーだけ)。**残高上限の当選者**: `creditChipsCappedLocked` のまま(`MaxChips` はこのパッケージの硬い不変条件で、ロールオーバーには拒否する相手がいない)。入り切らない分は次回の `Carryover` になり保存則は保たれる — 境界テストで挙動を固定した。設計書 §3 への明文化は C3-06(設計書が `paths:` にある)へ。検証出力: `recheck-C3-03fix-{1,2,3}.txt`(build+vet exit=0 / 114 PASS・FAIL 0 / `go test ./...` 8 パッケージ ok)、`mutation-C3-03fix-midnight-draw.txt`・`mutation-C3-03fix-refusal-aborts.txt`(どちらも元に戻すと新テストが落ちる)。
- C3-03(宝くじ: 永続化・購入・9 時抽選)= a0e1776。`GuildEconomy.Lottery` は**値型**(`json:"lottery"`)— ポインタにすると毎回の日次ロールオーバーで nil 修復が要るうえ、C-3a 以前の `data/casino.json` が `DrawDate:"" / Tickets:nil / Sales:0 / Carryover:0`(= 未抽選の空の壺)へそのまま unmarshal するので移行処理が 1 行も要らない。抽選は `drawLotteryLocked` を **`ensureTodayRateIndexLocked` の中**(`seedJackpotLocked` の直後)に置いた — 全コマンドと掲示掃引が必ず通る唯一の読み取り点なので、9 時に Bot が落ちていても・掲示チャンネル未設定でも取りこぼさない(設計書 §3)。判定は `DrawDate < today`(`!=` ではない — 時計が巻き戻ったときに払い済みの日を二度引かせない)。ハウス分は `seedJackpotLocked` の**後**に `Jackpot` へ足す(種を入れる前に足すと 0 → 1,000 の底上げに食われる)。`LotteryPrize` はハウス分を**引き算の余り**で出す(`sales - floor(sales*90/100)`)ので、端数は必ずハウス側に落ちて `prize + house == sales + carryover` が常に成り立つ。`PickLotteryWinner` は **userID 昇順**で累積する — Go はマップの反復順を毎回変えるので、ソートしないと同じ乱数から違う当選者が出て再現できない。当選者への加算だけは `creditChipsLocked` ではなく `creditChipsCappedLocked`(入る分だけ入れ、残りは `Carryover` へ)— ロールオーバーは失敗できない経路なので、拒否すると賞金を壊すか上限に座った当選者でロールオーバーが永久に詰まる。`AnnouncementJob.LotteryDraw` は **`Today` と同じ日付の `LastDraw` だけ**運ぶ(掲示の文言が「昨日の当選」なので、静かな日が続くと古い当選者を毎朝出してしまう)。検証出力: `.harness/runs/20260914-204154/verify-C3-03-{1,2,3}.txt`(build+vet exit=0 / 指定パターン 83 PASS・FAIL 0 / casino 全体 ok、いずれも exit=0)、`mutation-C3-03-no-rollover-draw.txt`(ロールオーバーの抽選呼び出しを外すと新テスト 6 件が落ちる)、`mutation-C3-03-unsorted-winner.txt`(`sort.Strings` を外すと重み付き抽選のテストが落ちる)。
- C3-02(ジャックポットの表示: `/slot` の結果・7️⃣7️⃣7️⃣ の祝い・9 時掲示)= 0cc0742。結果メッセージは常に `slotJackpotLine` の 1 行を持ち、発火時だけ「🎰 JACKPOT!! +N チップ」、それ以外は「🎰 ジャックポット: N チップ」。発火の判定は**リールではなく `JackpotWon > 0`** で、発火した回にプール残高(= 種へ戻った 1,000)を出さない — 出すと種が賞金の一部に読める。祝いは `slotCelebrationMessage` に切り出した(💎💎💎 も同じ関数を通るが、プールを取っていないので額は出さない)。掲示は `AnnouncementJob.JackpotPool` を運ぶ: `collectDailyAnnouncements` が `ensureTodayRateLocked`(= `seedJackpotLocked` を含む)の**後**に `economy.Jackpot` を読むので、一度も回していないギルドでも 0 ではなく種が出る。embed のフィールドは 2 枚目(ランキングの次)。既存の文言固定テスト 4 件は期待値を新しい 3 行目ごと更新した(表示の変更が意図どおりであることの記録 — 緩めてはいない)。検証出力: `.harness/runs/20260914-204154/verify-C3-02-{1,2,3,4}.txt`(build+vet exit=0 / commands の `TestSlot|TestAnnounce|TestBuildAnnouncement` 17 PASS・FAIL 0 / casino の `TestAnnounce|TestCollect` 13 PASS・FAIL 0 / `go test ./...` 8 パッケージ ok、いずれも exit=0)。
- C3-01(ジャックポットのプール: 積立・7️⃣ 3 つで全額・種)= 105da34。`GuildEconomy` に `Jackpot` / `JackpotAccum`(どちらも `omitempty` 無し — 0 は「未播種」という意味を持つ唯一の値で、C-3a 以前の `data/casino.json` がまさにそれに unmarshal する)。積立は 1/100 チップ単位の端数(`JackpotAccum`)で持つ: 最小ベット 10 枚の 2 % は 0.2 枚なので、整数チップだけで積むと毎回 0 に切り捨てられてプールが小額プレイから一切育たない。`Spin` の `Update` は 種 → 積立 → 抽選 → 発火 → 配当 の順で、7️⃣ 3 つは**自分の積立を含んだ**プールを取る(設計書 §2 の並び)。種は `Spin` と `ensureTodayRateIndexLocked` の両方で入れる — 後者は掲示・`/balance`・`/rank` が通る唯一の読み取り点なので、まだ誰も回していないギルドに 0 を見せないため。検証出力: `.harness/runs/20260914-204154/verify-C3-01-{1,2,3}.txt`(build+vet exit=0 / `TestSpin|TestStore|TestJackpot|TestPayout` ok / casino 全体 ok、いずれも exit=0)、`mutation-C3-01-no-accrual.txt`(積立の 1 行を外すと新しい 5 テストが全部落ちる)、`mutation-C3-01-reset-to-zero.txt`(発火後のリセットを種ではなく 0 にすると 7️⃣ のテストが落ちる)。
- C2-14(精算失敗の契約を設計書へ明記する)= bc95d1c。設計書 §9 に「精算が拒まれたとき(手動の再試行が唯一の出口)」節を足した — 決着した盤面は `WithSession(done=true)` でセッションから外れ、`Sweep` が回るのは期限切れの**生きた**盤面だけなので掃除人は二度と見ない。したがって進めるものは押した人自身か、次回起動の `RefundStaleEscrows`(配当ではなく預かりの返金)しかない。C2-12 の done-when が求めた「⚠️ 精算に失敗しました。次回の自動処理で精算されます」は**採らない**判断を正本に残した(待てば精算される自動処理は存在しない)。テストは `Data.Content` の値一致だけでは文言の嘘を止められない(定数を書き換えれば追従してしまう)ので、`assertNoAutomaticSettlementPromise` を足して「自動/次回/お待ち」を約束しないことと「もう一度」を求めることを H&L・BJ の両方の既存テストから検査する。評価者の 2 点目(`casinoPayoutLine` を `casino_sessions.go` へ移す)は**採らなかった** — `highlow.go` / `blackjack.go` / `casino_sessions.go` の 3 か所から呼ばれる共有ヘルパなので `casino_shared.go` が正しい置き場。C2-12 が `paths:` の外へ 1 関数はみ出した事実だけ記録に残す。`NEXT_FINDINGS.md` の反復 3 の節はこのタスクの完了をもって消した(節自身がそう指示していた)。検証出力: `.harness/runs/20260914-161722/verify-C2-14-{1,2,4-all}.txt`(build+vet exit=0 / `TestHighLow|TestBlackjack|TestSweepIdleBoards` 84 PASS・FAIL 0 / `go test ./...` 8 パッケージ ok)、`verify-C2-14-3-mutation.txt`(定数を「次回の自動処理で精算されます」に変えると H&L・BJ の両テストが落ちる)。
- C2-13(README の総資産式に預かりを含め、小額ベットの RTP を文書化する)= 2301e99。README の `/rank` は総資産を `コイン×当日レート＋チップ＋預かり中のチップ` と書く(`totalAssetsLocked` と同じ順・同じ項)。`highlow_test.go` に `TestHighLow_SmallBetReturnIsCutByRounding` を足した — 統計テスト(100 万手・0.9479)と違い**期待値テスト**で、初手 13 ランク × 有効な方向を等確率、各ケースは残り 51 枚すべてを実際に `Guess` させて期待配当を出す。ベット 11 の期待 RTP は **0.9346**(0.93〜0.95 に固定)。差は倍率 `95*remaining/winning` とポット `pot*m/100` の二度の切り捨てで、どちらもハウス側に落ちる(例: 初手 2 でハイ = 倍率 100 → 11 枚が 11 枚のまま)。設計書 §6 に「RTP 95 % はポットが大きいときの値。ベット 10 台では丸めで 93〜94 %」を 1 行。検証出力: `.harness/runs/20260914-161722/verify-C2-13-{1,2,4,5-final}.txt`(build+vet exit=0 / `TestHighLow` 16 PASS・FAIL 0 / `go test ./...` 8 パッケージ ok)、`verify-C2-13-3-mutation.txt`(倍率とポットを切り上げに変えると期待 RTP が 0.9840 になり新テストが落ちる)。
- C2-12(時間切れの決着に勝敗・手札を出し、精算前の金額を出さない)= 8b0597c。掃除人の編集は盤面 embed を「金額だけ」に潰さず、`custom_id` の prefix で引いたゲーム自身に結果を描かせる(`timedOutBoardResultRenderer.TimedOutEmbed(state, settled)`。`DisabledComponents` と同じ任意実装で、答えないゲームは従来の `casinoTimeoutEmbed` に落ちる)。BJ は `blackjackResultLines`(伏せ札を開いた両者の手・最終点・勝敗)、H&L は最終カード+`highLowResultLines`(AutoResolve は CashOut なので 💰 キャッシュアウトの見出し)、その上に `casinoTimeoutNotice` の理由行、タイトルは `casinoTimeoutTitle` のまま。掃除人の編集は精算が成功した回にだけ走るので、C2-10 の再試行で後から成功した回も同じ描画になる。H&L の精算失敗は `highLowPendingEmbed`(BJ の `blackjackPendingEmbed` と同じく金額行なし)へ — `highLowResultEmbed(pending.Result)` は Payout/Balance が未設定なので「配当: 0枚 / 残高: 0枚」と嘘をついていた。金額行は `casinoPayoutLine` 1 か所に寄せた(「書かない」を 0 の整形ではなく呼ばない判断にするため)。検証出力: `.harness/runs/20260914-161722/verify-C2-12-{1,2,3,4}.txt`(build+vet exit=0 / `TestRunSessionSweeper|TestHighLow|TestBlackjack` 68 PASS・FAIL 0 / `TestSweepIdleBoards` 17 PASS / `go test ./...` 8 パッケージ ok)、`verify-C2-12-3-mutation.txt`(結果描画への委譲と `highLowPendingEmbed` を潰すと新しい 5 テストが全部落ちる)。
- C2-11(編集に失敗した盤面で古い表示のまま判定しない)= 2aa31dc。`Session.NeedsRedraw` を足した: 手を進めたあとの `InteractionResponseUpdateMessage` が失敗したら `SessionManager.SetNeedsRedraw(id, true)` で立てる。次の押下は**手を進めず**、現在の盤面をそのまま描き直して(`redrawStaleBoard` / `redrawStaleHand`)、成功したときだけフラグを下ろし、押した人へ ephemeral の followup で「🔄 盤面を更新しました。もう一度選んでください」を返す。再描画も失敗したらフラグは立ったまま(次の押下がまた直す)。BJ はこの判定を `stakeDouble` の**前**に置く — 見えていない手に 2 枚目の掛け金を払わせない。検証出力: `.harness/runs/20260914-161722/verify-C2-11-{1,2,3}.txt`(build+vet exit=0 / `TestHighLow|TestBlackjack` 53 PASS・FAIL 0 / `go test ./...` 8 パッケージ ok)、`verify-C2-11-4-mutation.txt`(判定を `if false &&` で潰すと新しい 4 テストが全部落ちる)。
- C2-10(掃除人の精算失敗で配当を失わない)= 3b5c485。`SessionManager.Sweep` は期限切れの盤面を map から**消さず** `Session.Expired` を立てて返し、以後 `Get`/`Hold`/`WithSession`/`Touch`/`Close` がその盤面を拒む(押下は「⌛ この盤面はもう終了しています」、`Open` は `ErrGameInProgress` — escrow が残っている以上 `Store.OpenGame` も断るので両者の答えを揃えた)。掃除人は `AutoResolve` の結果を `PendingPayout`/`PayoutResolved` として盤面に置き、`SettleGame` が成功したときだけ新しい `Remove(id)` で消す。失敗したら盤面ごと残るので、次の掃除(30 秒後)が**同じ配当**を払い直し、成功した回にメッセージ編集もする。`ErrNoGameInProgress`(= 既に escrow が閉じている)と自己決着できない state の 2 つだけは再試行せず `Remove` する — 前者は払うものが無く、後者は誰にも決められないので、残せば持ち主が新規ゲームを始められなくなるだけ。検証出力: `.harness/runs/20260914-161722/verify-C2-10-{1,2,3,4,5}.txt`(build+vet exit=0 / casino `TestSession` 16 PASS / commands の掃除人・H&L・BJ ok / `go test ./...` 8 パッケージ ok)、`mutation-sweep-removes.txt`(`Sweep` を元どおり即 `removeLocked` に戻すと新しい 4 テストが全部落ちる)。
- C2-09(README のコマンド一覧に C-1 のカジノ 7 コマンドを足す)= 454a182。「カジノ（コイン・チップの経済）」節を新設し、`/balance` `/daily` `/rate` `/exchange` `/slot` `/rank` `/casino-admin` を各 `Definition()` の Description とオプションどおりに載せた(`/exchange` と `/casino-admin` はサブコマンドごとに 1 行)。既存の「カジノ（ボタン操作のゲーム）」節はそのまま。検証出力: `.harness/runs/20260914-154156/verify-C2-09-{2,3}.txt`(build+vet exit=0 / `go test ./...` 8 パッケージ ok)。
- 反復 1 の評価者(codex)の指摘 3 件 = 7082177 / a9f6460 / 103021b。(1) 起動時の返金を `session.Open()` より前へ動かした — `startupSteps{refund, open, register}` に切り出して順序をテストできるようにし、返金が失敗したら受付を始めずに `os.Exit(1)`。(2) `SessionManager.Hold`/`Release` を足し、`Sweep` は押下中(`busy > 0`)の盤面を飛ばす — ダブルの `AddToEscrow` はロックの外なので、その隙間で 200 枚の預かりを 100 枚のゲームとして自動決着していた。(3) 時間切れの編集はボタンを消さず、`custom_id` の prefix で引いたゲーム自身(`DisabledComponents`)に無効化した行を描かせる(設計書 §4・§8)。検証出力: `.harness/runs/20260914-154156/verify-C2-09-1.txt`(build+vet+test exit=0、8 パッケージ ok)、`mutation-sweep-busy.txt`(busy 判定を外すと新しい回帰テストが落ちる)。
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

- C2-05(セッション管理)= 未コミット→本反復。`session.go`: `Session{ID, GuildID, UserID, Game, State any, LastActionAt, Ref MessageRef}` と `SessionManager`(id→session と guild+user→id の 2 マップを 1 つの `sync.Mutex` で守る)。`Open` / `Get`(コピーを返す) / `WithSession`(ロック内で盤面に適用、`done` で両マップから削除) / `Sweep(now)`(期限切れを外して返す) / `Touch` / `Close` / `Len`、シングルトン `DefaultSessions()`(TTL 3 分・`time.Now`)。セッション ID は `crypto/rand` 16 バイトの hex(32 文字)。検証出力: `.harness/runs/20260914-121315/verify-C2-05-{1,2,4}.txt`(build+vet exit=0 / `TestSession` 10 件 PASS・exit=0 / `go test ./...` 8 パッケージ ok・gofmt 差分なし)と `verify-C2-05-3-mutation.txt`(`WithSession` からロックを外すと並行テストが 5/5 で落ちる = 更新の取りこぼしを実際に検出する)。

- 反復 4 の評価者指摘(C2-04)はコード変更なしで解消。機能条件への違反は「見つかりませんでした」で、残る 1 件は**評価依頼の比較基点**の問題(`8408e2f..HEAD` を渡したため C2-03 修正コミット `8446bbc` と進捗文書が混ざった)。正しい基点は `375c178^..375c178`。以後、評価依頼は**そのタスクのコミット 1 つの差分**を基点にし、`PROGRESS.md` / `TASKS.md` / `.harness/` は範囲検査の例外として渡す。

- 反復 5 の評価者指摘(`WithSession` が `done=true` とエラーの同時返しでセッションを残す)= 064b1e7。削除は `done` だけで決める — 終了した盤面が map に残ると二度目の押下が同じ手を決済でき、`Sweep` の対象にも残る。エラーは削除後にそのまま返す。テストは契約を 2 件に分けた(`false, err` は保持 / `true, err` は削除・`Sweep` からも消える・同じ人が次のゲームを開ける)。修正前に新テストが落ちることを確認した: `.harness/runs/20260914-121315/verify-C2-06-0-finding-mutation.txt`。検証出力: 同 `verify-C2-06-0-finding-{build,test}.txt`(build+vet exit=0 / `TestSession` 11 件 PASS・exit=0)。

- C2-06(`/highlow` コマンド・ボタン・表示)= 490d972。`internal/commands/highlow.go` の `HighLowCommand` は `Command` と `ComponentHandler` の両方(`RegisterComponent` の最初の利用者)。盤面は公開 embed + 3 ボタン、決着でボタン全無効化。`casino_shared.go` に `interactionResponder` / `respondVia` / `messageResponse` を足し、`respond` はそれを呼ぶだけにした。`/balance` はゲーム中だけ「ゲーム中の預かり」行を出す。検証出力: `.harness/runs/20260914-121315/verify-C2-06-{1,2,3,4,5-gofmt,6-balance}.txt`(build+vet exit=0 / 対象 22 PASS・FAIL 0 / commands ok / `go test ./...` 8 パッケージ ok / gofmt 差分なし / `/balance` 2 件 PASS)。

- C2-06 の評価者指摘 4 件(反復 6 の `NEEDS_WORK`)= 242f72c。(1) 精算が拒まれても盤面は「未払い」を覚え、次の押下が**ゲームを繰り返さずに支払いだけ**やり直す(🔁 のボタン 1 個を残す)。(2) 未配達の盤面の返金は `Close` が `true` を返したときだけ — 通信エラーが届くころには次のゲームの預かりに変わっていることがある。(3) `boardLocks`(盤面ごとのロック)を `casino_shared.go` に置き、手の適用から応答までを直列化した(セッション共通ロックは Discord 呼び出しをまたげない)。(4) 開始順序を完了条件どおり `EnsureCasinoAccess → OpenGame → Open` に直し、`Open` が失敗したら返金する。検証出力: `.harness/runs/20260914-121315/verify-C2-07-{1,4,5,6}.txt`(build+vet clean / TestHighLow・TestBalance 26 PASS / `go test ./...` 8 パッケージ ok / gofmt clean)。
- C2-07(`/blackjack` コマンド・ボタン・表示)= d5d30a5。`internal/commands/blackjack.go`: `/blackjack <bet>` は /highlow と同じガードと同じ順序、ナチュラルは配布時に精算して祝いを出す(応答は `ChannelMessageWithSource`、押下は `UpdateMessage`)。ボタンは 🃏 ヒット / ✋ スタンド / ⏫ ダブル、ダブルは「最初の判断」かつ「残高が 2 枚目の掛け金を賄える」ときだけ有効。ダブルは `CanDouble` → `AddToEscrow` → `Double` の順。検証出力: `.harness/runs/20260914-121315/verify-C2-07-{1,2,3}.txt`(build+vet clean / TestBlackjack 23 PASS / commands 全体 ok)。
- C2-07 の評価者指摘(反復 7 の `NEEDS_WORK`)= 284c633。開始応答が失敗したときの `Close` → 返金が盤面ロックを取っていなかったため、⏫ ダブルの `AddToEscrow` と盤面適用の隙間に割り込むと追加ベットが返らなかった。`closeUndeliveredHand` が押下と同じ per-board ロックを取ってから閉じる — ダブルは必ず手を終わらせるので、押下が先に済めば `Close` は false を返して二重に払わない。回帰テストはチャネルでその順序を固定する(`stakeWatchingBank` が `AddToEscrow` の直後で止める)。ロックを外した木で 5/5 落ちることを確認済み: `.harness/runs/20260914-154156/finding-7-3-mutation.txt`。検証出力: 同ディレクトリの `finding-7-{1,2}.txt`(build+vet clean / `go test ./...` 8 パッケージ ok)。
- C2-08(起動時の返金・Sweep goroutine・help・文書)= f3cdb69。`cmd/bot/main.go` は接続後に `casino.Default().RefundStaleEscrows(time.Now())` を呼んで件数をログし、掃除人 goroutine を 9 時掲示と同じ ctx で起動して、掲示と同じ理由でセッションを閉じる前に終了を待つ。本体は `internal/commands/casino_sessions.go`: 30 秒ごとに `Sweep` → `AutoResolve` → `SettleGame` → メッセージを「⌛ 時間切れ — 自動決着」に編集(チップが先、表示が後。編集失敗はログのみ)。`/help` は登録レジストリを読むので 2 本は自動で載る — `help_test.go` がそれを固定した。README にカジノ節を追加。`Docs/agent-guide/architecture.md` は展開コピーなので触らず、正本への追記案を `blocked/C2-08.md` に置いた(**人が MyWorkflow 側へ写して再展開する**)。検証出力: `.harness/runs/20260914-154156/verify-C2-08-{1,2,3}.txt`(build+vet clean / `go test ./...` 8 パッケージ ok / `go build -o bin/todayistodaybot ./cmd/bot` exit=0)。
- C3-08(掲示できなかった回の当選を取りこぼさない)= 6662270。`collectDailyAnnouncements` が job に載せる `LotteryDraw` の条件を `last.Date == today` から **`last.Date > economy.LastAnnounced`**(= まだ掲示していない回)へ変えた。9 時に Bot が落ちて翌朝復旧すると、起動時のロールオーバーが前日付の抽選を精算するため、今日の日付と一致せず**どの巡回でも二度と拾われない**当選者が出ていた。日付は両方 `"YYYY-MM-DD"` なので辞書順 = 時系列順、`LastAnnounced` の零値 `""` は全ての実日付より前に並ぶ(一度も掲示していないギルドの古い当選は 1 回だけ出て、送信成功で退役する)。重複掲示は `MarkAnnounced` が**送信成功時だけ**書くことで防がれる — 送信に失敗した回は次の巡回でまた対象になる。
  文言も合わせた: `lotteryAnnounceLines` の見出しは `LastDraw.Date` から作る「🏆 7/10 の当選: …」で、日付の無い当選者なしは「🏆 前回の当選: 該当者なし — 賞金は繰り越し」。新しい `lotteryDrawDateLabel` は parse に失敗した日付を**そのまま**出す(手編集のファイルに embed で嘘の日付を言わせない)。
  `TestCollectDailyAnnouncements_OmitsAStaleDrawAndCarriesTheOpenPot` は前提が「今日の日付と違う」から「もう掲示済み」へ変わるので `..._OmitsAnAlreadyAnnouncedDrawAndCarriesTheOpenPot` へ書き換えた(`LastAnnounced = 2026-07-02` を置き、7/1 の当選が出ないことを固定)。新規は `..._CarriesADrawTheOutageNeverAnnounced` — 7/9 まで掲示済み・7/10 の当選が残った状態で 7/11 に集めると 7/10 の当選が job に入り、`MarkAnnounced(7/11)` のあと 7/12 に集めると入らない。`st.rng = &lotteryRand{t: t, float: 0.5}`(rolls 空)にしてあるので、想定外の抽選が走ると `Intn` で落ちる。
  検証出力: `.harness/runs/20260914-222248/verify-C3-08-{1,2,3,4}.txt`(build+vet exit=0 / casino 17 件 PASS / commands 15 件 PASS / `go test ./...` 8 パッケージ ok、いずれも exit=0)、`mutation-C3-08.txt`(条件を `last.Date == today` へ戻すと新テストが `job.LotteryDraw = <nil>` で落ちる)。

## In progress
- (なし)

## Next
- **次は C3B-21**(新規、C3B-P2 の唯一の不揃い)。BJ の未送達の手だけ、返金に失敗しても閉じる。`AutoResolve` が
  スタンドなので盤面を残すと未見の手を打たれる、というのが理由。**選択肢 (a)(b)(c) はタスクに書いてある** —
  どれを取るかは製品判断なので、実装より先にそこを決める。危険地帯。
- **次は C3B-18**(預かりの印を開始前に確定させ、掃除人が印の不一致で盤面を落とさない)。**危険地帯かつ差し戻し**なので未完の中で最優先。反復 5 と並行して親が `NEXT_FINDINGS.md` の C3B-13 差し戻しからタスクへ切り出したもの(コミット `787cb71`)で、`NEXT_FINDINGS.md` 側の節はもう残っていない — 内容はタスクの `done-when` が全部持っている。
- **その次は C3B-17**(スロットの配当も上限で切り詰める)。C3B-12 で他の精算経路を確認したときに見つけた最後の不揃いで、`Store.Spin` だけが上限で**拒否する**側に残っている。設計書 §2 がスロットをその例として名指ししているのに実装が唯一の例外、という形。**争点はジャックポット** — 当たりで `economy.Jackpot = JackpotSeed` と一緒にリセットされるので、入り切らない分をそのまま捨てるとプールの分まで消える。宝くじ(`drawLotteryLocked`)は溢れをプールへ戻しており、同じ形にできるかを決める必要がある。
- **その次に C3B-19**(新規)(`MarkAnnounced` にも C3B-16 と同じ再試行を入れる)。C3B-16 で `paths:` の外だったという理由だけで残した積み残しで、設計上の判断はもう付いている(入れる)。`markSeasonAnnouncedWithRetry` が雛形。**再試行の待ちを `RunAnnounceScheduler` の `SleepFunc` に相乗りさせないこと** — C3B-16 が `markRetryWaiter` を別引数にした理由がそのまま効く。
- **旧: 次は C3B-13**(預かりに持ち主の印を付け、別の盤面の預かりで精算できないようにする = blocking 2)。`TASKS.md` の未完で最優先。C3B-12 で `SettleGame` の入金側は触ったが、**どの盤面の預かりか**を見ていない点は手つかず — `staked := account.Escrow` は「いま預かっている額」であって「この盤面が預けた額」ではない、が争点の中心。
- **C3B-17 は新規**(スロットの配当だけが上限で拒否する側に残っている)。C3B-12 の確認で見つけた。P2 — 取引ごと巻き戻るので賭け金は戻り、`SettleGame` のような行き詰まりはしない。
- **旧: 次は C3B-10**(月末に結果送信だけ失敗した場合の再送を回帰テストで押さえる)。実装の不具合は反復 4 の評価で見つかっていない — 足りないのは検証で、C3B-08 の done-when が名指しした「7/31 に結果送信だけ失敗 → 8/1 に 6 月の結果を再送」を、収集関数だけでなく**掲示側**(`startAnnounceScheduler`)から通す必要がある。雛形は `TestStartAnnounceScheduler_AFailedSeasonResultIsSentAgainNextPass` と、今回足した `TestStartAnnounceScheduler_ResendsOnlyTheSeasonResultLaterTheSameDay`。**7/31 → 8/1 は月替わりを通るので `LastSeason` が 7 月へ移る** — 送られるのは 6 月の結果である、が争点。
- **その次は C3B-11**(README と `blocked/C3B-07.md` の文言を実装に合わせる)。コードは変えない。
- **次は C3B-09**(`TASKS.md` の最後の未完 = 反復 2 の所見 2「同日中の次の巡回では結果を再送できない」)。C3B-08 で待ち行列の形は決まったので、残るのは**収集の条件**だけ — `collectDailyAnnouncements` は `today <= LastAnnounced` でギルドごと飛ばすので、embed が載った日のうちは `UnannouncedSeasons` / `Lottery.Unannounced` に何が残っていても運ばれない。所見の再現手順(embed 成功・結果送信失敗 → 同日 11 時に再起動)は `NEXT_FINDINGS.md` の反復 2 節に残してある。**行番号は C3B-08 の差分でずれている**(`casino_announce.go:272` は今の `return nil` ではない)ので、参照は本文の記述で引くこと。
- **旧: 次は C3B-08 → C3B-09**(反復 2 の評価者所見を `TASKS.md` へ落とした 2 件)。C-3b の機能実装と文書はこれで閉じたので、残るのは掲示の取りこぼし 2 件だけ。**C3B-08 が先** — 2 件とも `announce.go` の収集条件を触るので、待ち行列の形を決める方(P1)を先に閉じないと C3B-09 の差分が二度手間になる。`NEXT_FINDINGS.md` の該当節は、対応するタスクを閉じるときに消す(いまは残してある — 再現手順が所見の側にしか無い)。
- **旧: 次は C3B-07**(README・設計書の実測・architecture の追記案)= `TASKS.md` の最後の未完。C-3b の実装は C3B-06 で全部閉じた。README のコマンド一覧に `/duel` と `/season` が無いのはここで拾う(下の残件と同じもの)。
- **旧: 次は C3B-06**(9 時掲示のシーズン欄と結果の祝い)。表示側で残っているのは掲示だけ — `/season` と `/balance` は C3B-05 で閉じた。`SeasonStatus` と同じ数(上位 3 名・純利)を掲示が必要とするが、掲示は `AnnouncementJob` を組む側(`internal/casino/announce.go`)なので、`SeasonView` を再利用するのではなく `LastSeason` / `SeasonRanks` を直に読む形になる。
- **`README.md` のコマンド一覧に `/duel` と `/season` はまだ無い**。C3B-05 の `paths:` に README が無かったので触っていない(`/help` はレジストリ生成なので自動で載る)。README を触るタスクで拾う。
- **旧: 次は C3B-05**(`TASKS.md` の未完の先頭)。C3B-04 で C-3b のゲーム側は閉じた — 残るのは見せる側(`/season`、`/balance` の今月の純利行、9 時掲示のシーズン欄、`/help` と README の一覧)。
- **`/help` と `README.md` に `/duel` を足すのはまだ**(§5 は `/season` と同じタスクで足すと書いている)。C3B-04 では触っていない。
- **掃除人の任意インターフェースは 3 つになった**(`casino_sessions.go`): `timedOutBoardRenderer`(ボタン)・`timedOutBoardResultRenderer`(本文)・`timedOutBoardSettler`(精算そのもの)。新しいゲームを足すときは、既定(`AutoResolve` → `SettleGame`)で足りるかを最初に決める。
- **旧: 次は C3B-03**(`TASKS.md` の未完の先頭)。C3B-02 までで `SeasonNet` は**書き込み側も読み取り点も閉じた** — 残るのはシーズンの境界(月替わりで全口座の `SeasonNet` を 0 に戻し、賞与を配る)と、それを見せるコマンド層。C3B-03 は全口座を触るので C3B-02 と同じ危険地帯。
- **`SeasonNet` を新しい経路から動かすときの規律**(C3B-03 以降がここを踏む): (a) 計上は**入金が成功したあと**、(b) 入金が `creditChipsCappedLocked` 経由なら**戻り値**を計上する(`prize`/`payout` ではない)、(c) 掛け金を持つゲームは `Escrow` を消す**前**に読む、(d) チップを配るだけ・両替するだけの経路は触らない。`store.go` の `addSeasonNetLocked` の直前コメントが (b) の理由を持っている。
- **次は C3B-02(`SeasonNet` を全ゲームの決着に配線する)**。`UserAccount.SeasonNet` と `addSeasonNetLocked` は C3B-01 で**既に置いてある** — C3B-02 が足すのは (a) スロット / H&L・BJ の `SettleGame` / 宝くじの各決着からの呼び出しと、(b) **読み取り点(`ensureAccountLocked` → `normalizeAccountLocked`)での `[-MaxChips, MaxChips]` 正規化**。(b) はまだ無い(C3B-01 は書き込み側だけを閉じた)。`SettleGame` は「賭けた額」を知らないので、`clearEscrowLocked` の**前に** `account.Escrow` を読んで使う(ダブル込みの総額)。
- **C-3b の残り**: C3B-02〜C3B-07。仕様 `Docs/superpowers/specs/2026-09-15-casino-c3b-design.md`、ブランチ `feat/casino-c3b`。`--evaluate every` で回す(最後のタスクも評価させる)。これでフェーズ C が閉じる。
- **C3B-01 は危険地帯(資金の保存則)なので、段階が探索期でも評価者(Astra, `codex exec -m gpt-6-astra -s read-only`)を通す**。C3B-02 / C3B-03 も同じ危険地帯(C3B-03 は全口座を触る)なので、**3 件まとめて 1 つの差分として**回すのが 1 差分 2 周の枠を無駄にしない。
- **`AcceptDuel` が増やした公開センチネルは 2 つ** — `ErrDuelSelf` と `ErrDuelStakeMismatch`。C3B-04 のコマンド層は前者に「❌ 自分自身とは対戦できません」を当て、後者は盤面とファイルの不一致なので汎用の失敗文言でよい(ユーザー操作では到達しない)。
- **C-3a は完了**(2026-09-15: C3-01〜C3-19 着地。Astra 1 周目 blocking 3 → 対応 → 2 周目 blocking 2(修正が持ち込んだ新規)→ 対応 → 未評価だった移行コミットの単発レビュー blocking 2 → 対応 → 収集側の比較を親が直して締め)。`main` へ ff 済み。次は C-3b(/duel + 月次シーズン制)の M1 設計。
- **`TASKS.md` に未完のタスクは無い**(38 件すべて done。C3-19 が最後の 1 件)。`NEXT_FINDINGS.md` も見出しだけ。先へ進むには M1 へ戻って項目を足す。
- **この差分のレビューは 2 周で打ち切り済み**(1 差分 2 周まで)。C3-19 は 2 周目レビュー(`.harness/reviews/2026-09-15-astra-c3-18.md`)の blocking 2 件を落としたもの。**次に評価者へ回すなら、それは新しい差分として**回す。
- **「修復の条件に消費側の都合を混ぜない」が今回の一般則**(C3-18 の「宛先のあるギルドだけ修復する」を**取り消す**): 古い形式の修復は「その記録を**保存する価値があるか**」だけで決める。「今それを使えるか」(掲示先がある・今日が掲示日)は消費側の問い。混ぜると、使えるようになるまで待つつもりの記録が、**待っている間に上書きされて消える**。読み取り点に置いてあることは「いつでもやり直せる」を意味しない — 修復の材料(ここでは `LastDraw`)自体に寿命がある。
- **単調な日付フィールドは後退を writer 側で止める**: `LastAnnounced` のように「ここまでは済んだ」を表す永続フィールドは、複数の読み手が上限線として使う。時計は巻き戻りうる(NTP 補正・ホストの日付ずれ)ので、後退させない保証は**読み手ごとの分岐ではなく writer 1 か所**に置く。
- **`TASKS.md` に未完のタスクは無い**(38 件すべて done。C3-18 が最後の 1 件)。`NEXT_FINDINGS.md` も見出しだけ。先へ進むには M1 へ戻って項目を足す。
- **次の区切りは評価者(Astra, `codex exec -m gpt-6-astra -s read-only`)の 2 周目の確認**。2 周目レビューの blocking 2 件(C3-17 / C3-18)がこれで両方落ちたので、**この差分のレビューはこれが最後**(1 差分 2 周まで)。2 周目は前回指摘への対応差分だけを見る — 見るのは `225be4c`(C3-17)・`3447f50`(設計書の文言)・`056ad7b`(C3-18)の 3 コミット。
- **「移行は読み取り点に置く」が今回の一般則**: 古い形式のファイルが持ち込む欠落は、消費する側(掲示)ではなく**読み取り点**で埋める。消費側に置くと、その回の抽選が `LastDraw` を上書きしたあとでは手遅れになる。同時に、**修復は宛先のあるギルドだけに限る** — 誰も読まない書き込みは足さない(移行は読み取り点なので、宛先が後からできればそのとき効く)。
- **`TASKS.md` の未完は C3-18(2 周目 blocking 1: 旧形式の未掲示結果が待ち行列へ移行されない)1 件**。`NEXT_FINDINGS.md` は見出しだけ。C3-17 と C3-18 を閉じたら 2 周目の blocking は全部落ちるので、次の区切りで評価者(Astra)の**2 周目の確認**へ回す(レビューは 1 差分 2 周までなので、これが最後)。
- **「正規化点は切り詰めてよい/よくない」の線引きが今回の一般則**: 正規化点のクランプは**ファイルが持ち込んだ値の修復**であって、実装が作った値の後始末ではない。上限を超えうる値を**作る側**(抽選の繰り越し、上限に座った当選者への支払い)が、書き込みの時点で超過分の行き先(= プール)を決める。新しい永続フィールドを足すときは、正規化点を通すことに加えて「そのフィールドの writer は上限を超える値を作りうるか」を確かめる — 作りうるなら行き先を writer 側に書く。
- **`TASKS.md` に未完のタスクは無い**(C3-16 が最後の 1 件)。先へ進むには M1 へ戻って項目を足す。`NEXT_FINDINGS.md` も見出しだけで、残課題は無い。次の区切りは**評価者(Astra)を通すところ** — C3-14 / C3-15 / C3-16 は 3 件とも危険地帯(資金の保存則)なので、段階が探索期でも評価は必須。
- **口座の正規化はこれで閉じた。** `internal/casino/store.go` で `economy.Users` を直接引くのは `ensureAccountLocked` だけ。新しい読み取り経路を足すときは、そこを通すか(書き込み経路)`normalizeAccountLocked` を呼ぶか(口座を作りたくない表示経路)のどちらかにする。どちらも通さない経路を足すと、C3-13 / C3-14 / C3-16 で閉じた穴がその経路から開き直す。
- **次は C3-16(正規化点を通らずに口座を触る経路 `RefundStaleEscrows` を塞ぐ)**。`TASKS.md` の未完はこれ 1 件。入力は反復 1 の評価者の指摘で、`NEXT_FINDINGS.md` からは消してある(C3-16 が同じ内容を持っている)。
- **`NEXT_FINDINGS.md` は空(見出しだけ)になった。** 残っていた 3 節はすべて処理済み — C3-09 の差し戻しと所見 4 は今回閉じ、C3-12 の paths 違反は親が「処理不要」と判断済み。次の評価で `NEEDS_WORK` が出たらここへ書く。
- **タスクの `paths:` が実在しないファイルを指していた**(`internal/casino/announce_test.go`。実際は `internal/commands/casino_announce_test.go`)。C3-12 のときと同じく**コードではなく記録の方を実態へ直した** — `TASKS.md` の `paths:` を書き換え、理由を `notes:` に残した。`paths:` はタスクを書いた時点の推測なので、実装中に実在しないと分かったら直すのが正しい(狭すぎる `paths:` を守って done-when を落とす方が高くつく)。
- **次は C3-15(未抽選の「次回抽選」テストを足し、旧挙動のコメントを消す)**。`TASKS.md` の未完はこれ 1 件で、入力は `NEXT_FINDINGS.md` に残った所見 4 と、C3-09 の反復 3(`DrawDate == ""` のケースが購入・status の表に無い)。
- **`NEXT_FINDINGS.md` から今回消したのは所見 2・3 の 2 節だけ。** `## C3-07 の区切り評価` の所見 4、C3-09 の反復 3、C3-12 の反復 3(親が「処理不要・消してよい」と判断済み)は残してある。
- **正規化点は 3 つになった** — プールの `seedJackpotLocked`、宝くじの `normalizeLotteryLocked`(どちらも `ensureTodayRateIndexLocked` の先頭)、口座の `normalizeAccountLocked`(`ensureAccountLocked` の中)。新しい永続フィールドを足すときは、この 3 つのどれかを通る読み取り経路になっているかを確かめる。通らない経路を足すと、閉じた穴がその経路から開き直す。
- **`TASKS.md` に未完のタスクは無い。** 次はここから先へ進むために `TASKS.md` へ項目を足す必要がある(M1 へ戻る)。候補は `NEXT_FINDINGS.md` に残っている 3 件 — 所見 2(当選者がいない回でハウス分が消える。**通貨が消える既知の穴としては最優先**。`lottery.Carryover = prize` の経路に `creditJackpotCappedLocked(economy, house)` を足す)/ 所見 3(`creditChipsCappedLocked` の `headroom` が `Chips = Escrow = MaxInt64` の手編集であふれる。**今回の規律をそのまま口座側へ適用する — 個別の引き算にガードを足すのではなく、口座の読み取り点に正規化を置く**)/ 所見 4(`casino_announce.go` 108 付近の旧挙動コメント。文字だけ)。C3-09 の反復 3(`DrawDate == ""` のテスト不足)と C3-12 の反復 3(許可パス)は既にコミット済みの対応がある。
- **`NEXT_FINDINGS.md` から今回消したのは「反復 2」(C3-11 の差し戻し)の節だけ。** `## C3-07 の区切り評価` の所見 2・3・4、C3-09 の反復 3、C3-12 の反復 3 は残してある。
- **新しい永続フィールドを足したら、その読み取り点に正規化を置くか、既存の正規化点を通ることを確かめる。** 現在の正規化点は `ensureTodayRateIndexLocked` の先頭にある 2 つだけ(`seedJackpotLocked` と `normalizeLotteryLocked`)で、レートの修復も同じ場所にある。ここを通らない読み取り経路を足すと、今回閉じた穴がその経路から開き直す。
- **次は C3-13(永続値の読み取り点での正規化)**。`TASKS.md` の未完はこれ 1 件。入力は `NEXT_FINDINGS.md` の「反復 2」(C3-11 の差し戻し: プールに空きがあっても桁あふれした賞金が消える)と、`## C3-07 の区切り評価` に残っている所見 2・3。
- **`NEXT_FINDINGS.md` から消したのは C3-08 の反復 2 の節だけ**(C3-12 で閉じた)。`## C3-07 の区切り評価` の所見 2・3・4 と、C3-09 の反復 3、C3-11 の反復 2 は残してある。
- **掲示待ちに手を入れるときの不変条件**: 待ち行列を消してよいのは `MarkAnnounced` だけで、条件は `Date <= date`。「掲示したら全部消す」に変えると、送信中に確定した回が一度も出ないまま消える。`collectDailyAnnouncements` 側で日付の絞り込みを足すのも同じ穴を開ける。
- **次は C3-12(未掲示の当選を並べて持つ)**。`TASKS.md` の未完はこれ 1 件だけになった。入力は `NEXT_FINDINGS.md` の「反復 2」の節(残してある)。
- **`NEXT_FINDINGS.md` は「所見 1 と反復 1」だけを消した。所見 2・3・4 と反復 2・3 は残してある** — done-when は「2 節を消す」と書いてあるが、`## C3-07 の区切り評価` の節には**まだタスクになっていない所見 2・3・4 が同居している**ので、節ごと消すと未処理の指摘が消える。所見 1 の位置には「C3-11 で閉じた」の 3 行を残し、番号は振り直していない(過去のコミットと PROGRESS が 2・3・4 の番号で参照しているため)。
- **未タスクの差し戻しが 4 件残っている**(次に `TASKS.md` を足す人へ): 所見 2(当選者がいない回でハウス分が消える。「入り切らない分だけが消える」以外で通貨が消える唯一の既知の穴なので**優先度が高い**。直し方は今回と同じ `creditJackpotCappedLocked(economy, house)` で済むはず)/ 所見 3(`creditChipsCappedLocked` の `headroom` が `Chips = Escrow = MaxInt64` の手編集であふれる。**今回プール側にやったのと同じ手当てが口座側に必要** — 引き算の前に負値と上限超過を弾く)/ 所見 4(`casino_announce.go` 108 付近の「the pot rolls forward」が旧挙動のまま。コメントだけ)/ 反復 3(C3-09 の `DrawDate == ""` ケースのテスト不足)。
- **プールに何かを足す新しい経路を書くときは、`economy.Jackpot += ...` を直接書かないこと**。`creditJackpotCappedLocked` を通す — 上限が守られているのは「加算点が 1 つしかない」という一点に乗っており、直接加算を 1 か所書いた時点で構造的な保証ではなくなる。`store.go` のプール節の先頭コメントが writer の一覧を持っているので、増やしたらそこも直す。
- **`.harness/runs/20260915-014154/verify-C3-11-1.txt` は今回のタスクのもので上書きした**。C3-11b(設計書の掲示文言)の証拠は `verify-C3-11b-1.txt` / `recheck-C3-11b-1-1.txt` へ複製して残してある — 同名タスクが 2 つあった名残なので、`Done` の C3-11b の行が指す先はこちら。
- **次は C3-11(プールの上限と桁あふれの正規化)**(`TASKS.md` の未完の先頭)。同名の C3-11(設計書の掲示文言)は今回で done なので、残る未完は**プールの上限**の方と C3-12(未掲示の当選を並べて持つ)の 2 件。
- **`NEXT_FINDINGS.md` が作業ツリーで 0 バイトに削られていた**(この反復の開始時点。`git status` が `M` を出していた)。中身は C3-11(プールの上限)・C3-12 の入力そのものなので、**`git checkout -- NEXT_FINDINGS.md` で HEAD の内容へ戻した**(今回のタスクの `paths:` 外なので変更としては入れていない)。次の反復はこのファイルを読んでから消すこと — 消してよいのは対応するタスクを閉じた節だけ。
- **次は C3-11**(`TASKS.md` の未完の先頭)。ただし `TASKS.md` には **C3-11 が 2 つある**(設計書の掲示文言 / プールの上限と桁あふれの正規化)。拾うときに見出し本文で区別すること。そのあと C3-12(未掲示の当選を並べて持つ)。
- **`NEXT_FINDINGS.md` に反復 3 の差し戻しが入っている(未処理)**: C3-09 の `DrawDate == ""`(未抽選)ケースの購入・status テストが `store_test.go:3258` の表に無い、という指摘。**C3-10 の `paths:` の外なので今回は手を付けていない** — 次の反復がタスクより先に処理する。最小修正は指摘のとおり、未抽選の Store に対し `now = 2026-09-14 08:59:59 JST` で `NextDrawAt == nextRunAt(now)` を固定するテストの追加。
- **文書の「プールが動かない」系の記述は全部が誤り**(C3-10)。積立は毎スピン走るので、プールが動かないのは「ベットが小さくて端数が 100 に届かない回」だけ。今後ジャックポットの説明を書くときは「払い出し」と「積立」を分けて書く。
- **次は C3-10**(`TASKS.md` の未完の先頭。💎💎💎 の説明を「払い出しは無いが積立は行う」に直す)。残りは C3-10・C3-11(設計書の掲示文言)・C3-11(プールの上限。**同じ番号が 2 つある** — 拾うときに取り違えない)・C3-12(未掲示の当選を並べて持つ)。
- **`NEXT_FINDINGS.md` は C3-07 の 4 件がまだ未処理**(プールの桁あふれ → C3-11 として切り出し済み / 当選者がいない回でハウス分が消える / `creditChipsCappedLocked` の headroom / 旧挙動のコメント)。加えて反復 2 の C3-08 差し戻し(未掲示の当選が次の抽選で上書きされる)が C3-12 に対応する。**タスク化済みのものと未タスクのものが混ざっている**ので、次に触る人は節ごとに対応先を確かめてから消すこと。
- **`NextDrawAt` は「次の 09:00」ではなく「この券が引かれる 09:00」になった**(C3-09)。ここへ第三の呼び出しを足すときは `nextRunAt(now)` を直接使わず `nextLotteryDrawAt(lottery.DrawDate, now)` を通す。逆に**9 時掲示スケジューラの待ち時間は `nextRunAt` のまま**でよい — あちらは「次にいつ起きるか」であって「どの回か」ではないので、抽選日で押し出すと掲示が 1 日飛ぶ。
- **次は C3-09**(`TASKS.md` の未完の先頭。「次回抽選」を呼び出し時刻ではなく確定済みの抽選日から出す)。そのあと C3-10(💎💎💎 の説明)と、C3-08 が切り出した C3-11(設計書の掲示文言)。
- **`NEXT_FINDINGS.md` の C3-07 所見 1・2・3 は未処理**(プールへの加算の桁あふれ / 当選者がいない回でハウス分が消える / `creditChipsCappedLocked` の headroom)。所見 2 は「通貨は消えない」に直接反するので、C3-09 より先に拾う価値がある。所見 1 は**設計判断が要る**(飽和加算で済ませるか、不正値の検査を 1 か所へ集約するか)ので、決められなければ `blocked/` へ落として親へ。
- **掲示の `LotteryDraw` は「今日精算した回」ではなく「まだ掲示していない回」**(C3-08)。ここへ新しいフィルタ(日数の上限など)を足すと、また取りこぼしの経路ができる。古い当選が延々と出ないことを保証しているのは `LastAnnounced` の更新であって日付の一致ではない。
- **`Carryover` は当選者がいなかった回だけの仕組みになった**(C3-07)。`internal/commands/casino_announce.go` の「該当者なし — 賞金は繰り越し」はこの経路にだけ出るので文言は正しいまま。今後「賞金の一部が次回へ」という UI を足さないこと — 当選者がいた回に繰り越しは発生しない。
- **C3-07 は危険地帯なので Astra を 1 周かけた**(`codex exec -m gpt-6-astra -s read-only`、対象はこの差分のみ)。判定は `NEXT_FINDINGS.md` を見る — `NEEDS_WORK` があれば次の反復が C3-08 より先に処理する。
- **次は C3-08**(`TASKS.md` の未完の先頭。C-3a 区切りレビューの blocking 2 件目)。
- **`TASKS.md` に未完は無い**(C3-01〜C3-06 すべて done)。次に進めるなら M1 へ戻って C-3b(`/duel`・シーズン制)の設計を詰めるか、下の残課題を拾う。
- **`blocked/C3-06.md` はユーザー(人)待ち** — `Docs/agent-guide/architecture.md` の正本(MyWorkflow 側)へ写して `node MyWorkflow/deploy.mjs --apply todayistodayBot` で再展開するまで、展開コピーは C-2 までの記述のまま(宝くじ・ジャックポットが載っていない)。
- **次は C3-06**(`TASKS.md` の未完の先頭)。C-3a で残るのはこれ 1 件で、掲示・コマンド・ストアはすべて着地済み。
- **掲示に sender が 2 つある**(C3-05 以降): `startAnnounceScheduler(ctx, st, send, sendText, now, sleep)`。掲示側へ新しい「別メッセージ」を足すときは `sendText` を使い、**失敗でエラーを返さない**(返すと掲示ごと翌朝にやり直しになる)。実物を差すのは `StartCasinoAnnounceScheduler` だけで、どちらも `redactInteractionError` を通す。
- **`internal/commands` の JST は `lottery.go` の `jstZone`**。掲示側で時刻を出すなら新しく `FixedZone` を作らずこれを使う(`internal/casino` の `jst` は非公開で、パッケージを跨げない)。
- **C-3a の区切りで評価者(Astra)を 1 周**。探索期なので反復ごとには呼ばないが、危険地帯(`internal/casino.Store` の単一ライター規律)へ C3-01〜C3-05 が繰り返し触っているため、C3-06 の後に `codex exec -m gpt-6-astra -s read-only` を通す。基点は `ef807c5^..HEAD`。
- **次は C3-04**(`TASKS.md` の未完の先頭)。ストア側は揃ったので、残るは表示 — `/lottery buy` / `/lottery status` のコマンドと、9 時掲示の 🎟️ フィールド・当選者への公開の祝い。`LotteryPurchase` / `LotteryView` は文字列を一切持たない(❌ の文言は `internal/commands` 側の責務。`ErrLotteryLimit.Remaining` と `ErrInsufficientChips.Balance` が「あと N 枚」「現在 N 枚」をそのまま埋められる形で入っている)。`AnnouncementJob` の `LotteryPrize` / `LotteryTickets` は**今から買える方の壺**で、抽選直後なので繰り越しが無ければ 0 — 「今日の賞金プール」としてそのまま出してよい。
- **`Jackpot` を動かす書き手が 3 つになった**(C3-01 Notes の保存則を更新): スロットの積立(`accrueJackpotLocked`)、7️⃣7️⃣7️⃣ の発火(`Spin`)、**宝くじのハウス分**(`drawLotteryLocked`)。プールが `JackpotSeed` 未満へ落ちる経路は今も発火のリセットだけ。
- **次は C3-03**(`TASKS.md` の未完の先頭)。ここから宝くじ本体(`internal/casino/lottery.go`・`GuildEconomy.Lottery`)。抽選はレート生成と同じ日次ロールオーバーの `Update` の内側に置く(設計書 §3 の取りこぼし防止)ので、C3-02 で掲示が `ensureTodayRateLocked` の後にプールを読んでいるのと同じ読み取り点が当選結果にも効く。ハウス分(売上の 10 %)はジャックポットのプールへ入るため、`Jackpot` を動かす書き手が `Spin` 以外に増える — C3-01 の Notes に書いたプールの保存則(動くのは積立と発火だけ)はここで更新が要る。
- **次は C3-02**(`TASKS.md` の未完の先頭)。C3-01 でプールは動くが、まだ誰にも見えない — `/slot` の結果と 9 時掲示にプール額を出すのは表示側のタスク。`SpinResult.JackpotWon` は `Payout` の**内訳**であって上乗せではないので、描画で足し直すと二重計上になる(`slot.go` のフィールド注釈に書いた)。
- **C-3a 開始(2026-09-14)**: 仕様 `Docs/superpowers/specs/2026-09-14-casino-c3a-design.md`、タスク C3-01〜C3-06。ブランチ `feat/casino-c3a`。C-3b(/duel+シーズン制)は C-3a の後に M1 から。
- **C-2 は完了**(2026-09-14: C2-01〜C2-14 着地、区切りの Astra 1 周目 NEEDS_WORK(blocking 3)→ 対応 → 2 周目 PASS)。`main` へ ff 済み。次は C-3(ジャックポット+宝くじ+/duel+シーズン制)の M1 設計。
- **`TASKS.md` の未完は無し**(C2-01〜C2-14 すべて done)。次は C-2 全体の評価者 2 周目 — 反復 3 の指摘 2 件への**対応差分だけ**を見せる(Astra、`codex exec -m gpt-6-astra -s read-only`、基点は `bc95d1c^..bc95d1c`)。今回の対応は片方が「文書へ契約を書く」、もう片方が「指摘を採らない」なので、評価の争点は**判断の正しさ**(掃除人が決着済みの盤面を本当に見ないか)であってコードの挙動ではない。
- **文言を固定するテストは「フィールドの一致」では足りない**。`resp.Data.Content != casinoSettleFailedMessage` は定数と実装が一緒に動くので、嘘の文言へ書き換えても緑のまま通る。約束してはいけない語(ここでは自動精算)を名指しで禁じる検査を別に置くこと。
- **次は C2-14**(`TASKS.md` の未完はこれ 1 件)。反復 3 の評価者指摘(`NEXT_FINDINGS.md`)をタスクに切ったもの。1 点目(精算失敗の案内)は**文言を変えずに設計書へ契約を書く**方針 — 決着した盤面はセッションから外れて掃除人が二度と見ないので、「次回の自動処理で精算されます」は待てば済むという嘘になる(反復 3 の読み替えと同じ結論)。2 点目(`casinoPayoutLine` を `casino_sessions.go` へ移す)は**採らない** — `highlow.go` / `blackjack.go` / `casino_sessions.go` の 3 か所から呼ばれる共有ヘルパなので `casino_shared.go` が正しい置き場で、`paths:` 逸脱は記録として残す。この判断自体が次の評価者の対象。
- **README は CRLF と LF の混在ファイル**。Edit ツールで触ると周辺行の行末をまとめて書き換える(今回 15 行が CRLF 化した)。`git diff --numstat` と `--ignore-cr-at-eol --numstat` が一致するまで直すこと — 直し方は `git show HEAD:README.md` をバイト列で読み、対象行だけを同じ行末で置換して書き戻す。
- **次は C2-13**(`TASKS.md` の未完はこれ 1 件。README の総資産式に預かりを含め、小額ベットの RTP を文書化する)。文書のみ。
- **done-when の文言を 1 点だけ読み替えた**: C2-12 は精算失敗の案内を「⚠️ 精算に失敗しました。次回の自動処理で精算されます」にせよと書いていたが、既存の `casinoSettleFailedMessage`(「❌ 精算に失敗しました。もう一度お試しください。」+ 🔁 ボタン)のままにした。決着した盤面は `WithSession(done=true)` でマネージャから外れており、掃除人は**それを二度と見ない** — 自動で精算するのは次回起動の `RefundStaleEscrows` だけなので、「次回の自動処理で精算されます」は待てば済むという嘘になる。同じ括弧が要求する「BJ と同じ」を優先した(金額行を出さない、という検証可能な条件は満たしている)。
- **`-run` の名前に注意**: 掃除人のテストは `TestSweepIdleBoards*` で、`TestRunSessionSweeper` はループのテスト 1 件だけ。C2-12 の `verify:` 行の正規表現は前者を拾わないので、証拠は `verify-C2-12-3.txt`(`-run TestSweepIdleBoards`)を別に取った。以後 `TASKS.md` に掃除人の検証を書くときは `TestSweepIdleBoards` を入れること。
- **次は C2-12**(`TASKS.md` の未完は C2-12・C2-13 の 2 件)。`Session` の可変フィールドは `State` / `LastActionAt` / `Ref` / `Expired` / `PendingPayout` / `PayoutResolved` / `NeedsRedraw` の 7 つになった — 新しいフィールドを足すなら「誰がロック無しで読んでよいか」を `Get` のコピー規約と合わせて決めること(`NeedsRedraw` は `Hold` が返すコピーから読む)。
- **次は C2-11**(`TASKS.md` の未完は C2-11・C2-12・C2-13 の 3 件)。C2-10 で `Session` に `Expired` という第 3 の状態が増えたので、他の所見を直すときは「盤面はもう live か gone の 2 値ではない」ことを前提にする — 新しい経路を足すなら `Expired` を必ず場合分けする(拒む側が既定。触ってよいのは掃除人だけ)。
- **`TASKS.md` の未完は無し**(C2-01〜C2-09 すべて done)。次は C-2 全体の評価者 2 周目 — 反復 1 の指摘 3 件への対応差分だけを見せる(Astra、`codex exec -m gpt-6-astra -s read-only`)。危険地帯は escrow の保存則と二重決着(掃除人・ダブル・未送達の盤面の 3 経路)＋今回足した `Hold`/`Release`。
- (済)~~次は C2-09~~(README のコマンド一覧に C-1 のカジノ 7 コマンドを足す)。C2-08 で `/highlow` `/blackjack` だけを載せたので、`/balance` `/daily` `/rate` `/exchange` `/slot` `/rank` `/casino-admin` が README から抜けたままになっている。文書のみ。
- **C-2 の機能は C2-08 で全部着地した** — 次の区切りで評価者(Astra、`codex exec -m gpt-6-astra -s read-only`)を通す。危険地帯は escrow の保存則と二重決着(掃除人・ダブル・未送達の盤面の 3 経路)。
- **`blocked/C2-08.md` は人への申し送り**: `Docs/agent-guide/architecture.md` は展開コピーなので、C-2 の追記(レイヤー表・`DefaultSessions` の所有権・依存方向・危険地帯 5 点目)は MyWorkflow の正本へ写して再展開する必要がある。
- (済)~~次は C2-08~~(起動時の返金・放置の Sweep goroutine・help・文書)。`TASKS.md` の未完はこれ 1 件。Sweep を書くときは下の Notes「未払いの精算と Sweep」を先に読むこと — 決着した盤面は `WithSession(done=true)` で即座にマネージャから外れるので Sweep とは競合しないが、`AddToEscrow` と `Double` の隙間だけは Sweep が割り込める。
- (済)~~次は C2-07~~(`/blackjack` コマンド・ボタン・表示)。C2-06 と同じ骨格をなぞれる: `interactionResponder` + `respondVia`(`casino_shared.go`)、`snapshotHighLow` に相当する盤面スナップショット、`highLowButtonsOf` などのテスト補助(`highlow_test.go`。流用するなら共有ファイルへ出す)。ダブルは `CanDouble` を見てから `AddToEscrow`(C2-04 の Notes)。
- (済)~~次は C2-06~~(`/highlow` コマンド・ボタン・表示)。材料は揃った: `HighLowGame`(C2-03)・`BuildCustomID`/`RegisterComponent`/`requireSessionOwner`(C2-01)・`OpenGame`/`SettleGame`(C2-02)・`SessionManager`(C2-05、`RegisterComponent` はまだ登録者ゼロ)。順序の規律は Notes の「預け入れと Open の順序」を守る。
- (済)~~次は C2-05~~(セッション管理と放置の自動決着)。`internal/casino` の純粋ロジック 2 つ(`HighLowGame` / `BlackjackGame`)は揃った — どちらも `AutoResolve() int64` を持つので、セッション管理はこの 1 メソッドだけを知っていればよい。`internal/commands` 側の配線は C2-06 以降。
- (済)~~次は C2-04~~(ブラックジャックの純粋ロジック)。`cards.go` の `Deck`/`Card` はそのまま使える(`Draw` は末尾から引く。テストの `deckOf` が引く順で並べ替える)。`internal/commands` 側の配線は C2-06 以降。
- (済)~~次は C2-03~~(`internal/casino/cards.go` + `highlow.go` の純粋ロジック)。C2-01 で置いた `RegisterComponent` はまだ登録者ゼロ — 最初の利用者はハイ&ロー(C2-06)。C2-05 の `SessionManager` はまだ無い。
- **C-2 開始(2026-09-14)**: 仕様 `Docs/superpowers/specs/2026-09-14-casino-c2-design.md`、タスク C2-01〜C2-08(`TASKS.md`)。ブランチ `feat/casino-c2`。順に消化する。
- **C-1 は完了**(2026-09-14: R-001〜R-004 着地、Astra 2 周目 PASS、`main` へ ff マージ)。次は稼働(トークンと実行場所はユーザー判断)か C-2(ボタン基盤+ハイ&ロー+ブラックジャック、未設計 → M1 から)。
- `TASKS.md` の未完は無し(R-001〜R-004 すべて done)。次は R-003 修正差分の評価者 2 周目(前回指摘への対応差分だけを見る)。
- Astra の C-1 レビュー(`.harness/reviews/2026-09-14-astra-casino-c1-round1.md`)の所見を R 系タスクにして消化 → 2 周目 PASS → `main` へ ff マージ(ユーザー承認済み 2026-09-14)→ 片付け。稼働(トークン・実行場所)は後日、ユーザー判断。

## Notes
- **「除外」のテストは非ゼロから始める**(今回の一般則)。「この操作は X を動かさない」を 0 始まりで書くと、X を**ゼロに戻す**誤実装が素通りする(0 のまま 0 なので合格に見える)。デイリー・両替・mint が `SeasonNet` を消さないことは、`-777` を置いてから各操作を通し `-777` のままであることで初めて示せる。同じ形の主張(「触らない」「保つ」「冪等」)を書くときは、まず始点を動かす。
- **口座の正規化点は両端を見る**。`normalizeAccountLocked` はこれまで「0 で床、上限で天井」だけだったが、`SeasonNet` は**符号付き**なので 0 で床を張ると負けている人が全員イーブンに繰り上がる。新しい永続フィールドをここへ足すときは、そのフィールドが符号付きかどうかを先に決め、床を 0 にしてよいかを確かめる。
- **掲示済み境界 `LastAnnounced` は high-water mark**(前進のみ)。収集側も `today > LastAnnounced` で揃える — 片側だけ単調にすると、時計が巻き戻った日を毎回掲示し続ける(境界が下がらないので追いつけない)。回帰は `TestMarkAnnounced_DoesNotLetAClockRollbackRepostADraw` と `TestCollectDailyAnnouncements_StaysQuietWhileTheClockIsBehindTheMark` の 2 本で挟んである。
- **ループの穴**: `ALL_DONE` を返す反復(一覧の最後のタスク)は `--evaluate feature` だと評価者が回らない。最後の 1 件を確実に見せたいときは `--evaluate every` で回すか、着地後に単発でレビューする(C-3a では単発レビューが blocking 2 件を拾った)。MyWorkflow 側に所見として記録済み。
- **正規化点は「読み取り経路」ではなく「口座に触る経路」に要る(C3-16)**: C3-13 / C3-14 で「読み取り点に正規化を置く」と決めたあとも、map を直接引く経路が 2 つ残っていた。見落としの形はどちらも同じ — `ensureAccountLocked(economy, userID)` ではなく `range economy.Users` で回しているので、正規化点の存在そのものが視界に入らない。**探すときは `grep -n "economy.Users" internal/casino/store.go` を使う**(`ensureAccountLocked` の grep では見つからない)。書き込み経路には `ensureAccountLocked`、口座を作りたくない表示経路には `normalizeAccountLocked` — 表示経路に前者を使うと初回ボーナスが湧くので、2 つを使い分ける。
- **表駆動テストのケースは「実装のどの分岐に入るか」で選ぶ(C3-15)**: `DrawDate` の表に 2 ケースあっても、どちらも `drawLotteryLocked` の早期 return に落ちるなら**ロールオーバーは 1 度も走っていない**。未抽選(`""`)だけが抽選を走らせ、走ったあとの `DrawDate` を読む経路を通る — 「入力の見た目が違う」ではなく「通る経路が違う」がケースを足す理由。効き目は当選者なしの経路のスタンプを暦日へ変える mutation で確かめた(新ケースだけが落ちる)。
- **設計書 `Docs/superpowers/specs/2026-09-14-casino-c3a-design.md` 61/69 行目の「昨日の当選」は C3-08 で実装とずれた** — `paths:` の外なので触らず C3-11 として切った。README には該当の文言は無い。
- **`codex exec` は stdin を閉じないと無限に待つ**(2026-09-14, C3-07 の評価者で 22 分溶かした)。プロンプトを引数で渡しても
  `Reading additional input from stdin...` と出て**標準入力を読み続ける** — このハーネスの非対話シェルでは EOF が来ないので、
  CPU 0 % のまま生き続ける(「考えている」ように見えるが止まっている)。必ず `< /dev/null` を付け、出力は
  `> ファイル 2>&1` で直に受ける(`| tail` にすると終了までパイプに溜まって途中経過も読めない)。
  止まっているかの見分け方は `Get-Process codex` の CPU 列 — 0.00 のままなら待っているのは入力であって推論ではない。
- **C3-07 の評価者(Astra)は NEEDS_WORK**(全文 `.harness/runs/20260914-222248/astra-C3-07.txt`)。所見 4 件は `NEXT_FINDINGS.md` へ。
  4 件とも**ハンド編集された JSON か、コメントの文字だけ**の話で、通常の遊びで到達する経路ではない — 種入れ順序・移送先・
  `Carryover = 0`・`LastDraw.Prize = paid`・追加テストの有効性は評価者も正しいと認めている。
- **プールはハウスの金で、口座の保存則には参加しない(C3-01 / 危険地帯)**: 積立は配当表を変えずにハウスの取り分(7.01 %)から出るので、プレイヤーの控除はベット額のまま。したがって `Chips + Escrow` の保存則にプールは入らず、プールには**別の**保存則がある — `economy.Jackpot` が動くのは `accrueJackpotLocked` の積立か 7️⃣ 3 つの発火だけ。テスト(`TestStore_Spin_ConservesChipsAndPool`)は端数を言い訳にできないよう 1/100 チップ単位で両方を突き合わせ、期待値は**結果が報告した数**から組む(配当表をテストに書き写さない)。`seedJackpotLocked` の判定が `== 0` ではなく `< JackpotSeed` なのは、正規の経路では (0, 種) の値が作れない(種で始まり増えるだけ、発火で種に戻る)ため — 間の値は手編集か部分書き込みでしかありえず、読み取り点で直すのは `ensureTodayRateIndexLocked` がレートに対してやっているのと同じ規律。`JackpotAccum` が負だと Go の `/` と `%` がゼロ方向へ切り捨てるせいで積立がプールを**減らす**ので、そこだけ 0 に戻す。
- **C-2 の残課題(non-blocking)**: なし(2 周目で残ったのは文書の旧説明 1 件のみ → 修正済み)。精算失敗は手動再試行(🔁)だけが出口で、自動再精算は掃除人の期限切れ経路だけ(設計書 §9)。
- **表示が追いつかない盤面は「手を進めない」で守る(C2-11)**: 押下の応答(`UpdateMessage`)が失敗すると、盤面は進んでいるのにチャンネルのメッセージは前のカード/手のままになる。これを直すのに「編集を再試行する」経路は取らなかった — 1 回の Interaction に応答は 1 回しか送れず、再試行はボットトークンでの `ChannelMessageEdit`(= `Ref` 依存・別の失敗経路)になる。代わりに**次の押下が修理に使われる**: `NeedsRedraw` が立っている間は判定もチップの移動もせず、現在の盤面を描き直すだけ。ephemeral の通知は `FollowupMessageCreate`(応答そのものは再描画に使うため)で、失敗してもログだけ — 説明を失うのは盤面を失うより軽い。フラグを下ろすのは**再描画が届いたあと**なので、`SetNeedsRedraw` は `WithSession` の外から呼ぶマネージャのメソッドになっている(立てるのも下ろすのも Discord 呼び出しの向こう側の判断)。`LastActionAt` は触らない — フラグはボットの都合で、放置された盤面の寿命を延ばす理由にならない。
- **時間切れ盤面の所有権(C2-10 以降の危険地帯)**: `Expired` な盤面はマネージャの map に残ったまま掃除人ゴルーチンだけのものになる。だから掃除人は `session.State` と `PendingPayout` をロック無しで読み書きしてよい — 成り立っているのは `Get`/`Hold`/`WithSession`/`Touch`/`Close` が `Expired` を全部断るからで、ここに「断らない読み手」を足すとその瞬間にデータ競合になる。`AutoResolve` は札を引く(= 冪等でない)ので再試行では二度と呼ばない。`PendingPayout` が 0 の負け手と「まだ決めていない」を区別するために `PayoutResolved` が別にある。
- **押下中の盤面は掃除しない(反復 2 の指摘 2)**: 盤面ごとのロック(`boardLocks`)は押下どうししか直列化しない — 掃除人はそのロックを取らない。ダブルの `AddToEscrow` はディスク I/O なので `WithSession` の中では呼べず、その隙間は `LastActionAt` が更新されないまま残る。`Hold` はマネージャ自身のロック(= `Sweep` が期限判定に使うロック)で盤面に「操作中」の印を付け、`Sweep` はその盤面を返さない。押下が自分で終わらせた盤面は map から消えるので `Release` は空振りする(セッション ID は 16 バイト乱数なので別の盤面に当たらない)。効き目は `busy` 判定を外した木で `TestBlackjackDoubleIsNotSweptBetweenTheStakeAndTheHand` が落ちることで確認済み(`mutation-sweep-busy.txt`)。
- **時間切れのボタンはゲームが描く(反復 2 の指摘 3)**: 掃除人は `casino.AutoResolver` しか知らないが、設計書 §4・§8 は決着後もボタンを無効化して残せと言う。`timedOutBoardRenderer`(`DisabledComponents(sessionID, state)`)を `ComponentHandler` の**任意の**片割れにして、`custom_id` の prefix で引いたゲームに描かせた。実装しないゲームは空の行(= ボタン削除)に落ちるので、掃除人側に分岐は増えない。`registeredComponents` はテストが `resetComponentsForTest` で空にするため、この経路を見るテストは自分で `RegisterComponent` する。
- **起動の順序(反復 2 の指摘 1)**: 「起動時に残っている預かり = 消えた盤面」は、**新しいゲームがまだ 1 つも始まっていない間だけ**成り立つ。返金は `session.Open()` より前。`startupSteps` は 3 手を関数フィールドにしただけの構造体で、Discord 接続なしに順序をテストするためにある。返金が失敗したら開かない — 預かりが残ったままの口座は「進行中のゲームがあります」で遊べず、黙って劣化運転する方が悪い。
- **掃除人はゲームを知らない(C2-08)**: 掃除人が知っているのは `casino.AutoResolver` 1 メソッドだけ。だから時間切れの編集はゲーム固有の盤面を描き直さず、`⌛ 時間切れ — 自動決着` + 配当/残高の embed に差し替えてボタンを**消す**。設計書 §4 の「決着後のボタンは無効化して残す」から外れる唯一の場所 — ボタンのラベルはゲームの持ち物(手札や倍率が入る)で、掃除人が組み直すには全ゲームの描画を知る必要がある。
- **掃除人の順序(C2-08)**: `Sweep` が期限判定と削除を同じクリティカルセクションでやるので、返ってきた盤面はこの goroutine だけのもの(ロック無しで `AutoResolve` してよい)。精算が失敗した盤面は**編集しない** — 払われていないのに「決着しました」と出すのが最悪。預かりは口座に残り、次の起動の `RefundStaleEscrows` が返す。
- **`main.go` の shutdown(C2-08)**: 掃除人も 9 時掲示と同じく `session.Close()` の前に終了を待つ(`sweeperDone`)。編集の最中にセッションを閉じると use-after-close になる。掃除人の周期は `commands.CasinoSweepInterval`(30 秒)= 時間切れの player が余分に待つ上限。
- **未送達の盤面を閉じる経路もロックを取る(反復 7 の指摘)**: `Close` は「盤面を消す」だけに見えて、返金額の判断を伴う。⏫ ダブルが `AddToEscrow` をマネージャのロックの外で行う以上、閉じる側も押下と同じ per-board ロックを取らないと預かりの総額が読めない。
- **未払いの精算(C2-07 / 反復 6 の指摘 1)**: `Store.SettleGame` が拒んだとき、盤面は既にセッションから消えている。`boardLock.pending` がその「確定した配当」を持ち、次の押下は所有者検査のあと**支払いだけ**やり直す(`settle` は `lock.pending` の唯一の書き手で、チップが着いた瞬間に nil にする)。`boardLocks.release` は待ち手が 0 でも `pending` が残っていれば map から消さない — その記録が再試行そのもの。誰も押さないまま終わったプロセスでは記録ごと消えるが、預かりは次回起動の `RefundStaleEscrows` が返す。
- **盤面ごとのロック(C2-07 / 反復 6 の指摘 3)**: `SessionManager.mu` は全盤面を守るので Discord 呼び出しをまたげない(危険地帯)。その隙間で同じ盤面の 2 押下が「手は順番どおり・応答は逆順」になり、決着表示が遊べる盤面で上書きされる。`boardLocks` は**盤面ごと**のロックで、手の適用から応答までずっと握る。ロック順序は acquire が `b.mu → (解放) → lock.mu`、release が `lock.mu 保持のまま b.mu` — acquire は `lock.mu` を待つ前に `b.mu` を放すので逆転しない。効き目は `TestHighLowSerializesThePressesOfOneBoard` をロック無しの木で落として確認済み。
- **`casinoBank` の seam(C2-07)**: 「配当は確定したがチップは動かなかった」状態は経済側からは作れない(固定の山が上限を破る配当を配らない)。`casino_shared.go` の `casinoBank` インターフェースで `*casino.Store` を包み、テストだけが `SettleGame` を拒ませる(`interactionResponder` と同じ流儀)。
- **ブラックジャックの固定デッキは「探す」(C2-07)**: `newBlackjackFromShoe` は非公開なので、テストは `rand.NewSource(seed)` を種に `blackjackSeedWhere(t, 条件)` で目的の配布を**探す**。手で組んだ `Intn` 列は `NewDeck` のシャッフル実装に固定されるが、探索は配布そのものに固定される。期待値は `blackjackProbe` が同じ種で組み直した手から取る(casino の配当規則をテストに書き写さない)。
- **ダブルの隙間(C2-07 の残課題)**: `AddToEscrow`(ディスク)は `WithSession` の外でしか呼べないので、預け入れと `Double()` の間に一瞬だけ隙間がある。同じ盤面の別押下は `boardLocks` が止めるが、**C2-08 の Sweep だけ**はこの隙間に割り込める(割り込むと 2 枚目の掛け金が配当に数えられない)。C2-08 で Sweep を書くときに、掃除対象から「今まさに押下中の盤面」を外すか、隙間を許容するかを決めること。
- **ダブルの可否表示は残高を読む(C2-07)**: `affordsDouble` は `board.CanDouble` が真のときだけ `ViewAccount` を叩く(ヒットのたびに無駄な書き込みをしない)。読めなかったら **disabled** にする — 払えないダブルを出す方が高くつく。押下側の拒否は `AddToEscrow` の `ErrInsufficientChips` が本体で、disabled は表示の判断でしかない。
- **開始の順序は「セッション → 預け入れ」(C2-06 で確定)**: `TASKS.md` の done-when は `OpenGame → Open` と書いてあるが、C2-05 の Notes の決定(セッションが先)を採った。逆順だと 2 つ目のゲームを断るときに預けたチップを戻す経路が要る。セッションを先に開けば、`OpenGame` が断ったときは `Close` するだけで済む(チップは動いていない)。
- **盤面が Discord に届かなかったときは即返金(C2-06)**: 応答が失敗した `/highlow` は押せるボタンが存在しないので、その預かりは二度と決着しない。`Close` + `SettleGame(payout = bet)` でその場で返す(起動時の `RefundStaleEscrows` を待たない)。
- **`MessageRef` は応答後に埋める(C2-06)**: メッセージ ID はこの応答そのものなので `Open` の時点では知りえない。`rememberBoardMessage` が `InteractionResponse` で引き、`WithSession` の中(= ロック内)で `Ref` に書く。「`Ref` は Open 後に不変」の唯一の例外。失敗しても C2-08 の掃除人が編集できなくなるだけなのでログのみ。
- **盤面の読み取りは必ずスナップショット越し(C2-06)**: `Get` が返すコピーの `State` は共有の盤面を指す。`snapshotHighLow` を `WithSession` の中で撮り、表示関数は全部その値型 (`highLowBoard`) だけを受ける — 描画のためにロック外でゲームを触る道を残さない。
- **ボタン応答は `InteractionResponseUpdateMessage`(C2-06)**: 元メッセージの編集を別 API 呼び出しにしない。1 回の応答で embed とボタンを差し替えるので、「精算 → 編集」の順序が 2 つの Discord 呼び出しに割れない。
- **`respondVia` と `interactionResponder`(C2-06)**: `respond` は `*discordgo.Session` を取るので、順序を記録する fake を挿せない。`casino_shared.go` にインターフェース版を置き、`respond` はその薄い包みにした。`respond_test.go` のソース走査(`s.InteractionRespond(` 禁止)はそのまま効く。
- **上限の定数は commands 側にも写しがある(C2-06)**: 連勝 10 とポット 100 倍は `internal/casino` の非公開定数。結果メッセージの文言のためだけに `highLowStreakLimit` / `highLowPotCapMultiple` を commands 側に置いた(規則の所有者は casino のまま)。§6 を変えるときは 2 箇所。
- **セッションの二重決着を止める 3 枚(C2-05)**: (1) `WithSession` が `done` を返した瞬間に両マップから消す、(2) `Sweep` が削除と期限判定を同じクリティカルセクションでやるので同じ盤面は 1 度しか返らない、(3) それでも漏れたら `SettleGame` の `ErrNoGameInProgress`。`Close` が bool を返すのも同じ理由 — 「自分が閉じた」と「既に誰かが閉じていた」を呼び出し側が区別できないと二重に払う。
- **`WithSession` の再入禁止(危険地帯)**: `SessionManager.mu` は非再入で、`fn` の実行中ずっと握られる。`fn` からマネージャのメソッドを呼ばない・Discord も disk I/O も sleep もしない(プロセス内の全ボタン押下が待つ)。永続化(`Store.SettleGame`)は `WithSession` を**抜けてから**。`Store.Update` の同名契約と 2 つのロックが入れ子になるので、順序は常に「セッション → 抜ける → ストア」。
- **`Get` はコピーを返す(C2-05)**: 所有者検査が `GuildID`/`UserID` を読むためだけにマネージャのロックを取らずに済ませるため。ただしコピーの `State` は**共有の盤面を指す**ので、盤面の読み書きは `WithSession` の中だけ。
- **`fn` のエラーは盤面を消さない(C2-05)**: 却下された手(例: `ErrDoubleUnavailable`)で盤面が消えると、預かりが残ったまま操作先が消える。消えるのは `done == true` のときだけ。
- **`Open` は預け入れより先(C2-05)**: 「1人1ゲーム」はセッション側(`ErrGameInProgress`)と口座側(`Store.OpenGame` の `ErrGameInProgress`)の二重で守る。コマンド層は**セッションを開いてからチップを預ける**。逆順にすると、2 つ目のゲームを断るときに預けたチップを戻す経路が要る。
- **TTL 判定は境界を含む(C2-05)**: `now.Sub(LastActionAt) >= ttl` で外す。30 秒周期の Sweep goroutine(C2-08)なので、ちょうど 3 分の盤面を次の周回まで生かす理由が無い。
- **並行テストの効き目を確かめた(C2-05)**: この PC は cgo 無しで `-race` が使えないため、`WithSession` からロックを外した木で並行テストを走らせ 5/5 で落ちる(96〜98 件しか適用されない)ことを確認した(`verify-C2-05-3-mutation.txt`)。ロック付きのテストが緑なだけでは「ロックが効いている」証拠にならない。
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

## 反復 6(C3B-10) — 2026-09-15

- **Done**: C3B-10。`internal/commands` 側に月末の送信失敗を配線ごと通す回帰テスト
  `TestStartAnnounceScheduler_AFailedSeasonResultSurvivesTheMonthBoundary` を足した。
  7/31(embed 成功・結果送信失敗)→ 8/1(7 月が 6 月の上に閉じる)→ 8/2 の 3 巡回を
  `startAnnounceScheduler` で駆動する。ゲート 3 本とも exit=0・全 8 パッケージ ok
  (`.harness/runs/20260915-064206/verify-C3B-10-1..3.txt`)。
- **Next**: C3B-11(duel のゼロサムと全口座を触る経路の説明を実装に合わせる。`TASKS.md` で唯一の未完)。
- **収集側のテストだけでは月末の失敗を押さえられない(反復 4 の所見 1)**: `internal/casino` 側の
  `TestCollectDailyAnnouncements_AMonthRolloverDoesNotDropAnUnannouncedSeason` は
  `collectDailyAnnouncements` を呼ぶだけなので、**送信の失敗**も、**embed の成功が `LastAnnounced` を
  進めること**も通っていない。この 2 つが同時に効く経路 — 7/31 に embed だけ着いて `LastAnnounced` が
  月末に進み、8/1 に `rolloverSeasonLocked` が 7 月を 6 月の上に閉じる — は配線側でしか再現できない。
  収集側のテストにはその旨の相互参照コメントを足した(どちらが何を見ているか、次に読む人が迷わないように)。
- **月替わりを commands 側から起こせる理由**: シーズンの切り替えは `ensureTodayRateLocked(economy, now, ...)`
  → `rolloverSeasonLocked(economy, jstMonth(now))` で、`now` は巡回が渡す時計。`Store` の非公開 `clock` は
  要らない(R-001 の制約に当たらない)ので、`startAnnounceScheduler` に 7/31 → 8/1 を渡すだけで境界を越える。
- **空振りしないことを `LastSeason` で確かめる**: 「6 月の結果が送られた」だけの主張は、配線が境界を
  一度も越えなくても通ってしまう。`SeasonStatus("g1", "observer", 8/1)` の `Last.Month == "2026-07"` を
  足して、7 月が実際に閉じた(= `LastSeason` が上書きされた)ことを同じテストで押さえた。
  `SeasonStatus` が開く観測用の口座は純利 0 なので `SeasonRanks` から除かれ、どの表彰台も汚さない。
- **効き目の確認(mutation)**: `rolloverSeasonLocked` の `queueClosedSeasonLocked` の直前に
  `economy.UnannouncedSeasons = nil`(= C3B-08 以前の「枠は 1 つ」)を入れると、このテストは
  `8/1 delivered [], want the June result` で落ちる(`verify-C3B-10-mutation.txt`)。緑なだけでは
  待ち行列が効いている証拠にならない。

## 反復 7(C3B-11) — 2026-09-15

- **Done**: C3B-11。反復 3 の評価者所見 1・2(どちらも P2・文書のみ)を閉じた。コードは変えていない。
  ゲート 2 本とも exit=0・全 8 パッケージ ok(`.harness/runs/20260915-064206/verify-C3B-11-1..2.txt`)。
  これで `TASKS.md` は 50 件すべて done。
- **Next**: `TASKS.md` に未完は無い。次の作業は M1(新しい仕様の合意)から。
  `blocked/C3B-07.md` の追記案を正本(`MyWorkflow/projects/todayistodayBot/Docs/agent-guide/architecture.md`)へ
  写して再展開するのは**人**の作業で、まだ残っている(`Docs/agent-guide/architecture.md` は今も「6 点」のまま)。
- **duel のゼロサムは上限で崩れる**: `AcceptDuel` は払い出しを `creditChipsCappedLocked` に通すので、
  勝者の残高が `MaxChips` に近いと入り切らない分は切り捨てられ、その回だけ 2 口座の合計が減る。
  切り捨てた分はプールにも敗者にも移らない(宝くじの「他の 1 人へ移る」規則は duel に無い)。
  `SeasonNet` に足すのは表の額ではなく**実際に入った額 − 賭けた額**
  (`TestAcceptDuel_WinnerAtTheCapTakesOnlyWhatFitsAndSeasonNetCountsThat` が 300/-200 で実測)。
  README は「ベット額の 2 倍」の断定をやめ、所持上限で頭打ちになる旨を 1 行足した。
- **全口座を走査する経路は切り替えだけではない**: `SeasonStatus` は月替わりが無くても全口座を
  `normalizeAccountLocked` で正規化して保存し、`RefundStaleEscrows` は全ギルドの対象口座をまとめて返金する。
  危険地帯 7 点目(c)の「全口座を触る唯一の書き込み」「他はすべて 1〜2 口座」は成り立たないので、
  「全口座の `SeasonNet` を一括で 0 にする書き込み」に直し、経路を限定する断定を消した。
  nil 口座を飛ばす・順位を取る前に正規化する、という**守るべき中身の方は変えていない**。
- **設計書の「duel はゼロサム」は `paths:` の外**: `Docs/superpowers/specs/2026-09-15-casino-c3b-design.md:22` は
  設計の契約として無条件のゼロサムを書いている(§2 の実測段落は上限の話と整合)。今回は触っていない。
  次に設計書を開くときに、上限の例外を 1 行足すか判断する。

## 反復 2(C3B-13) — 2026-09-15

- **Done**: C3B-13。`UserAccount` に `EscrowSession`(`json:"escrow_session,omitempty"`)を足し、
  預かりに「どの盤面のものか」の印を持たせた。`SettleGame` / `AddToEscrow` / `AcceptDuel` /
  `DeclineDuel` は渡されたセッション ID と印が一致するときだけ動き、不一致は新しいセンチネル
  `ErrEscrowMismatch` で拒む(`escrowOwnedByLocked`)。ゲート 3 本とも exit=0・全 8 パッケージ ok
  (`.harness/runs/20260915-080323/verify-C3B-13-1..3.txt`)。
- **Next**: C3B-14(未送達時の後始末を盤面ロックの内側で行う。blocking 1、P1)。未完は C3B-14〜17 の 4 件。
- **印は盤面が生まれる前から必要だった(二段階の開始)**: 賭けは盤面より先に動く(C2-06 の完了条件 —
  「一人一ゲーム」の**永続する側**が二度目の賭けを断る側でなければならない)ので、`OpenGame` の
  時点ではまだセッション ID が無い。印を空のままにするとその隙だけ「誰の預かりでもない」= 誰でも
  精算できる状態が残るので、`OpenGame` は `casino.EscrowOpening`(定数 `"opening"`)を書き、盤面が
  できてから `BindEscrowSession` が盤面 ID へ差し替える。セッション ID は 32 桁の hex なので
  `"opening"` と衝突しない = 開始途中の預かりを精算できるのは、その定数を明示して渡す開始側の
  返金だけ。順序を逆(盤面 → 賭け)にする案は採らなかった:
  `TestHighLowStakesBeforeTheBoardAndRefundsWhenTheBoardCannotOpen` と blackjack の同名テストが
  C2-06 の契約としてこの順序を押さえており、`session.go` の `Open` の説明もそれ前提で書かれている。
- **`paths:` の外に 3 ファイル触った(不可避)**: `internal/commands/casino_shared.go` は `casinoBank`
  インターフェースの住所で、`SettleGame` などの引数が増えれば**必ず**直さないとコンパイルが通らない
  (タスクの `paths:` がこの 1 ファイルを落としている)。`internal/commands/balance_test.go` と
  `season_test.go` も同じ理由(`OpenGame` の呼び出しが 1 行ずつ)。どれも引数の追加だけで、判断は
  入っていない。
- **後方互換は「印が空なら通す」= 前向きにだけ効く**: 配備前に開かれた預かりは `escrow_session` を
  持たないので、どの盤面からでも精算できる(`escrowOwnedByLocked`)。ここを厳格にすると、更新を
  またいだ進行中のゲームのチップが全部宙に浮く。印が守るのは C3B-13 以降に開かれた盤面だけ。
- **効き目の確認(mutation)**: `escrowOwnedByLocked` を `return true` に差し替えると、今回足した
  5 件(duel の受諾・取り下げ、H&L/BJ の精算、ダブルの追加、`BindEscrowSession`)がすべて落ちる
  (`verify-C3B-13-mutation.txt`)。緑なだけでは印が効いている証拠にならない。
- **掃除人の扱い**: `sweepIdleBoards` は `ErrEscrowMismatch` を `ErrNoGameInProgress` と同じ
  「この盤面にはもう払うものが無い」として盤面を落とす。再試行を続けても、口座にある預かりは
  別の盤面のものなので永久に払えない。
- **テスト側の下準備も本番と同じ順序にした**: `sweepFixture` は `EscrowOpening` で開いてから
  `BindEscrowSession` で盤面へ渡す。盤面を差し替えていた 2 つのテストは、差し替えをやめて
  `sweepFixtureFor(t, casino.GameBlackjack, ...)` で最初から blackjack の盤面を開く
  (呼ぶ人がいなくなった `dropFixtureBoard` は削除した)。
- **評価者未実施**: 危険地帯の変更なので、区切りのレビューでは `AcceptDuel` / `SettleGame` の
  再入(`Update` のクロージャ内から公開メソッドを呼んでいないこと)と、`EscrowOpening` の隙間に
  誰も入れないことを重点に見てほしい。

## 反復 3(C3B-14) — 2026-09-15

- **Done**: C3B-14。未送達の挑戦(初回 `InteractionRespond` が失敗した `/duel`)の取り下げを
  `withdrawUndeliveredChallenge` として切り出し、⚔️・🚫 と**同じ per-board ロックと `Hold`** の
  内側で走らせ、`DeclineDuel` が**成功したときだけ** `Close` するようにした。ゲート 3 本とも
  exit=0・全 8 パッケージ ok(`.harness/runs/20260915-080323/verify-C3B-14-{1,2,3}.txt`)。
- **Next**: C3B-15(挑戦の開始時に受け手の進行中ゲームも断る。blocking 4、P2)。未完は C3B-15〜17 の 3 件。
- **順序が本体、ロックは副次だった**: 2 つの欠陥のうち実際に**チップを取り残す**のは (b) の順序だけ。
  (a) の競合は C3B-13 の印のおかげで誤精算にはならない — 受諾と返金はどちらも預かりを奪い合い、
  後から着いた方が `ErrNoGameInProgress` で断られる(預かりそのものが札になっている)。
  一方 (b) は、`Close` してから返金に失敗すると**盤面が消えた状態で預かりだけが残る**: 🚫 も
  掃除人も届かず、再起動の `RefundStaleEscrows` まで口座が「進行中」で固まる。
- **失敗したら盤面を残す = 掃除人が再試行の経路**(C2-10 と同じ形)。`Hold` が失われた盤面は
  「もう他人のもの(受諾済み・辞退済み・掃除済み)」なので**何もしない** — 精算済みの預かりを
  返金してはいけない。返金が `ErrNoGameInProgress` / `ErrEscrowMismatch` で断られた場合も盤面は
  残すが、3 分後の巡回で `sweepIdleBoards` が同じ 2 つを「払うものが無い」として落とすので、
  永久に居座ることはない(最大 3 分、次のゲームは開けない)。
- **`Close` はホールド中でも通る**(`busy` を見ない)。受諾・辞退が既にそうしているのと同じで、
  `Release` は消えた盤面を無視する。順序を「返金 → `Close`」にしても新しい規約は要らなかった。
- **効き目の確認**: 後始末を旧実装(`Close` してから返金、ロック無し)へ戻すと
  `TestDuelUndeliveredChallengeKeepsTheBoardUntilTheRefundLands` が
  `0 challenges are live after a refused refund, want 1` で落ちる
  (`mutation-C3B-14-close-before-refund.txt`)。**もう 1 本の競合テストはこの変異では落ちない** —
  上に書いたとおり印が誤精算を止めるので、旧実装でも金額は合う。あれは「ロックで直列化されている」
  という不変条件の番人であって、欠陥の再現ではない。
- **残っている同型の穴(今回の `paths:` の外)**: `BindEscrowSession` が失敗したときの後始末は
  今も `Close` → 返金の順。ただしこちらは印が `EscrowOpening` のままなので、盤面を残しても
  掃除人の `SettleTimedOutBoard` は盤面 ID で `ErrEscrowMismatch` になり再試行にならない。
  `NEXT_FINDINGS.md` の反復 2 所見 1(紐付け前の掃除)と同じ根で、直すなら
  「紐付けが終わるまで掃除から守る」側。タスクとしてはまだ起きていない。

## 反復 4(C3B-15) — 2026-09-15

- **Done**: C3B-15。`/duel` の開始が受け手の預かりも見るようにした。読み取りは
  `casino.Store.GameInProgress`(`Snapshot` 1 回)で、口座を**作らない** —
  `ensureAccountLocked` を通すと「他人の挑戦で名前を呼ばれた」だけの人に 1,000 枚の
  ウェルカムボーナスが出る。ゲート 3 本とも exit=0・全 8 パッケージ ok
  (`.harness/runs/20260915-080323/verify-C3B-15-{1,2,3}.txt`)。
- **Next**: C3B-16(掲示済みの記録に失敗したときの再送を減らし契約を明記する。blocking 5、P2)。
  未完は C3B-16・C3B-17 の 2 件。
- **開始時の検査は保証ではなく礼儀**: `OpenGame` が見るのは**自分が引き落とす口座**だけなので、
  受け手の進行中は ⚔️ が押されるまで誰も見ていなかった。挑戦が立ち、挑戦者のチップが
  3 分間預かりに入り、`AcceptDuel` が断って終わる — 最初から始まりようがなかったゲームのために。
  ただし開始時の読み取りは**保証にはなれない**(3 分の間に相手が別のゲームを開ける)ので、
  規則を守っているのは今も `AcceptDuel` のロック内の再検査のほう。今回足したのは「無駄な挑戦」を
  「その場の拒否」に変える一段だけで、受諾側の検査は残してテストで固定した。
- **文言から「先に決着してください」を落とした**: 既存の `duelInProgressMessage` をそのまま流用すると、
  何も悪いことをしていない挑戦者に向かって「先に決着してください」と言うことになる(決着させられるのは
  相手の手札で、挑戦者には触れない)。`duelOpponentInProgressFormat = "❌ <@%s> は進行中のゲームがあります"`
  として相手を名指しするだけにした。
- **読み取りエラーは開始を断る**: `GameInProgress` が失敗したら `duelStartFailedMessage` で止める。
  同じファイルを読む `OpenGame` の `Update` もどのみち失敗するので、先に断るほうが預かりを触らない。
- **効き目の確認(mutation)**: 2 つの検査をそれぞれ外すと、対応するテストだけが落ちる
  (`mutation-C3B-15.txt`)。開始時の検査を外すと `TestDuelRefusesAnOpponentWhoIsAlreadyPlaying` が
  「挑戦者 900/100・盤面 1 枚」= まさに直した欠陥の形で落ち、`AcceptDuel` の
  `opponent.Escrow > 0` を消すと `TestDuelAcceptStillRefusesAnOpponentWhoStartedAGameMeanwhile` が
  「両者 0 預かり・挑戦者 1100 枚」= 進行中の相手の口座を巻き込んで決着した形で落ちる。
- **`paths:` の外に 1 ファイル(不可避)**: `internal/commands/casino_shared.go` の `casinoBank` に
  `GameInProgress` を足さないとコマンド層から呼べない。テストの偽物(`flakyBank`)は `*casino.Store` を
  埋め込んでいるので、そちらの追随は不要だった。
- **`NEXT_FINDINGS.md` の未コミットの空化は触っていない**: セッション開始時点で既に 62 行が
  消された状態(反復 1・2 の評価者所見)だった。中身は C3B-15〜17 として `TASKS.md` にあり、
  反復 2 所見 1(紐付け前の掃除)は反復 3 の Notes に残っている。今回のタスクの `paths:` 外なので
  判断は次の反復へ回す。

## 反復 6(C3B-17) — 2026-09-15

- **Done**: C3B-17。`Store.Spin` を `creditChipsCappedLocked` に変え、上限で入り切らない配当が
  スピンごと拒否される最後の経路をなくした。`SpinResult` は `Payout`(入った額)と `Owed`(表の額)を
  持ち、`SeasonNet` は入った額で動く。ゲート 3 本とも exit=0・全 8 パッケージ ok
  (`.harness/runs/20260915-080323/verify-C3B-17-{1,2,3}.txt`)。
- **Next**: C3B-18(預かりの印を開始前に確定させる。危険地帯)。未完は C3B-18・C3B-19・C3B-20 の 3 件。
- **ジャックポットの判断: 入り切らなかった分はプールへ戻す**(タスクが実装者に委ねた点)。7️⃣7️⃣7️⃣ は
  `economy.Jackpot = JackpotSeed` でプールをリセットするので、溢れた分を捨てると**積み立ての実在の
  チップごと消える** — 賞与や配当表の分は払う瞬間に発行されるので「発行しない」で済むが、プールは
  既にあるチップで、消せば保存則の穴になる。宝くじ(`drawLotteryLocked`)が余りをプールへ送るのと
  同じ形にした。設計書 §2 の「捨てる」はそのまま生きていて、捨てるのは配当表の分だけ。
- **戻すのは `min(入り切らなかった額, JackpotWon)`**。配当表の分を先に入れるものとして数える
  (`SpinResult.JackpotWon` の説明が「配当表の上に乗せて払う」と書いている)。全額を戻すと、
  払われなかった配当表の分——ハウスが発行しなかっただけのチップ——がプールで**新しく湧く**。
  上限手前 2,000 の口座が 7️⃣7️⃣7️⃣(表 1,960 + プール 5,000)を引くと、入るのは 2,000、
  プールへ戻るのは 4,960、捨てられるのは 0。
- **`JackpotPool` を読む位置を戻しの後ろへ移した**。前のままだと画面には `JackpotSeed` = 1,000 が出て、
  次のスピンが遊ぶプール(6,000)と食い違う。
- **効き目の確認(mutation)**: 3 つの変異がそれぞれ対応するテストで落ちる(`mutation-C3B-17.txt`)。
  `SeasonNet` を `Owed` から数えると 2 本が「60/6950 want 30/1990」で落ち、戻す額の `JackpotWon` 上限を
  外すと `7920 want 6000`、戻しごと消すと `1000 want 6000/5960` で落ちる。
- **`slot.go` の `ErrChipCapExceeded` の分岐を消した**(到達不能になったため)。`Spin` は
  `ErrBetOutOfRange` と `*ErrInsufficientChips` だけを返す。
- **表示の穴を C3B-20 として切った**: `JackpotWon` は「払うはずだった額」になったので、上限手前の
  口座が当たると結果行の「40枚 獲得」と祝いの「5,000 チップ 獲得」が食い違う。金額の動きは正しく、
  文面だけの話なので P3。
- **`NEXT_FINDINGS.md` の空化を戻した**: 反復 5 までに見出しごと消えていたが、中身は所見ではなく
  雛形の説明文だけだったので `git checkout` で復元した(コミットには含めていない)。

## 反復 7(C3B-18) — 2026-09-15

- **Done**: C3B-18。`casino.NewSessionID()` を公開し、H&L・BJ・duel の 3 経路とも**盤面 ID を先に作って**
  `OpenGame(..., sessionID)` と `SessionManager.OpenWithID(sessionID, ...)` へ同じ ID を渡す形に揃えた。
  `EscrowOpening` と `BindEscrowSession` は削除。掃除人は `ErrEscrowMismatch` で盤面を落とさなくなった。
  ゲート 4 本とも exit=0・全 8 パッケージ ok(`.harness/runs/20260915-080323/verify-C3B-18-{1,2,3,4}.txt`)。
- **Next**: C3B-19(`MarkAnnounced` にも同じ再試行)。未完は C3B-19・C3B-20 の 2 件。
- **隙間を塞ぐのではなく無くした**: 2 段階(`OpenGame(EscrowOpening)` → `BindEscrowSession(session.ID)`)の
  あいだ、預かりは**どの盤面も名乗れない印**を持っていた。掃除がそこに挟まると精算が `ErrEscrowMismatch` に
  なり、旧コードは盤面を落として預かりを取り残した。ID の生成は乱数を引くだけでチップを動かさないので、
  **ステークの前に**引ける — 引いてしまえば最初の書き込みからずっと印は本物の盤面 ID で、隙間が存在しない。
  `crypto/rand` が尽きた場合はまだ何も賭けていないので、断るだけで返金は要らない。
- **`Open` は残した**(ID を自分で作る呼び出し側のまま)。`OpenWithID` が本体で、`Open` は
  `NewSessionID` + `OpenWithID`。既存のテスト 30 箇所が `Open` を使っており、そちらは盤面 ID を
  先に知る必要がない。
- **`ErrSessionIDTaken` を足した**: `OpenWithID` に生きている ID を渡すと拒否する。乱数 16 バイトでは
  起きないが、定数を渡す呼び出し側が**他人の盤面を乗っ取る**(その預かりごと)ことだけは防いでおく。
- **掃除人の 2 つの拒否を分けた**: `ErrNoGameInProgress` は「払うものがもう無い」なので今までどおり
  盤面を落とす(再試行しても永久に見つからず、持ち主が新しいゲームを開けなくなるだけ)。
  `ErrEscrowMismatch` は「**チップの持ち主について食い違っている**」で、落とすとその巡回が既に決めた
  配当ごと捨てたうえ預かりも残る。C2-10 の「精算が成功するまで消さない」に揃え、`slog.Error` を出して
  次の巡回に再試行させる。印が開始前に確定した今、不一致は開始の隙間ではなく**実際の不整合**を意味する。
- **効き目の確認(mutation)**: 3 つの変異がそれぞれ対応するテストで落ちる(`mutation-C3B-18.txt`)。
  掃除人に `Remove` を戻すと `TestSweepKeepsABoardWhoseStakeBelongsToAnotherBoard` が落ち、
  `/highlow` が別 ID で盤面を開くと「staked for X but the board went live as Y」と
  「escrow = 100 after the sweep」= まさに直した欠陥の形で落ち、`OpenWithID` が呼び出し側の ID を
  捨てると 3 ゲームとも同じ形で落ちる。
- **テストの下準備も本番と同じ 1 回書きに直した**: `sweepFixtureFor` と duel の 3 箇所は
  `NewSessionID` → `OpenGame` → `OpenWithID`。`duelBankThatRefusesOneRefund` は
  `BindEscrowSession` ではなく `OpenGame` を包んで盤面 ID を覚える(`bound` → `board`)。
## 反復 8(C3B-P1 → C3B-19) — 2026-09-15

- **Done (先に差し戻し)**: C3B-P1。`DeclineDuel` の拒否を「預かりがそもそも無い(`ErrNoGameInProgress`)」と
  「預かりが他人のもの(`ErrEscrowMismatch`)」の 2 つに割った。前は `Escrow == 0 || EscrowGame != duel` を
  ひとまとめに `ErrNoGameInProgress` にしていたので、**H&L の預かり 100 を抱えた口座の duel 盤面**を掃除人が
  「払うものが無い」と読んで削除していた。回帰 2 本(`TestSweepKeepsADuelWhoseChallengerIsStakedForAnotherGame`、
  既存の H&L 版へ**ログ検査**を追加)。ゲート 4 本とも exit=0
  (`.harness/runs/20260915-080323/verify-C3B-P1-{1,2,3,4}.txt`、`mutation-findings-7.txt`)。
- **Done**: C3B-19。`runAnnouncePass` の `MarkAnnounced` を `markAnnouncedWithRetry` にした。
  3 回・50ms/150ms、3 回とも失敗したら `slog.Warn` で「送信済みだが記録できなかった」と at-least-once を名指しする。
  ゲート 3 本とも exit=0・全 8 パッケージ ok(`.harness/runs/20260915-080323/verify-C3B-19-{1,2,3}.txt`)。
- **Next**: C3B-20(上限で切り詰められたジャックポットを「獲得」と書かない、P3)。未完は C3B-20 の 1 件のみ。

### C3B-P1 — 「預かりが残る可能性がある限り盤面を消さない」に 3 経路とも揃えた

- **同じ状況を 2 通りに扱っていた**。`SettleGame`(H&L・BJ)は `Escrow == 0` と印の不一致を分けていたのに、
  `DeclineDuel` だけが「ゲーム種別が違う」も `ErrNoGameInProgress` に混ぜていた。掃除人はこの 2 つで
  **盤面を落とすか保つか**を決めるので、混ぜた側だけが C2-10 を破っていた。
- **永久に居座る盤面にはならない**。保持した duel 盤面は、そのチップを持つ盤面が精算した時点で
  `Escrow == 0` になり、次の巡回が `ErrNoGameInProgress` で落とす。盤面の滞留自体は新しいゲームを
  塞がない(塞いでいるのは預かりの方で、それは元から)。
- **🚫 ボタンの文面が変わる**のは承知の上。この状態は「開始の隙間」ではなく実際の不整合なので、
  `translateDuelPressError` の default(`slog.Error` + 汎用の失敗文言)に落ちるのが `ErrEscrowMismatch` の
  扱いとして正しい。盤面は残るので、チップは次の巡回が返す。
- **ログ検査を回帰に足した**(差し戻し 2 件目)。`assertSweepLoggedTheStakeMismatch` が
  「`level=ERROR` で盤面 ID を名指す行」を要求する。保持は静かな挙動で、この行だけが運用者への唯一の通知。
- **効き目の確認**: `DeclineDuel` を元の 1 本の条件に戻すと store 側とスイープ側の 2 本が落ち、
  `slog.Error` を消すと H&L・duel の両方が落ちる(`mutation-findings-7.txt`)。

### C3B-19 — 待ちを 2 つに分けた理由と、テストが「重複」を見られるようにした工夫

- **`RunAnnounceScheduler` の署名は変えていない**。再試行の待ちは非公開の `runAnnouncePass` の
  最後の引数(`markRetryWaiter`)として受け、`RunAnnounceScheduler` は本番の
  `realMarkAnnouncedRetryWait` を渡すだけ。`SleepFunc`(翌朝 9 時までの待ち)と同じ関数にすると、
  テストの stub が答えた `false` が「再試行を打ち切った」のか「スケジューラを止めた」のか区別できない。
- **`healIfBroken` が無いと回帰が空振りする**。壊した store は `collectDailyAnnouncements` も落とすので、
  2 巡目が**掲示にたどり着く前に**戻ってしまい、肝心の「もう一度出る」が観測できない。
  壊れているときだけ種ファイルへ戻す(書けている store は触らない — 成功した記録を巻き戻さないため)。
  これを入れて初めて、再試行を外す変異が「the day was posted 2 times」で落ちるようになった。
- **2 巡目は翌日ではなく同じ日**。翌日は翌日で掲示を負っているので、再送の有無を測れない。
  同じ日をもう一度巡回させる(再起動・9 時前後の起床)形にして、`LastAnnounced` が立ったかどうかだけを見る。
- **回数の検査は最後**。`waits != 1` を先に置くと、変異が「仕組みが違う」で落ちて
  「embed と当選祝いがもう一度出る」という**結果**で落ちなくなる。
- **効き目の確認(mutation)**: 3 つの変異がそれぞれ落ちる(`mutation-C3B-19.txt`)。再試行ごと戻すと
  「the day was posted 2 times ... u9」、`markAnnouncedWithRetry` が待ち手を無視すると同じ形、
  `slog.Warn` だけ消すと「no at-least-once warning」。
- **契約は変えていない**。`at-least-once` のまま。3 回とも失敗した後の巡回では embed も u9 への
  メンションも本当にもう一度出ることを `TestRunAnnouncePass_AnUnrecordedAnnouncementWarnsAndIsPostedAgain` が
  そのまま検査している(隠していない)。

## 反復 9(C3B-20) — 2026-09-15

- **Done**: C3B-20。`slotJackpotLine` と `slotCelebrationMessage` が、上限で切り詰められた 7️⃣7️⃣7️⃣ で
  `JackpotWon`(払うはずだった額)を「獲得」と印字するのをやめた。新しい `slotJackpotCredited` が
  `Owed - Payout` から `min(不足額, JackpotWon)` を取り、切り詰められたときだけ受け取った額で書く。
  ゲート 3 本とも exit=0・全 8 パッケージ ok(`.harness/runs/20260915-080323/verify-C3B-20-{1,2,3}.txt`)。
- **Next**: `TASKS.md` に未完なし(`status: todo` は 0 件)。次は M1 へ戻して次の一覧を立てる。

### C3B-20 — 金額は正しく、食い違っていたのは文字列だけ

- **直したのは表示だけ**。`JackpotWon` の意味は変えていない(プールが火を噴いた事実は祝いの条件として要る)。
  口座へ入る額も、溢れた分がプールへ戻る経路も C3B-17 のまま。
- **不足額のうちプールの取り分だけを名指す**。`min(不足額, JackpotWon)` は `Store.Spin` が
  プールへ戻す額と同じ式 — テーブル配当の捨て分まで「プールが届かなかった」と書くと今度は逆向きに嘘になる。
  捨てられたテーブル分は結果行が `Payout` を出す時点で既に正直なので、ジャックポットの 2 行は
  プールを過大に書くのをやめるだけでよい。
- **行き先ではなく「受け取れなかった」と書いた**。`creditJackpotCappedLocked` は `MaxJackpot` で
  自分も頭打ちになりうるので、「プールへ戻りました」は常に真とは限らない。受け取れなかったことは常に真。
- **回帰の数値は実 Store から写した**。`internal/casino/store_test.go` の
  `TestStore_Spin_JackpotAtTheCapReturnsTheUndeliveredPoolToThePool`(MaxChips-30 → `Payout` 40)と
  `..._JackpotPartlyFits_...`(MaxChips-1990 → `Payout` 2000)が固定済みの出力なので、
  手作りの値で自分の思い込みを検査する形を避けられた(`paths:` の都合で commands 側から実 Spin は駆動できない)。
- **通常範囲の fixture に `Owed` を足した**。前は `Owed` 未設定(0)で「たまたま」切り詰め判定に落ちない状態で、
  文面は変わらないが、テストが偶然で通るのをやめさせた。
- **効き目の確認(mutation)**: 3 つとも落ちる(`mutation-C3B-20.txt`)。結果行の切り詰め判定を外すと
  「40枚 獲得」と「+5000 チップ」が並んだまま落ち、祝いの文だけ外すと祝いの文で落ち、
  `min` の丸めを落とすと「1枚も入らなかった」側が落ちる。


## 反復 11(C3B-P3) — 2026-09-15

- **Done**: C3B-P3(差し戻し 2 件)。**盤面は「握られた状態」で生まれる**ようにした
  (`OpenWithID` が `busy: 1` で登録する)。登録 → 預かり → 初回応答の全体が 1 つの操作になり、
  掃除人は `busy` の盤面を対象から外すので、「盤面だけ消えて預かりが残る」窓が構造的に無くなった。
  併せて**ブラックジャックの未送達の手を、返金に失敗しても閉じない**ようにし、
  `MarkRefundPending` で「掃除人が払う額」を預かり額に固定した(自動スタンドではなく返金として再試行)。
  ゲート 4 本とも exit=0・全 8 パッケージ ok
  (`.harness/runs/20260915-114223/verify-C3B-P3-{1,2,3,4}.txt`、`mutation-C3B-P3.txt`)。
- **Next**: `TASKS.md` に未完なし(`status: todo` は 0 件)。危険地帯の変更なので、次は評価者を通す。

### 3 ゲーム × 経路の表 — どの経路が不変条件のどちら側か

不変条件:「掃除人に見える盤面は、(a) まだ資金が動いていない か (b) 預かりの印が付いていて精算・返金の対象になる」。

| 経路 | H&L | BJ | duel |
|---|---|---|---|
| 開始(登録〜預かり) | 掃除人に**見えない**(握り) | 同左 | 同左 |
| 開始(応答後) | (b) 預かりは盤面 ID 付き | (b) | (b) 挑戦者の預かり |
| 受諾 | — | — | (b) `AcceptDuel` は 1 トランザクション。落とすのは預かりが消えているときだけ = (a) |
| 辞退 | — | — | (b) 返金が成功して初めて閉じる |
| 時間切れ | (b) `AutoResolve` → `SettleGame`、精算が載って初めて `Remove` | (b) 自動スタンド → `SettleGame` | (b) `SettleTimedOutBoard` = `DeclineDuel` |
| 未送達(返金成功) | 閉じる(預かり 0) | 閉じる | 閉じる |
| 未送達(返金失敗) | (b) 盤面を残し預かり額を記す | (b) **今回変更** 残して記す | (b) 残す(記さない — 下記) |

- **duel だけ `MarkRefundPending` を呼ばない**。掃除人は登録済みハンドラの `SettleTimedOutBoard` を先に見るので、
  duel の時間切れは初めから `DeclineDuel`(コインを投げない全額返金)。取り除くべき `AutoResolve` がこの経路には無い。
- **H&L の印は冗長**(挙動は変わらない)。未操作の H&L 盤面の `AutoResolve` はポット = 賭け金のキャッシュアウトで、
  数字としては返金と同じ。印を足したのは、同じ約束を 3 ゲームで同じ形にするため — 偶然の一致ではなく
  「返金の失敗は返金として再試行する」と書いてある状態にする。M1〜M3 の変異では H&L の印だけは検出されない。
- **不変条件の外にある 1 本**: 決着済みの手(BJ のナチュラル・各ゲームの最終プレス)は `SettleGame` が失敗すると
  盤面が先に消え、預かりは `lock.pending` + 🔁 ボタン + 再起動時の `RefundStaleEscrows` が持つ。これは C2 の設計
  (決着した盤面は二度と押させない)であり、今回の不変条件は**まだ遊べる盤面**を対象にしている。

### なぜ「開始のあとに `Hold` を取る」では足りないか

- **隙間は Hold を取るその場所にある**。`OpenWithID` の直後に `Hold` を呼ぶ形は、登録と `Hold` の間で
  掃除人に取られうる。取られたら開始を中止すれば安全ではある(まだ資金が動いていないので (a) 側)が、
  それは「呼び出し側が必ず確認する」という約束に依存する。登録と握りを**同じクリティカルセクション**で行えば、
  盤面の一生のどの瞬間にも隙間が無い。`Hold` が取れない場合の扱いは「この経路では失敗しえない」が答えになる
  (ID をまだ誰も見ていない)。
- **代償はテストの churn**。開いたら `Release` するのが契約になったので、盤面を開いてすぐ掃除するテストは
  「開始が終わった」ことを書く必要がある(`session_test.go` の `endOpen`、`sweepFixtureFor` などの `Release`)。
  これは雑音ではなく、テストが開始の終わりを明示するようになったということ。
- **入れ子は数える**。`busy` はカウンタなので、開始の握りの内側で `withdrawUndelivered*` が `Hold` を取っても
  正しく 2 → 1 → 0 と戻る。盤面が途中で閉じられたら `Release` は何もしない(ID は 16 バイトの乱数なので
  別の盤面に当たらない)。

### 「返金に失敗した手」を決着させない仕組み

- **`PayoutResolved` を先に立てるだけ**。掃除人は `!PayoutResolved` のときだけ `AutoResolve` を呼ぶので、
  預かり額を `PendingPayout` に置いて `PayoutResolved` を立てると、その盤面は**ゲームに訊かれない**まま
  その額で精算される。新しい分岐を掃除人に足していない。
- **握っている間しか書けない**。期限切れの盤面のこの 2 つのフィールドは掃除ゴルーチンがロック無しで読むので、
  `MarkRefundPending` は期限切れ・不明な盤面を拒否する。呼び出し側は必ず `Hold` の内側にいて、
  握られた盤面は期限切れにならない。
- **効き目の確認(mutation)**: 3 つとも落ちる(`mutation-C3B-P3.txt`)。`busy: 1` を外すと
  3 ゲームの「開始中の掃除」と manager 側の 1 本が落ち、返金失敗でも閉じる旧実装に戻すと
  「0 hands are live after a refused refund」で落ち、印だけ外すと
  「900 chips ... standing this hand would have paid 0」で落ちる — 最後の 1 本が
  「自動スタンドではなく返金」を数字で示している。


## 反復 12(C3B-P4) — 2026-09-15

- **Done**: C3B-P4(差し戻し 1 件 + 系統の構造化)。盤面が manager から出る道を
  `closeBoardIfSettled`(`casino_shared.go`)**1 本**にし、判定を**口座**に移した —
  「預かりの印がこの盤面を指している間は閉じない」だけを見て、ゲームの状態は一切見ない。
  `internal/commands` の直接 `sessions.Close(` 10 か所を全てこの入口へ置き換え、
  **ソース走査テスト**(`TestOnlyOneEntryPointClosesABoard`)で新しい経路が直接閉じたら落ちるようにした。
  併せて**配札時決着(ナチュラル)を払う前に閉じる**のをやめた — 精算と初回応答が両方失敗したとき、
  預かりが盤面も 🔁 ボタンも無いまま残っていた最後の 1 本。
  ゲート 4 本とも exit=0・全 8 パッケージ ok
  (`.harness/runs/20260915-120429/verify-C3B-P4-{1,2,3,4}.txt`、`mutation-C3B-P4.txt`)。
- **Next**: 反復 1 の差し戻し 2「返金待ちにした盤面をその後もプレイできる」を `C3B-P5` として切った。
  危険地帯の変更なので、次は評価者を通す。

### 3 ゲーム × 経路の表 — どの経路が不変条件のどちら側か

不変条件:「盤面が閉じられるのは、(a) その口座で資金が動いていない か
(b) 預かりの印がこの盤面を指していない(= 既に精算・返金が載った)ときだけ」。
判定は 1 か所(`closeBoardIfSettled`)にあり、下の表はその 1 つの式の帰結。

**盤面を消す経路は 1 本しかない。** `closeBoardIfSettled` が `casino.SessionManager` の
`Close` / `Remove` を呼ぶ 2 行が、このプロセスで盤面がマネージャから消える唯一の場所である
(構文木走査 `TestOnlyOneEntryPointRetiresABoard` が他のファイルからの呼び出しを落とす)。
`WithSession` は**何を返しても盤面を消さない**。

| 経路 | H&L | BJ | duel |
|---|---|---|---|
| 開始(登録〜預かり) | 握り中で掃除人に見えない。`OpenGame` 失敗は (a) | 同左 | 同左 |
| 開始(応答成功) | (b) 預かりは盤面 ID 付き。閉じない | 同左 | 同左(挑戦者の預かり) |
| 未送達(返金成功) | (b) 印が消えたので入口が閉じる | 同左 | 同左 |
| 未送達(返金失敗) | (b) 印が残るので入口が拒否。盤面 + `MarkRefundPending` | 同左 | 盤面を残す(印は不要 — 下記) |
| 受諾 | — | — | (b) `AcceptDuel` は 1 トランザクション。成功で印が消えて閉じる/失敗は印が残って閉じない |
| 辞退 | — | — | (b) `DeclineDuel` が載ってから閉じる |
| 決着(押下) | (b) **C3B-X1 で変更** 払ってから閉じる。失敗なら盤面が残り掃除人が払う | 同左 | 同左(受諾・辞退) |
| 決着(配札) | — | (b) 払ってから閉じる。失敗なら盤面が残り掃除人が払う | — |
| 時間切れ | 掃除人も**入口を通る**(C3B-X1)。精算が載って初めて外す | 同左 | 同左(`SettleTimedOutBoard`) |

- **「何も預けていないので閉じてよい」は呼び出し側の信念**だった。5 回の欠陥は全て、その信念が
  「書いた経路では真、隣の経路では偽」だったもの。入口は信念を受け取らず口座を読むので、
  経路が増えても判定が増えない。
- **`duelAcceptDropsTheChallenge` を消した**。「どのエラーなら盤面を落としてよいか」という
  エラー分類そのものが信念の言い換えで、入口が口座を読むなら不要になる。
  `ErrNoGameInProgress` は「預かりが無い」= 入口が閉じる、書き込み前の I/O 失敗や
  相手が対局中は「預かりが残る」= 入口が閉じない、と同じ結論に自動的になる。
- **印の一致は厳密**(`EscrowHeldBy`)。精算側の `escrowOwnedByLocked` は印が空なら通す
  (C-3b 以前の預かりを取り残さないため)が、入口は逆に倒す — 印の無い預かりはこの盤面のものでは
  ありえない(C-3b 以降の預かりは最初の書き込みから盤面を名乗る)ので、そこで盤面を残すと
  **誰にも解けない盤面**ができる。2 つは別の問いに答えている:「払ってよいか」と「落として大丈夫か」。
- **期限切れの盤面も入口を通る**(C3B-X1)。掃除人のものであることは変わらないが、掃除人が
  それを外すときも口座を先に読む — `Exists` は期限切れを見せ、`Close` が期限切れを拒否した先で
  `Remove` に落ちる。掃除人が「解けない盤面」を落としていた 1 経路(`errBoardCannotResolve`)は、
  これで預かりを取り残さなくなった代わりに、毎回 loud に鳴り続ける。
- **読めない口座は「閉じてよい」の証拠ではない**。`EscrowHeldBy` がエラーなら盤面を残す。
  最悪でも 3 分後の掃除で解ける — 再起動まで固まるのとは違う。

### 配札時決着を「払ってから閉じる」にできる理由

- **開始の握りが全体を覆っている**。`handle` は `OpenWithID` が返した握りを `defer Release` まで
  持っているので、`settleDealtHand` の間は掃除人がこの手を取れない。閉じるのを後ろへ回しても
  二重決着の窓は開かない(押下側は盤面ロックで直列化されている)。
- **🔁 の再試行も入口を通る**。`handleComponent` の `lock.pending` 分岐は精算のあと入口を呼ぶので、
  配札時決着で立ったままの手は、払えた押下が閉じる。既に居ない手には入口が何もしない。
- **印(`MarkRefundPending`)は冗長**。BJ の `AutoResolve` は決着済みの手ではディーラーを引かず
  `Settle()` を返すので、掃除人が再導出しても同じ数字になる(M3 は落ちない)。
  それでも書くのは、**決着は配札で決まっている = カードに二度訊かない**と宣言しておくため
  (C3B-P3 の H&L の印と同じ扱い)。
- **テストの fixture を 1 つ直した**。`TestDuelAcceptWithoutAStakeDropsTheChallenge` は
  `ErrNoGameInProgress` を返す偽物の銀行を使いながら預かりは口座に残したままだった。
  入口が口座を読むようになったので、この状態はストアが作れない。実際に預かりを精算してから
  押させる形に変え、本物の `AcceptDuel` が同じエラーを返すようにした(テストの弱体化ではなく、
  fixture が嘘をやめた)。
- **効き目の確認(mutation)**: M1(預かりを見ずに必ず閉じる)は BJ の回帰・入口の単体・duel の
  3 本が落ち、M2(旧順序へ戻す)は回帰とソース走査が同時に落ちる(`mutation-C3B-P4.txt`)。


## 反復 13(C3B-P5) — 2026-09-15

- **Done**: C3B-P5(反復 1 の差し戻し 2)。**返金待ちを「ゲーム操作できない状態」にした。**
  `MarkRefundPending` は掃除人が払う額を固定するだけで、盤面はその後も押せていた — 返金に失敗した
  H&L でボタンを押すとポットが 101 に育つのに、掃除人は固定した 100 しか払わない。判定は
  `casino.Session.RefundPending()` **1 つ**(`PayoutResolved` の読み取り)にまとめ、3 ゲームの
  `handleComponent` がそれを訊いてから手を進める。答えは既存の `casinoSettleFailedMessage`
  (「精算に失敗しました。もう一度お試しください。」)— プレイヤーから見れば返金も精算も
  「チップがまだ動いていない、再試行中」で同じ。
  ゲート 4 本とも exit=0・全 8 パッケージ ok
  (`.harness/runs/20260915-120429/verify-C3B-P5-{1,2,3,4}.txt`、`mutation-C3B-P5.txt`)。
- **Next**: `NEXT_FINDINGS.md` に残る C3B-P4 の差し戻し 2 件(押下決着が共通入口を通らない/
  ソース走査が複数行呼び出しを見逃す)= `TASKS.md` の `C3B-X1`。
- **Notes**: **作業ツリーに反復ランナーが 2 本走っていた**(`blocked/C3B-P5.md` に相手側の診断)。
  同じ 3 ファイルを交互に書いていたため、こちらの編集途中の内容が相手のコミット `e701563
  「作業途中の保存」` に取り込まれている。**再開は 1 本だけにすること。**

### なぜ「押せなくする」が正しく、「額を固定する」だけでは足りなかったか

- **固定した額は、盤面が動くと嘘になる**。`MarkRefundPending` は「掃除人はこの額を払え」としか
  言っていない。盤面はメモリの上で自由に動けるので、押下でポットが 101 になっても掃除人は 100 を払う。
  預けた側から見れば、目の前の盤面が約束した額より少ない。**額を固定するなら、盤面も止めなければ
  意味の対にならない。**
- **判定は口座ではなく盤面の印を読む**(C3B-P4 の入口とは別の問い)。入口は「落としてよいか」を
  口座に訊く。こちらは「まだ遊んでよいか」を盤面に訊く。前者は預かりが残っていれば閉じない、
  後者は返金待ちなら押させない — 同じ盤面でも答えが逆になる場面(返金待ちは「閉じない」かつ
  「押せない」)があるので、1 つの述語にまとめてはいけない。
- **掃除人からは見えたまま**。`Sweep` / `Remove` / `closeBoardIfSettled` は一切触っていない。
  拒否したのは押下と追加預かりだけで、払うのは掃除人という役割分担は変えていない。
- **`AddToEscrow` は press の gate とは別に、書き込みの直前でも訊く**。BJ の ⏫ は
  「盤面へ訊く → 口座へ書く → 盤面へ適用する」の順なので、口座へ書く手前(`stakeDouble` の
  `WithSession` 内)で**生きた盤面**を読む。押下の gate は Hold の複製を読むので、二重に見える
  この 2 つは読んでいる対象が違う。
- **`/duel` にも印を書くようにした**。評価者の指摘どおり、`withdrawUndeliveredChallenge` が
  印を書かないままだと `/duel` の gate は永久に false を読む。`/duel` は掃除人が
  `SettleTimedOutBoard`(= 同じ `DeclineDuel`)で払うので**額は要らない**が、印のもう半分
  (「これはもう押せる盤面ではない」)は要る。`settleSweptBoard` は settler を先に見るので、
  `PendingPayout` を書いても掃除の経路は変わらない。
- **`/duel` は被害が 2 人に及ぶ唯一のゲーム**。返金待ちの挑戦を ⚔️ で受けられると、**相手の** 100 枚も
  新たに預かって、既に返金へ向かっているチップでコインを投げることになる。
- **効き目の確認(mutation)**: 6 つとも落ちる(`mutation-C3B-P5.txt`)。M1(H&L の gate を外す)は
  評価者の反例をそのまま再現し「`pot = 101 after the presses, want the bet (100)`」で落ちる。
  M2(BJ の押下 gate だけ外す)では ⏫ は `stakeDouble` の番人に止められ、hit / stand だけが落ちる —
  2 段の番人がそれぞれ別に効いていることの数字。M3(両方外す)で ⏫ も落ち、M4(duel の gate)、
  M5(`RefundPending` を常に false)、M6(duel の印を書かない)も落ちる。


## 反復 14(C3B-X1) — 2026-09-15

- **Done**: C3B-X1(C3B-P4 の差し戻し)。**盤面を消す機構を 2 つから 1 つに減らした。**
  C3B-P4 は `sessions.Close` の呼び出しを 1 か所に絞ったが、**扉はもう 1 つ開いていた** —
  `WithSession` のコールバックが `done=true` を返すと、マネージャが移動を適用したその同じロックの
  中で盤面を外していた。押下で決着した手はそこを通るので、**口座を一度も読まずに**盤面が消え、
  直後の精算が拒否されると預かりは「どの盤面も名乗らないチップ」になった — 🔁 はこのプロセスの
  メモリにしか無く、掃除人はマネージャに残っている盤面しか見ないので、再起動まで誰も解けない。
  - `WithSession` の戻り値を `error` だけにした。移行漏れはコンパイラが 9 か所すべて指した。
  - 押下による決着(H&L のキャッシュアウト/最後の一手、BJ の hit / stand / ⏫)は
    「盤面を保持したまま精算 → 成功したら入口で閉じる」へ。失敗したら盤面と確定配当が残る。
    `/duel` は元から `WithSession` の外で精算していたので、この形は既に満たしていた。
  - **掃除人も入口を通す**。`mgr.Remove` の 3 か所を `closeBoardIfSettled` にした。
  - ソース走査を `go/parser` + `go/ast` に。受信側の名前は推測せず、各ファイルが
    `*casino.SessionManager` として**宣言**した識別子を訊く。
  ゲート 4 本とも exit=0・全 8 パッケージ ok
  (`.harness/runs/20260915-120429/verify-C3B-X1-{1,2,3,4}.txt`、`mutation-C3B-X1.txt`)。
- **Next**: `TASKS.md` に未完なし。
- **Notes**: **また同じ作業ツリーでランナーが 2 本走った**(`blocked/C3B-X1.md` = run-id
  `20260915-123431` 側の診断。相手は 1 バイトも書かずに止まった)。こちらの実装中の差分が
  ハーネスのコミット `406f99a「作業途中の保存」` に取り込まれており、**実装ファイルはそこに載っている**。
  その中途半端な HEAD に対して評価者が走ったのが `NEXT_FINDINGS.md` の「反復 3」節で、
  指摘 4 点(テスト移行漏れ・構文木走査未実装・回帰 3 本と経路表・証拠)は本コミットで全て閉じた。
  **再開は 1 本だけにすること。**

### なぜ「扉を塞ぐ」ではなく「扉を 1 枚にする」だったか

- **塞いだ扉の隣にもう 1 枚あると、直した経路の数だけ安心して終わる**。C3B-P4 までの 5 回は
  どれも「見つかった経路を直す」で、そのたびに隣の経路が同じ穴を開けていた。C3B-P4 は
  呼び出し側を全部塞いだが、マネージャ自身が持っていた 2 枚目には手を付けていない。
  今回消したのはコードではなく**機構**で、`done` という戻り値そのものを無くした。
- **移行漏れをコンパイラに探させた**。`WithSession` の型を変えると呼び出し側が全部落ちる。
  「探して直す」ではなく「直さないとビルドが通らない」に変えたので、見落としが原理的に無い。
- **精算失敗のとき、確定配当を盤面へ書くのは必須**(選択ではない)。H&L の `AutoResolve` は
  `CashOut` で、キャッシュアウト済みの盤面は `HighLowPlaying` ではないので **0 を返す**。
  盤面を残しただけで印を書かなければ、掃除人はポットではなく 0 を払い、預かりは戻らずに消える。
  回帰テストの `900 != 1000` はこの数字。
- **掃除人を入口へ通したのは締め付け**。`errBoardCannotResolve`(解決できない盤面)は今まで
  無条件に落としていた — つまり **この欠陥系統そのもの**を掃除人自身がやっていた。口座を読むように
  したので預かりは取り残されないが、代わりに毎回エラーを吐き続ける。到達不能な分岐なので
  「静かに取り残す」より「鳴り続ける」が正しい。
- **行単位の走査は formatter が安定して壊せる**。`c.sessions.` の次行に `Close(sessionID)` を
  置くと gofmt はそのまま残し、どの 1 行も両方を含まない。構文木にすれば改行は組み立ての時点で
  消えている。`TestTheBoardScanSeesACallSplitAcrossLines` はその形を実際に食わせ、
  「行単位なら見逃す」ことも同じテストの中で示している。
- **効き目の確認(mutation)**: 6 つとも落ちる(`mutation-C3B-X1.txt`)。M1(H&L の印を書かない)は
  回帰が `900 chips ... want 1000` で落ち、M2(評価者が書いた複数行の `Close`)と
  M3(掃除人の `mgr.Remove` を戻す)は走査が行番号付きで落とす — M2 は旧・行単位の走査では
  落ちなかった形である。M4 / M5(押下後に入口を呼ばない)、M6(口座を読まずに落とす)も落ちる。

## 反復 15(C3B-X2) — 2026-09-15

- **Done**: C3B-X2(返金待ちの盤面を押したときの案内を実態に合わせる)。`NEXT_FINDINGS.md` の
  「反復 2」節(C3B-P5 の差し戻し、拒否時の案内)を閉じた。
  **変えたのは文字列 1 本と、それを返す 4 か所だけ。資金の経路には触っていない。**
  `casino_shared.go` に `casinoRefundPendingMessage`
  (`⏳ この盤面は返金の再試行を待っています。完了すると自動でチップが戻ります。`)を足し、
  H&L・BJ の押下の門(`session.RefundPending()`)、duel の同じ門、`translateBlackjackPressError` の
  `casino.ErrRefundPending` の変換 — 計 4 か所をこれに替えた。
  ゲート 3 本とも exit=0・全 8 パッケージ ok
  (`.harness/runs/20260915-131233/verify-C3B-X2-{1,2,3}.txt`、変異後の復元確認が `-4`)。
- **Next**: `NEXT_FINDINGS.md` に残るのは C3B-X1 の構文木走査についての 2 件(反復 3・反復 4。
  同じ指摘 — 別ファイルで宣言されたフィールド経由・`:=` 経由の削除呼び出しを走査が見逃す)。
  `TASKS.md` にはこれに対応する未完タスクがまだ無いので、次は M1 へ戻して切るか、走査を
  `go/types` でパッケージ全体から解く形に直すか。
- **Notes**: `gofmt -l internal/commands/` が `highlow_test.go`・`blackjack_test.go`(節見出しの前の空行)と
  `duel_test.go`(末尾改行なし)を挙げる。**いずれも HEAD の時点で既にそうで、今回の変更とは無関係**
  (`git show HEAD:… | gofmt -d` で確認)。ゲートには `gofmt` が入っていないので緑のままだが、次に
  これらのファイルを触る反復が一緒に直すと安い。

### 「精算失敗」と「返金待ち」は、利用者にとって別の状態だった

- **同じ文字列を使い回していたのが誤りの本体**。`casinoSettleFailedMessage` は
  🔁 ボタンの**下に出る**文言で、「もう一度お試しください」は**押せば再試行が走る**から真だった。
  一方 `RefundPending` で断られた盤面には**その 🔁 が無い** — `boardLock.pending` を持っていないから
  押下は再試行の経路へ入らず、門で断られて終わる。つまり同じ文が、押すところのない利用者に
  「押せ」と言っていた。**何度押しても何も起きない**のが指摘の再現そのもの。
- **「待てば戻る」は本当に真か**を先に確かめてから書いた。この門に届くのは実際には**返金**の経路だけで
  (`highlow.go:506` / `blackjack.go:504` / `duel.go:550` — 未送達・不成立の後始末が返金に失敗した盤面)、
  配当の失敗は `lock.pending` を持つので押下が再試行へ入る(`boardLocks.release` は
  `pending == nil` のときしか記録を捨てない)。そして 3 ゲームの既存の回帰テストは、どれも
  **3 分後の掃除で 1000 / 0 に戻る**ところまで検査している。だから「完了すると自動で戻る」と書ける。
  配当(勝ち額)が絡む状態がこの門に届くなら「返金」は嘘になるが、届かないことを上の経路で確認した。
- **効き目の確認(mutation、`mutation-C3B-X2.txt`)**: 3 つとも落ちる。
  M1(2 つの定数を同じ値にする = C3B-X2 以前)は**共有テストだけ**が落ちる — 各ゲームのテストは
  定数と比べているので、定数が同一なら通ってしまう。**だから distinctness は別テストで持つ**
  (`TestTheRefundPendingLineTellsThePlayerToWaitRatherThanRetry`。「もう一度」「再度」「やり直」を
  含まないことも見る)。M2(H&L・duel の門を旧文言へ)、M3(BJ の門 2 か所を旧文言へ)は
  各ゲームの回帰が実際の文字列付きで落とす。**役割が違う 2 本が要る**ことがここで出ている。
