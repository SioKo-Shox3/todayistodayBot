# NEXT_FINDINGS — 次の反復がタスクより先に処理する

評価者(Astra, `codex exec -m gpt-6-astra -s read-only`)の `NEEDS_WORK` を落とす場所。
処理したら該当節を消す。

## 反復 2 — 評価者(codex)の判定: NEEDS_WORK

対象: C3B-06 9 時掲示のシーズン欄と結果の祝い(§4 掲示)

### 1. [P1] 未掲示の結果が次の月替わりで失われる

「掲示に成功するまで保持する」を満たしません。[announce.go:121](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/announce.go:121) は `LastSeason` だけを拾いますが、[store.go:1407](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/store.go:1407) は月替わりごとに無条件で上書きします。「LastSeason は上書きされない」という前提が実装と食い違います。

- **再現手順:** 既存の `closedJuneSeasonFile` を使い、7月31日10時の巡回で結果送信だけ失敗させ、8月1日10時に再巡回する。6月の結果は7月の空の結果に上書きされ、再送されません。チャンネル未設定のまま月をまたいでも同様です。
- **修正案:** 未掲示結果を `LastSeason` とは別の永続待ち行列に保持し、送信成功した月だけ取り除く。月末の失敗→翌月の再送を回帰テストに追加してください。

### 2. [P2] 同日中の次の巡回では結果を再送できない

「送信失敗なら次の巡回でまた送られる」を満たしません。結果送信失敗時の [return nil](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/commands/casino_announce.go:272) により `LastAnnounced` が進み、次回は [日次掲示の除外条件](C:/Users/KINGkawamura/Documents/todayistodayBot/internal/casino/announce.go:85) で結果ごと除外されます。

- **再現手順:** 7月10日10時に embed 成功・結果送信失敗→送信を復旧→同日11時に再起動する。起動時巡回で結果が再送されません。
- **修正案:** 日次掲示済みでも未掲示結果だけを処理できるよう、収集・送信条件を分離する。同日再巡回で「embed 累計1件、結果1件」を検証してください。

### paths 確認

前回所見対応の `c79bad2` は除外しました。ただし対象コミット `2910ca0` にも、範囲外の `internal/casino/store.go` の空白整形4行が含まれます。機能への影響はありませんが、対象差分から分離が必要です。

### 検証証拠・制約

`verify-C3B-06-{1,2,3,4}.txt` と `recheck-C3B-06-2-{1,2,3,4}.txt` を開き、指定コマンドの終了コード0とテスト出力を確認しました。上記2件はコードから導いた再現手順で、追加実行による確認はできていません。

**再実行できなかったコマンド:** 以下はすべて Go の作業ディレクトリ作成時に `Access is denied` となり、未実行扱いです。

- `go build ./...`
- `go vet ./...`
- `go test ./internal/casino/... -count=1`
- `go test ./internal/commands/... -count=1`
- `go test ./... -count=1`

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
