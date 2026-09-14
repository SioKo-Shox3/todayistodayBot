# NEXT_FINDINGS — 次の反復がタスクより先に処理する

評価者(Astra, `codex exec -m gpt-6-astra -s read-only`)の `NEEDS_WORK` を落とす場所。
処理したら該当節を消す。全文: `.harness/runs/20260914-222248/astra-C3-07.txt`。

## C3-07 の区切り評価(2026-09-14, 対象コミット 58b721b)= NEEDS_WORK

所見 1(プールへの加算に桁あふれ検査が無い)は **C3-11 で閉じた** — `MaxJackpot`(= `MaxChips` = 1e12)を敷き、
プールへの加算を `creditJackpotCappedLocked` 1 か所に集め、`seedJackpotLocked` を正規化点にした。
**残っているのは所見 2・3・4 で、いずれもまだタスクになっていない。**

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
