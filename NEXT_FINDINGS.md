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

## 反復 1 — 評価者(codex)の判定: NEEDS_WORK

対象: C3B-P4 盤面を閉じる入口を 1 か所に絞り、預かりが残る盤面は閉じられなくする

### 1. 押下による決着が共通入口を通らない — done-when (1)(4)

[highlow.go:597](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/highlow.go:597) と [blackjack.go:649](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/blackjack.go:649) は、精算前に `WithSession` へ `done=true` を返します。[session.go:316](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/session.go:316) が盤面を削除するため、共通入口を迂回しています。

**コード上の再現:** 100枚で開始 → `SettleGame` を失敗させてキャッシュアウト／スタンド → ストア復旧 → 掃除。預かり100枚は残りますが、盤面は既に削除され、掃除では回収できません。手動押下での再試行に依存します。[経路表の「決着（押下）」](C:/Users/KINGkawamura/Documents/todayistodayBot/PROGRESS.md:839) は、この未精算状態を安全な終了として扱っています。

**修正候補:** 押下による決着も盤面と確定配当を保持し、精算後に共通入口で閉じる。H&L・BJについて「精算失敗→復旧→押下なしで掃除」の回帰テストを追加する。

### 2. ソース走査が複数行の直接呼び出しを見逃す — done-when (3)

[casino_shared_test.go:316](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/casino_shared_test.go:316) は行ごとに照合するため、次を検出できません。

```go
c.sessions.
    Close(sessionID)
```

`gofmt` はこの形式を維持します。実装から抽出した正規表現と同じ行分割による照合結果も `detected=False` でした。

**修正候補:** `os.ReadFile` で読んだソースを `go/parser` で解析して呼び出しを検査し、複数行形式も検出するテストを追加する。

### 検証

保存された `verify`・`recheck`・`mutation` を開き、通常ゲート4本の `exit=0`、全8パッケージの `ok`、変異M1/M2の失敗を確認しました。範囲外の実装変更、テストの削除・無効化はありません。

今回の再実行はすべて `go: creating work dir: … Access is denied.` で停止し、未実行です。最初のコマンドは `go vet` に到達していません。

- `go build ./... && go vet ./...`
- `go test ./internal/casino/... -count=1`
- `go test ./internal/commands/... -count=1`
- `go test ./... -count=1`
