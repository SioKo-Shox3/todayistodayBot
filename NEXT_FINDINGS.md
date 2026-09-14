
## 反復 1 — 評価者(codex)の判定: NEEDS_WORK

対象: C3B-12 精算の入金を他の経路と同じ「上限で切り詰める」形に揃える(blocking 3、P1)

**[P2] 他の精算経路を揃える完了条件が未達です。**

[store.go:1065](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/store.go:1065) の `Spin` は、まだ `creditChipsLocked` の上限エラーで取引全体を巻き戻します。C3B-17への先送りだけでは、今回の「違えば揃える」を満たしません。

- **再現条件:** `Chips=MaxChips`、ベット10、🍒🍒🍒で配当70。現在は `ErrChipCapExceeded`。既存の [store_test.go:1791](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/store_test.go:1791) も、この拒否を期待しています。
- **修正候補:** `Spin` も入金を切り詰め、実入金額を結果表示と `SeasonNet` に使用する。この条件では入金10・純利0で成功させる回帰テストへ変更し、ジャックポット超過分の扱いも整合させる。

`SettleGame` の月替わり回帰テスト、実入金額の返却・表示、`AcceptDuel`、設計書の上限例外は確認できました。対象差分は許可されたパス内です。

証拠 `recheck-C3B-12-1-{1,2,3}.txt` を開き、指定検証すべての `exit=0` と全8パッケージの `ok` を確認しました。

**こちらで完了できなかった検証:** `go build ./...`、`go test ./internal/casino/... -count=1`、`go test ./... -count=1` は一時ディレクトリ作成時の `Access is denied.` で未実行。`go vet ./...` は先行するビルド失敗により未着手です。

## 反復 2 — 評価者(codex)の判定: NEEDS_WORK

対象: C3B-13 預かりに持ち主の印を付け、別の盤面の預かりで精算できないようにする(blocking 2 の構造、P1)

### 1. [P2] 印の紐付け前に掃除されると、預かりが取り残されます

**親の決定(2026-09-15)**: 仮の印(`"opening"`)を無くして**隙間そのものを消す** — `casino.NewSessionID()` を公開し、コマンド側が**先にセッション ID を作って**から `OpenGame(..., sessionID)` に渡し、同じ ID で `SessionManager.Open` する(`Open` が ID を受け取る形にするか、`OpenWithID` を足す)。`BindEscrowSession` と `"opening"` は削除する。H&L・BJ・duel の 3 経路すべて同じ形に揃える。
併せて掃除人の扱いも直す: **精算が `ErrEscrowMismatch` で失敗したときも盤面を落とさない**(C2-10 の「精算が成功するまで消さない」規律どおり。警告としてログに残し、次の巡回で再試行する)。回帰テスト: `Open` 直後に掃除しても預かりが取り残されない / 正常な開始・精算・返金が通る。


[highlow.go:423](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/highlow.go:423) の `BindEscrowSession` より前から、盤面は掃除対象です。BJ・duel も同じ構造です。

**コード上の再現手順（実行検証は環境制約で未実施）:**

1. `sessions.Open` 成功後、`BindEscrowSession` 直前で開始処理を止める。
2. TTLを進めて掃除する。預かりの印は `"opening"` なので精算が `ErrEscrowMismatch` になり、[casino_sessions.go:106](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/casino_sessions.go:106) が盤面を削除する。
3. 開始処理を再開する。紐付けは成功するが盤面は既に存在せず、応答失敗時も `Close` が false となり返金されない。

結果、`Escrow > 0` のまま次のゲームも開始できません。`done-when` の精算維持に対し、今回追加した開始途中の状態が未処理です。

**修正候補:** 紐付け完了まで盤面を掃除から保護し、保護取得に失敗した場合も預かりを回収する。紐付け直前で掃除を挟む回帰テストを追加してください。

### 2. 対象タスクのコミットに範囲外変更があります

`git show --name-only 8ed395b` で、次の3ファイルが C3B-13 の実装コミットに含まれることを確認しました。

- `internal/commands/casino_shared.go`
- `internal/commands/balance_test.go`
- `internal/commands/season_test.go`

親コミット・前反復対応の除外には該当しません。引数変更に伴う必要性は確認できますが、`paths:` との整合が必要です。必要な関連変更として対象範囲へ明記するのが最小の対応です。

### 検証証拠

`verify-C3B-13-*` と `recheck-C3B-13-2-{1,2,3}.txt` を開き、指定3本の `exit=0`、全8パッケージの `ok` を確認しました。保存出力の抜粋です。

```text
ok   github.com/SioKo-Shox3/todayistodayBot/internal/casino   2.794s
ok   github.com/SioKo-Shox3/todayistodayBot/internal/commands 1.950s
```

S1→S2 の拒否、通常精算、印なしJSONの互換テストは確認できました。

**こちらで完了できなかった検証:** `go build ./...`、`go test ./internal/casino/... -count=1`、`go test ./... -count=1` は一時ディレクトリ作成時の `Access is denied.` で未実行。`go vet ./...` は先行ビルド失敗により未着手です。
