# NEXT_FINDINGS

評価者の `NEEDS_WORK` をここへ落とす。次の反復がタスクより先に処理し、片付いた節を消す。
`## LESSON` 節があるときは、先に `docs/lessons/<date>-<slug>.md` を書いて `LESSONS.md` へ 1 行落とす。

(BJ の配札時決着は C3B-P4 で解消。残りは下の 1 件)

## 反復 2 — 評価者(codex)の判定: NEEDS_WORK

対象: C3B-P5 「返金待ち」の盤面をプレイできなくする(反復 1 の差し戻し 2)

### 1. 拒否時の案内が待機を伝えていない — done-when (1)

[highlow.go:576](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/highlow.go:576) とBJ・duelの拒否処理は、既存の `casinoSettleFailedMessage` を返します。実際の文言は「❌ 精算に失敗しました。もう一度お試しください。」です。

**再現経路:** 100枚で開始 → 応答失敗・返金失敗 → ストア復旧 → ボタン押下。この押下では返金を再試行せず、同じ案内を返します。再度押しても同様です。「返金の再試行を待っています」に相当する案内になっていません。

**最小修正:** 返金待ち専用の共有文言を定義し、3ゲームの拒否応答と `ErrRefundPending` の変換に使う。追加済みの回帰テストも、その待機案内を検証する形に直す。

### 確認できた点

- 共通判定・追加預かりの拒否・掃除経路の維持は確認できました。
- 3ゲームの回帰テストは、押下後の残高900／預かり100、掃除後1000／0を検証しています。H&Lはポット100の維持、duelは相手の残高1000／預かり0も確認しています。
- 保存された `verify-C3B-P5-*`・`recheck-C3B-P5-*` を開き、4ゲートの `exit=0` と全8パッケージの `ok` を確認しました。変異テストの失敗も確認しました。
- 範囲外の実装変更、テストの削除・無効化はありません。

### 今回実行できなかったコマンド

いずれも `go: creating work dir: … Access is denied.` で停止しました。最初のコマンドは `go vet` に到達していません。

- `go build ./... && go vet ./...`
- `go test ./internal/casino/... -count=1`
- `go test ./internal/commands/... -count=1`
- `go test ./... -count=1`
