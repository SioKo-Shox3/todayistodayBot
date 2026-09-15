# NEXT_FINDINGS

評価者の `NEEDS_WORK` をここへ落とす。次の反復がタスクより先に処理し、片付いた節を消す。
`## LESSON` 節があるときは、先に `docs/lessons/<date>-<slug>.md` を書いて `LESSONS.md` へ 1 行落とす。

(BJ の配札時決着は C3B-P4 で解消。残りは下の 1 件)

## 反復 1 — 評価者(codex)の判定: NEEDS_WORK

対象: C3B-P3 開始の間は掃除人を入れない/返金に失敗した盤面は閉じない(C3B-P2 の差し戻し)

### 1. 「返金待ち」にした盤面を、その後もプレイできる(→ `TASKS.md` の C3B-P5)

[session.go:411](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/session.go:411) の `MarkRefundPending` は支払額だけを固定します。`Hold`・`WithSession`・各ゲームの押下処理は返金待ちを拒否しません。

コード上の反例：メッセージ生成後に応答エラーとなり、返金も失敗したH&Lで、復旧後にボタンを押す。既存の `zeroRng` なら「ハイ→ロー」でポットが101になりますが、掃除人は固定された100だけを支払います。BJも返金待ちからスタンド・ダブルへ進めます。

**未達条件:** 返金失敗後も「返金待ち」を維持し、決着させず返す。

**修正候補:** 返金待ちをゲーム操作できない状態として扱い、3ゲームで押下・追加預かりを拒否する。返金失敗後の押下と掃除を組み合わせた回帰テストを追加する。

### 検証

範囲外の実装変更、テストの削除・無効化は確認されませんでした。保存された `verify`・`recheck`・`mutation` を開き、通常ゲート4本の `exit=0` と全8パッケージの `ok` を確認しました。

今回の再実行はすべて `go: creating work dir: … Access is denied.` で停止し、未実行です。最初のコマンドでは `go vet` に到達していません。

- `go build ./... && go vet ./...`
- `go test ./internal/casino/... -count=1`
- `go test ./internal/commands/... -count=1`
- `go test ./... -count=1`
