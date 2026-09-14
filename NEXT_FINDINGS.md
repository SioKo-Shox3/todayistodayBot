# NEXT_FINDINGS — 評価者の指摘の受け皿

評価者(別文脈)が `NEEDS_WORK` を返したら、その指摘をここへ落とす。次の反復は
`TASKS.md` のタスクより先にこの節を処理し、片付いた節を消す。空のときは見出しだけ残す。

## 反復 3 — 評価者(codex)の判定: NEEDS_WORK

対象: C2-12 時間切れの決着に勝敗・手札を表示し、精算失敗時は金額行を出さない(所見 3・non-blocking 2)

1. **精算失敗時の案内が `done-when` と不一致です。**  
   [highlow.go:635](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/highlow.go:635) は「❌ 精算に失敗しました。もう一度お試しください。」と再試行ボタンを返します。要求された「⚠️ 精算に失敗しました。次回の自動処理で精算されます」にはなっていません。  
   再現条件は、キャッシュアウト時に `SettleGame` を失敗させることです。追加テストは embed の本文だけを調べ、`Data.Content` を検証していません。現行コードでは盤面が削除され、自動再精算もされないため、文言と動作を揃える必要があります。修正候補は、自動再精算を実装するか、親側で手動再試行を維持する契約変更を確定し、案内文の検証を追加することです。

2. **対象コミットに `paths:` 外の変更があります。**  
   `git show --name-only 8b0597c` で、[casino_shared.go:263](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/casino_shared.go:263) の `casinoPayoutLine` 追加を確認しました。C2-12 自身の変更なので除外規定は適用されません。関数を許可済みの `casino_sessions.go` に移し、`casino_shared.go` の差分を戻せば解消できます。

保存済みの `verify-C2-12-{1,2,3,4}.txt` と `recheck-C2-12-3-{1,2}.txt` は開いて確認しました。build・vet は `exit=0`、指定テストは `ok …/internal/commands 1.054s`。追加の掃除人テストでは BJ の勝ち・負け・プッシュ、H&L のカード・連勝表示が成功しています。mutation ログの失敗も確認できました。

自身の再検証で未実行扱いとなったコマンド：

- `go build ./...`：一時ディレクトリ作成が `Access is denied.`
- `go vet ./...`：直前の build 失敗により未到達。
- `go test ./internal/commands/... -run "TestRunSessionSweeper|TestHighLow|TestBlackjack" -count=1`：同じ権限エラーでテスト開始前に停止。

→ 反復 4 で `TASKS.md` の **C2-14** に切った(1 点目 = 設計書へ契約を明記、2 点目 = 共有ヘルパの置き場として採らない判断)。C2-14 が done になったらこの節を消す。
