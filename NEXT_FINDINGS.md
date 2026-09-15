# NEXT_FINDINGS

評価者の `NEEDS_WORK` をここへ落とす。次の反復がタスクより先に処理し、片付いた節を消す。
`## LESSON` 節があるときは、先に `docs/lessons/<date>-<slug>.md` を書いて `LESSONS.md` へ 1 行落とす。

(BJ の配札時決着は C3B-P4 で解消。反復 2 の指摘(拒否時の案内)は C3B-X2 で解消。残りは C3B-X1 の構文木走査について下の 2 件)

## 反復 4 — 評価者(codex)の判定: NEEDS_WORK

対象: C3B-X1 盤面を消す機構を 1 つだけにし、走査を構文木で行う(C3B-P4 の差し戻し)

対象は `433c5ff`。評価中に追加された経路表の更新も確認しました。

### 条件(3)：構文木走査に検出漏れがあります

[casino_shared_test.go:332](/C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/casino_shared_test.go:332) は、検査する**ファイル内**の型宣言からマネージャの識別子を集めます。そのため、次を別の非テストファイルに置くと削除呼び出しを検出できません。

```go
package commands

func (c *HighLowCommand) retireDirectlyForProbe(id string) {
	c.sessions.
		Close(id)
}
```

`HighLowCommand.sessions` の定義は別ファイルにあるため、`managers` が空になり、343行目で検査を抜けます。`m := casino.DefaultSessions(); m.Remove(id)` のような型推論による変数も収集対象外です。これはソースから確認した反例で、書き込み・実行はしていません。

**修正候補:** パッケージ全体の構文木を解析し、標準ライブラリの `go/types` などでレシーバの型を判定してください。別ファイルのフィールドと `:=` 経由の呼び出しを検出する回帰ケースも追加してください。

### 確認できた内容

- `WithSession` による削除はなくなり、現行の削除呼び出しは共通入口に集約されています。
- 回帰テスト3本と経路表の更新があります。範囲外の実装変更や、検証を無効化する変更はありません。
- 指定ディレクトリのログは旧タスク用でした。追加で `PROGRESS.md` が参照する `.harness/runs/20260915-120429/verify-C3B-X1-{1,2,3,4}.txt` と実行記録を開き、4ゲートの `exit=0`、全8パッケージの `ok` を確認しました。

### 独立再実行できなかったコマンド

すべて `go: creating work dir: … Access is denied.` で停止し、未実行です。`go vet` には到達していません。

- `go build ./... && go vet ./...`
- `go test ./internal/casino/... -count=1`
- `go test ./internal/commands/... -count=1`
- `go test ./... -count=1`

## 反復 3 — 評価者(codex)の判定: NEEDS_WORK

対象: C3B-X1 盤面を消す機構を 1 つだけにし、走査を構文木で行う(C3B-P4 の差し戻し)

### 1. 構文木走査が別ファイルの削除呼び出しを見逃す — done-when (3)

[casino_shared_test.go:332](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/casino_shared_test.go:332) は、検査するファイル内の明示的な `*casino.SessionManager` 宣言だけを集めています。

例えば別の非テストファイルに次を追加しても、検出対象になりません。

```go
package commands

func (c *HighLowCommand) dropBoard(id string) {
	c.sessions.Close(id)
}
```

フィールドの定義は `highlow.go` にあるため、このファイルの `managers` は空になり、343行目で呼び出しを除外します。`mgr := c.sessions; mgr.Remove(id)` という別名への代入も追跡しません。

**修正候補:** パッケージ全体で受信側の型を解決し、削除メソッドか判定する。`go/types` も標準ライブラリです。別ファイル・別名経由の検出を回帰テストに追加してください。

### 2. 掃除で支払い済みになっても再試行記録が残る — done-when (2) に伴う回帰

[highlow.go:548](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/highlow.go:548) と [blackjack.go:575](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/blackjack.go:575) は、盤面の存在確認より先に `lock.pending` を処理します。一方、掃除による精算成功はこの記録を消しません。

**再現手順:**

1. 100枚で開始し、キャッシュアウト／スタンドの精算を失敗させる。
2. 復旧後に掃除し、支払いと盤面削除を完了させる。
3. 掃除時のメッセージ編集を失敗させ、残った🔁を押す。

コード上、`SettleGame` は `ErrNoGameInProgress` を返し、支払い済みなのに再び「精算失敗」と🔁を表示します。`pending` も残り続けます。

**修正候補:** 掃除の精算完了時に再試行記録も片付け、終了済み盤面への古い押下を終了として処理する。追加したH&L・BJの回帰テストに、掃除後の押下を加えてください。

### 確認した証拠

- `verify-C3B-X1-*`・`recheck-C3B-X1-3-*` を開き、4ゲートの `exit=0`、全8パッケージの `ok` を確認しました。
- `mutation-C3B-X1.txt` の6件すべてに失敗出力がありました。ただし、上記の反例は含まれていません。
- `WithSession` の削除処理廃止、掃除の共通入口経由、3ゲームの回帰テスト、経路表更新は確認できました。無関係な範囲外変更や検証の無効化はありません。

### 今回実行できなかったコマンド

すべて `go: creating work dir: … Access is denied.` で停止し、未実行です。上記の反例は静的確認であり、実行結果ではありません。最初のコマンドは `go vet` に到達していません。

- `go build ./... && go vet ./...`
- `go test ./internal/casino/... -count=1`
- `go test ./internal/commands/... -count=1`
- `go test ./... -count=1`
