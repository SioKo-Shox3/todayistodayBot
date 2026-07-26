# フェーズC-1 実装開始プロンプト(Codexメイン用)

> **⚠️ この文書は使わない(2026-07-10 ユーザー判断で撤回)。**
> フェーズC-1はClaudeメインで実装する → `2026-07-10-casino-c1-claude-kickoff.md` を使うこと。
> 本ファイルは、将来Codexメインで実装する選択肢を再検討する場合の参考として残している。

> このファイルの内容をそのままCodexに渡す(対話セッションで「このファイルを読んで開始せよ」でも、
> `codex exec --sandbox workspace-write -C <リポジトリ> - < このファイル` でもよい)。

---

あなたはこのリポジトリのメイン(オーケストレーター)である。まず `AGENTS.md` と
`Docs/agent-guide/`(特に `architecture.md` / `coding-style.md` / `build-and-verify.md`)を読み、
その働き方の合意に従うこと。本タスクは危険地帯(永続化・並行性・`registry.go`・`cmd/bot/main.go`)に
触れるため、規模トリアージは**重量(heavy)**として扱う。

## タスク

フェーズC-1(カジノ経済基盤+スロット)を、実装計画に**忠実に**実装する:

- **実装計画**: `Docs/superpowers/plans/2026-07-10-casino-c1.md`(Task 1〜19、コミット境界A〜D)
- **設計書**: `Docs/superpowers/specs/2026-07-10-casino-c1-design.md`(承認済み・レビュー反映済み)

計画には各タスクの完全なコード・テスト関数名・完了条件が書かれている。計画のコードを正として
転記し、逸脱が必要になった場合は「何を・なぜ・証拠」を報告に明記する(黙って変えない)。

## Step 0(必須): 実装前の計画レビュー

この計画はClaude側で作成されたが、**クロスAIの計画レビューが未了**(レビュー実行時に両ベンダーの
利用上限に到達)。実装に入る前に、あなた自身(またはあなたの plan_reviewer サブエージェント —
`.codex/agents/`)で計画を1回批判的にレビューせよ。観点: 設計書との数値整合(両替式・レート丸め・
配当表)、`Update`(再入不可mutex)と `*Locked` ヘルパー分離のデッドロック健全性、discordgo
v0.29.0 実APIとの整合(`~/go/pkg/mod/github.com/bwmarrin/discordgo@v0.29.0/` の実ソースで確認)。
**blockerを見つけたら実装に入らず、指摘を報告して停止する。** blockerが無ければ non-blocking
指摘を記録して実装に進む。

## 進め方

1. 作業ブランチ `feat/casino-c1` を現在のHEADから作成する。
2. Task 1から順に、各タスクの **Red(失敗するテストを先に書き、失敗を実際に確認)→ Green(最小実装)** を守る。
3. コミットは計画のコミット境界どおり4つ(A: Task1 / B: Task2〜7 / C: Task8〜16 / D: Task17〜19)。
   各コミット時点で `go build ./... && go vet ./... && go test ./...` がすべて通ること。
   件名は日本語・命令形、1コミット=1論理変更、余計なフッターを付けない。
4. Task 19では `diff CLAUDE.md AGENTS.md` が空(完全一致)であることをコミット前に確認する。

## 特に厳守する点(計画のリスク節より)

- **スロット配当表の数値**(重み 28/22/21/18/7/4、倍率 ×7/×16/×22/×32/×98/×196、統計テスト
  seed=1)を**一字一句そのまま**転記する。もっともらしい別の数値に置き換えない。この数値は
  厳密分数演算+5シード×100万スピン実測で検証済み(RTP解析値92.99%)。
- `internal/casino/store.go` の `Update`(再入不可mutex)のクロージャ内から、ロックを取る
  公開メソッド(`s.Update`/`s.EnsureTodayRate`/`s.TopAssets`/`s.RecentRates` 等)を**絶対に呼ばない**
  — 必ず `ensureTodayRateLocked`/`topAssetsLocked` 等の非公開Lockedヘルパーを使う(デッドロック防止)。
- `/casino-admin` は `DefaultMemberPermissions`(Definition側)と実行時 `requireAdministrator`
  (Handle側)の**両方**を実装する。どちらか一方だけにしない。
- スロットは「**控除→抽選→払い戻しの永続化→演出**」の順序。演出(メッセージ編集)の失敗・
  クラッシュは払い戻しに一切影響させない(`runReveal` はエラーをログして `nil` を返す)。
- レートの丸めは**四捨五入**(`math.Round`)。通貨量の切り捨て規則をレートに適用しない。
- 両替は `(coins × rate × 97) / 100` 形式で**最後の除算1回のみ**(二重floorしない)。

## 書き込み許可パス

- **許可**: `internal/casino/`(新規)、`internal/commands/`(計画記載の新規ファイル+
  `registry.go`/`registry_test.go`)、`cmd/bot/main.go`、`Docs/agent-guide/architecture.md`、
  `CLAUDE.md`、`AGENTS.md`
- **禁止**: 上記以外すべて。特に `config.json`・`data/`・`bin/`・`internal/store/`・
  `internal/choseisama/`・`internal/weather/`・`internal/today/`・`internal/config/`・
  `cmd/bot/main_test.go`・計画/設計書ドキュメント自体

## 検証(Doneの条件)

- `go build ./...` / `go vet ./...` / `go test ./...` がエラー0・警告0・全8パッケージ `ok`
  (新規 `internal/casino` を含む)。
- `go test -race ./internal/casino/...` — **このマシンはCコンパイラが無く `-race` が動かないことが
  既知**(フェーズA/Bで確認済み)。失敗した場合は非raceの成功を代替証拠とし、その旨を報告に明記する
  (黙って省略しない)。
- `go test ./internal/casino/... -run TestSpin_RTP -v` が数秒以内にPASS。
- `diff CLAUDE.md AGENTS.md` が無出力。
- `make build` が成功。
- 環境注意: シェルによっては素の `go` がPATHに無い。Git Bashの場合は
  `export PATH="/c/Program Files/Go/bin:$PATH"` を前置する。

## 報告形式

- コミットごとの `git diff --stat` と件名
- 実行した検証コマンドと**実際の出力**(断言ではなく生の出力を貼る)
- Step 0 の計画レビュー結果(blocking/non-blocking の分類つき)
- 計画からの逸脱(あれば: 何を・なぜ・証拠)
- 未解決リスク・前提にしたこと

## レビューと収束

- 実装完了後、自前の impl_reviewer(`.codex/agents/`、実装者とは別エージェント)で一次レビューを
  行い、指摘を blocking / non-blocking に分類して対応する(AGENTS.mdの収束規則: 最大2周。
  3周目は存在しない — 残った non-blocking は「残課題」として報告に記録)。
- **非メイン側AI(Claude)による二次レビューはユーザーが別途手配する**ので、あなたは実施しない。
  ブランチ `feat/casino-c1` にコミット済みの状態で停止し、上記の報告を出して終了すること。
  mainへのマージもユーザー側の二次レビュー後に行う(あなたはマージしない)。
