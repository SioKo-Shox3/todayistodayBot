# NEXT_FINDINGS — 次の反復がタスクより先に処理する

評価者(Astra, `codex exec -m gpt-6-astra -s read-only`)の `NEEDS_WORK` を落とす場所。
処理したら該当節を消す。全文: `.harness/runs/20260914-222248/astra-C3-07.txt`。

## C3-07 の区切り評価(2026-09-14, 対象コミット 58b721b)= NEEDS_WORK

### 1. [P2] プールへの加算に桁あふれ検査が無い(`internal/casino/store.go` 1100)

`economy.Jackpot += house + (prize - paid)` は上限を見ていない。反例はどれも**ハンド編集された JSON** が要る
(`MaxChips` は `math.MaxInt64` よりはるかに小さいので、通常の遊びでは到達しない)が、C3-07 は加算する額を
`house` から `house + 残額` へ増やしたので、あふれるまでの余裕はこの変更のぶん狭くなっている。

- `Jackpot = MaxInt64-10`・売上 100・繰り越し 0・当選者が `MaxChips-50`: 賞金 90 / 入金 50 なので 50 を足して**負値**になる。
  変更前(10 を足す)なら `MaxInt64` に収まっていた。
- 売上 100・`Carryover = MaxInt64-40`: `LotteryPrize`(`lottery.go` 60)の加算が先にあふれて `prize` が負。
  `creditChipsCappedLocked` は負の `amount` に 0 を返すので `prize - paid` も負になり、プールが減る。

**判断が要る**(次の反復の担当者へ): これは C-3a の範囲を超える「ハンド編集された値への耐性」の話で、
評価者自身が差分の外にも同類(下記)があると書いている。`Jackpot` を飽和加算にする小さな手当てで済ませるか、
不正値の検査を 1 か所(ロールオーバーの入口)に集約するかは設計判断 — 決められないなら `blocked/` に落として親へ。

### 2. [P2, 差分外] 当選者がいない回でハウス分が消える(`internal/casino/store.go` 1085)

`winner == ""` の経路は `lottery.Carryover = prize` だけを残し、`house` をどこにも入れていない。
売上 100・券なしなら 90 だけが繰り越り、**ハウス分 10 が消える**(プールにも入らない)。
C-1 の「丸め損は常にハウス側」と C3-07 で決めた「通貨は消えない」の両方に反する。C3-07 と同じ結論
(`Jackpot += house`)で直せるはずだが、`paths:` の外なので別タスクに切ること。

### 3. [P2, 差分外] `creditChipsCappedLocked` の `headroom` があふれる

`MaxChips - account.Escrow - account.Chips` は `Chips = Escrow = MaxInt64` のハンド編集であふれる。上の 1 と同じ系統。

### 4. [P3] 旧挙動を書いたコメントが残っている(`internal/commands/casino_announce.go` 108 付近)

C3-07 で「当選者がいた回の繰り越し」は無くなったのに、祝い処理のコメントがまだ「the pot rolls forward」と説明している。
`internal/casino/announce_test.go` 457 のテスト本文のコメントも同じ(挙動への依存は無く、**文字だけ**が古い)。
コメントのみの修正で、テストは触らない。

### 評価者が PASS と認めた点(再確認は不要)

`seedJackpotLocked` の順序、残額の移送先、`Carryover = 0`、`LastDraw.Prize = paid`、表示側に旧繰り越しへの
動作上の依存が無いこと、追加テストがトートロジーでないこと(mutation で落ちることまで確認済み)。

## 反復 1 — 評価者(codex)の判定: NEEDS_WORK

対象: C3-07 上限で受け取れなかった賞金を、次の当選者ではなくジャックポットのプールへ送る(所見 1)

**[P2] プールへの加算が桁あふれし、「通貨は消えない」という完了条件を破ります。** [store.go:1100](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/store.go:1100)

再現条件は、抽選前に `Jackpot=math.MaxInt64-10`、売上100、繰り越し0、当選者の残高を `MaxChips-50`、Escrowを0とすることです。賞金90・実入金50なので、ハウス分10と残額40を加算します。

```text
変更前:  9223372036854775807
変更後: -9223372036854775769
```

コード読解とPythonの64ビット整数演算で確認しました。手編集などで上限近傍の保存値を与えた場合の問題ですが、読み込み時に拒否する検査はありません。

**親の決定(2026-09-14)**: 口座の `MaxChips`(1e12)と同じ規約をプールにも敷く。
1. `internal/casino/types.go` に `MaxJackpot int64 = MaxChips`(1e12)を足し、`types.go:126` 付近の桁あふれ余裕の説明に
   プールの行を加える(プール ≤ 1e12・加算 ≤ 1e12 なので int64 に対して十分な余裕がある、と示す)。
2. `seedJackpotLocked` を正規化点にする — 既存の「`< JackpotSeed` なら引き上げ」「`JackpotAccum < 0` なら 0」に加えて
   **`> MaxJackpot` なら `MaxJackpot` へ切り下げる**(手編集・破損ファイルの値をここで必ず正常範囲に入れる)。
3. プールへの加算は `creditJackpotCappedLocked(economy, amount) int64`(実際に入った額を返す)を通す。
   入り切らない分は**捨てる** — `creditChipsCappedLocked` が `MaxChips` で超過分を捨てるのと同じ契約で、事故ではなく仕様。
   ハウス分・上限超過の賞金・スロットの積立の 3 経路すべてをこれに差し替える。
