# NEXT_FINDINGS — 次の反復がタスクより先に処理する

評価者(Astra, `codex exec -m gpt-6-astra -s read-only`)の `NEEDS_WORK` を落とす場所。
処理したら該当節を消す。

## 反復 3 — 評価者(codex)の判定: NEEDS_WORK

対象: C3B-07 README・設計書の実測・architecture の追記案

危険地帯7点目の追記案に、実装と矛盾する説明が2件あります。

### 1. [P2] duelのゼロサムに上限の例外がない

[blocked/C3B-07.md:46](C:/Users/KINGkawamura/Documents/todayistodayBot/blocked/C3B-07.md:46) は無条件にゼロサムと説明し、[README.md:100](C:/Users/KINGkawamura/Documents/todayistodayBot/README.md:100) も勝者が必ずベットの2倍を受け取る記述です。

しかし、既存テスト `TestAcceptDuel_WinnerAtTheCapTakesOnlyWhatFitsAndSeasonNetCountsThat` では、残高 `MaxChips − 100` の挑戦者が200を賭けて勝つと、受取額は300です。両口座の合計は100減ります。テスト本体と保存された成功出力を確認しました。

**最小修正:** READMEに上限による減額を明記し、危険地帯には「上限による切り捨てがない場合にゼロサム」と、その例外を書いてください。設計書の実測段落とは整合します。

### 2. [P2] 全口座を書き換える経路を誤って限定している

[blocked/C3B-07.md:60](C:/Users/KINGkawamura/Documents/todayistodayBot/blocked/C3B-07.md:60) の「全口座を触る唯一の書き込み」「他のすべての経路が1〜2口座」は成立しません。

- [SeasonStatus](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/store.go:720) は、月替わりがなくても全口座を正規化し、保存します。
- [RefundStaleEscrows](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/store.go:1610) は、全ギルドの対象口座をまとめて返金します。

**最小修正:** 「切り替えは全口座の `SeasonNet` を一括で0にする書き込み」とし、経路を限定する断定を削除してください。

### 検証証拠

指定ディレクトリのC3B-07用verify 4本・recheck 3本を開き、終了コード0、全8パッケージの `ok`、実測の根拠となる5テストの成功を確認しました。対象変更は許可範囲内で、architecture展開コピー・`cmd/bot/main.go`・テストの変更はありません。

**再実行できなかったコマンド:** `go build ./... && go vet ./...`、`go test ./... -count=1`。どちらも作業ディレクトリ作成時に `Access is denied` となりました。`go vet` は前段失敗で未実行です。バイナリ生成コマンドは保存済み証拠のみで確認しました。

