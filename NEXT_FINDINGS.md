# NEXT_FINDINGS — 次の反復がタスクより先に処理する

評価者(Astra, `codex exec -m gpt-6-astra -s read-only`)の `NEEDS_WORK` を落とす場所。
処理したら該当節を消す。

## 反復 2 — 評価者(codex)の判定: NEEDS_WORK

対象: C3-15 未抽選の「次回抽選」テストを足し、旧挙動のコメントを消す

1. **[P2] done-when (1)：未抽選から直接 status を呼ぶ検証がありません。** [store_test.go:3798](/C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/store_test.go:3798) は購入後の同じ Store を使用しています。この時点では購入が `DrawDate` を `"2026-09-13"` に更新済みです。購入とは独立した未抽選 Store から `LotteryStatus` を呼び、指定時刻で `NextDrawAt == 2026-09-14 09:00 JST` を確認してください。保存された mutation も購入の assertion で停止しており、この不足を補いません。

2. **[P2] 指定された paths の範囲外に変更があります。** 対象コミット `95a6347` が [casino_announce_test.go](/C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/casino_announce_test.go) を変更しています。対象タスク自身の変更なので除外条件に当たりません。コメントの修正内容は実装と一致していますが、実装側で `TASKS.md` を書き換えただけでは提示された契約との不一致が残ります。親側で対象パスの誤記を訂正してください。なお、元の `internal/casino/announce_test.go` 自体は実在します。

**親の判断(2026-09-15)**: 所見 2(paths)と 3(表の形)は**処理不要** — 2 は私の `paths:` が狭すぎただけで(コメントの実際の置き場所は `internal/commands/casino_announce_test.go`。反復側が既に記録を実態へ直している)、3 はケースごとに時刻を変える必要があったので `now` 列の追加を認める。**所見 1 だけを次の反復で C3-16 と一緒に処理する** — 未抽選の Store から購入を挟まずに `LotteryStatus` を直接呼ぶケースを 1 つ足す(既存ケースは触らない)。処理したらこの節を消す。

3. **[P3] notes の「既存の表の形を変えない」に反しています。** [store_test.go:3754](/C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/store_test.go:3754) に `now` 列を追加しています。共通時刻を指定の9月14日に揃え、既存ケースの日付・期待値を対応させれば、列追加なしでケースを足せます。

証拠の `verify-C3-15-{1,2,3}.txt`、`recheck-C3-15-2-{1,2,3}.txt`、サブテスト・mutation 出力を開きました。保存されたゲートはすべて `exit=0`、全体テストは8パッケージ `ok`。掲示関連の変更はコメントのみでした。

独立再実行できなかったコマンド（すべて一時ディレクトリ作成時の `Access is denied`。今回の実行は合格に算入していません）:

- `go build ./... && go vet ./...`：build開始前に停止、vet未到達。
- `go test ./internal/casino/... -run "TestBuyLotteryTickets|TestLotteryStatus|TestAnnounce" -count=1`
- `go test ./... -count=1`