4. 保存則の言い方を「通貨は消えない」から「**プールの上限(`MaxJackpot`)で入り切らない分だけが消え、それ以外では消えない**」へ直す
   (`store_test.go` の保存則テストと設計書 §8・`blocked/C3-06.md` の記述も同じ言い方に揃える)。
5. 回帰テスト: `Jackpot = MaxJackpot - 10` で抽選・スピンを通しても `Jackpot` が負にならず `MaxJackpot` を超えない /
   手編集で `MaxInt64` 相当が入っていても読み取り点で `MaxJackpot` に正規化される / 通常範囲では既存の期待値が変わらない。

元の修正候補(参考): 修正候補は、賞金とプール加算の容量を更新前に検査し、処理不能な保存状態を残高変更・抽選確定前に拒否することです。この境界の回帰テストも必要です。飽和加算だけでは超過分が消え、保存則を満たしません。

通常値での残額移送、`Carryover=0`、実支払額の記録、翌日のu2への非移転、文書更新、変更範囲は確認できました。保存された `verify`／`recheck` は指定検証すべて `exit=0`。追加テストの `PASS` と、旧繰り越しへ戻したmutationの `FAIL` も実ファイルで確認しました。

再検証できなかったコマンドは以下です。いずれも一時ディレクトリ作成時の `Access is denied` により未実行扱いです。

- `go build ./... && go vet ./...`：build開始前に停止、vet未到達。
- `go test ./internal/casino/... -run "TestLottery|TestStore|TestDrawLottery" -count=1`
- `go test ./... -count=1`

## 反復 2 — 評価者(codex)の判定: NEEDS_WORK

対象: C3-08 掲示できなかった回の当選を、次の掲示で取りこぼさない(所見 2)

**[P2] 次の抽選が発生すると、未掲示の当選が上書きされます。** `done-when` の「前日分を次の掲示で取りこぼさない」が未達です。

[announce.go:84](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/announce.go:84) の条件変更は指定どおりですが、その前の `ensureTodayRateLocked` が抽選を実行し、[store.go:1101](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/store.go:1101) で `LastDraw` を置き換えます。

再現手順（コード読解で確認、実行再現は未実施）:

1. `LastAnnounced = 2026-09-12`。14日8時に復旧し、13日付の当選者u1を精算する。9時前なので掲示されない。
2. 14日8時30分にu2が宝くじを購入する。
3. 14日9時の収集で当日分が抽選され、`LastDraw` がu2に置き換わる。jobにはu2だけが入り、13日のu1は永久に失われる。

[追加テスト](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/announce_test.go:841) は次回の購入券が空のケースに限定され、この経路を検証していません。

修正候補は、未掲示の抽選結果を `LastDraw` とは別に永続保持し、送信成功した結果だけを消すことです。上記の購入を挟む回帰テストも追加してください。保存構造の変更には対象パスの拡張が必要です。

保存された `verify-C3-08-{1,2,3,4}` と `recheck-C3-08-2-{1,2,3}` は実ファイルを確認し、すべて `exit=0`。旧条件へのmutationは追加テストが失敗して `exit=1`。実装変更は指定4ファイル内です。

再検証できなかったコマンド（いずれも一時ディレクトリ作成時の `Access is denied`。合格には算入していません）:

- `go build ./... && go vet ./...`：build開始前に停止、vet未到達。
- `go test ./internal/casino/... -run "TestCollectDailyAnnouncements|TestAnnounce|TestMarkAnnounced" -count=1`
- `go test ./internal/commands/... -run "TestAnnounce|TestBuildAnnouncement" -count=1`

## 反復 3 — 評価者(codex)の判定: NEEDS_WORK

対象: C3-09 「次回抽選」を呼び出し時刻ではなく確定済みの抽選日から出す(所見 3)

**[P2] 未抽選状態の購入・statusテストが不足しています。** `done-when` が指定する `DrawDate == ""` のケースが、[store_test.go:3258](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/store_test.go:3258) の表にありません。当日抽選済み・前日抽選済みの2例だけです。空文字は補助関数の単体テストでのみ検証されています。

最小修正は、購入・statusそれぞれを未抽選の独立したStoreから呼び、`now = 2026-09-14 08:59:59 JST` で `NextDrawAt == nextRunAt(now)`（同日09:00）を確認するテストの追加です。

実装はロールオーバー後の `DrawDate` を両方で使用し、指定された「遅い方」の算出になっています。`nextRunAt` は変更されておらず、実装差分も対象パス内です。

証拠ファイル `verify-C3-09-{1,2,3}.txt` と `recheck-C3-09-3-{1,2,3}.txt` を開き、全件 `exit=0`、全8パッケージ `ok` を確認しました。mutationの保存出力では旧呼び出しに戻すと追加テストが失敗しています。

再検証できなかったコマンド（いずれも作業ディレクトリ作成時の `Access is denied`。合格には算入していません）:

- `go build ./... && go vet ./...`：build開始前に停止、vet未到達。
- `go test ./internal/casino/... -run "TestLottery|TestNextRunAt|TestNextLotteryDraw|TestBuyLottery" -count=1`

`go test ./... -count=1` は再実行せず、保存出力のみ確認しました。
