# NEXT_FINDINGS

評価者の `NEEDS_WORK` をここへ落とす。次の反復がタスクより先に処理し、片付いた節を消す。
`## LESSON` 節があるときは、先に `docs/lessons/<date>-<slug>.md` を書いて `LESSONS.md` へ 1 行落とす。

(BJ の配札時決着は C3B-P4 で解消。残りは下の 1 件)

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

## 反復 2 — 評価者(codex)の判定: NEEDS_WORK

対象: C3B-P5 「返金待ち」の盤面をプレイできなくする(反復 1 の差し戻し 2)

### 1. 返金待ちの案内が done-when (1) を満たしていません

3ゲームとも返金待ちへの押下に `casinoSettleFailedMessage` を返しますが、その内容は「❌ 精算に失敗しました。もう一度お試しください。」です。[文言の定義](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/casino_shared.go:255)

**再現経路:** 盤面送信失敗 → 返金失敗 → 復旧後にボタン押下。操作は拒否されますが、押し直しても返金を再試行しないのに、再操作を案内します。「返金の再試行を待っています」に相当する説明になっていません。追加テストも、この文言を期待値にしています。

**最小修正候補:** 返金待ち専用の共通文言で待機を案内し、3ゲームの拒否応答とBJの `ErrRefundPending` の変換、回帰テストの期待値に適用してください。

### 確認済み

共通判定による操作・追加預かりの拒否、3ゲームの金額不変と掃除後の返金を確認しました。対象外の実装変更、テスト削除・無効化はありません。証拠8ファイルを開き、HEAD `7da6edf` に対応するゲート4種類の `exit=0`、全8パッケージの `ok` を確認しました。

### 独立再実行できなかったコマンド

いずれも `go: creating work dir: … Access is denied.` で停止し、未実行です。`go vet` には到達していません。

- `go build ./... && go vet ./...`
- `go test ./internal/casino/... -count=1`
- `go test ./internal/commands/... -count=1`
- `go test ./... -count=1`

## 反復 3 — 評価者(codex)の判定: NEEDS_WORK

対象: C3B-X1 盤面を消す機構を 1 つだけにし、走査を構文木で行う(C3B-P4 の差し戻し)

対象HEADは `406f99a`。以下が未完了です。未コミットの変更は判定に含めていません。

1. **テストのAPI移行漏れ — 条件(1)・verify**
   `WithSession` は `func(*Session) error` に変わりましたが、HEAD版の [session_test.go](/C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/session_test.go:135) には `(bool, error)` を返す呼び出しが9か所残り、型が一致しません。また、期限切れ盤面を共通入口で削除する変更に対して、既存テストは保持を期待しています。
   **修正:** コールバックと旧仕様の期待値を移行し、成功・失敗のどちらでも `WithSession` が盤面を削除しないことを検証してください。

2. **構文木走査が未実装 — 条件(3)**
   [casino_shared_test.go](/C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/casino_shared_test.go:282) は依然として正規表現と行分割による走査です。`c.sessions.` の次行に `Close(id)` を置く呼び出しや、`mgr.Remove(id)` を検出できません。
   **修正:** `go/parser`・`go/ast` でセッションマネージャの削除メソッド呼び出しを検査してください。

3. **指定された回帰テスト3本と経路表が未更新 — 条件(4)**
   押下決着の精算失敗から、盤面保持・復旧後の掃除・残高回復までを検証する3ゲームのテストがありません。既存の押下失敗テストは手動再試行、掃除テストは未送達や配札時決着などを扱っています。[PROGRESS.md](/C:/Users/KINGkawamura/Documents/todayistodayBot/PROGRESS.md:839) も「`WithSession` が先に外す」「掃除人の `Remove` は入口を通らない」のままです。
   **修正:** 指定の3経路を追加し、確定配当・預かり・盤面数を検証して経路表を更新してください。

4. **C3B-X1に対応する合格証拠がありません**
   指定ディレクトリの検証ログ8本を開きました。すべてC3B-P5用で、`verify-*` は `HEAD=7da6edf` と明記されています。`blocked-C3B-X1-evidence.txt` はプロセス一覧と作業状態の記録です。
   **修正:** 完成した対象コミットに対して4ゲートを実行し、出力を保存してください。

範囲外の実装変更、対象差分でのテスト削除・無効化はありません。

**独立再実行できなかったコマンド**

すべて `go: creating work dir: … Access is denied.` で停止し、未実行です。`go vet` には到達していません。

- `go build ./... && go vet ./...`
- `go test ./internal/casino/... -count=1`
- `go test ./internal/commands/... -count=1`
- `go test ./... -count=1`
