# フェーズC-1 実装開始プロンプト(Claudeメイン用)

> 新しいClaude Codeセッションを開き、下の「---」以降をそのまま貼る(またはこのファイルを
> 読ませて開始する)。メインセッションは常に最上位モデルで運転すること。

---

フェーズC-1(カジノ経済基盤+スロット)の実装を開始してください。

## 読むもの

1. `CLAUDE.md`(働き方の合意)と `Docs/agent-guide/`(特に `architecture.md` の危険地帯 /
   `coding-style.md` / `build-and-verify.md` / `orchestration.md`)
2. **実装計画**: `Docs/superpowers/plans/2026-07-10-casino-c1.md`(Task 1〜19、コミット境界A〜D)
3. **設計書**: `Docs/superpowers/specs/2026-07-10-casino-c1-design.md`(承認済み・レビュー反映済み)

計画には各タスクの完全なコード・テスト関数名・完了条件が書かれている。計画のコードを正として
転記し、逸脱が必要になった場合は「何を・なぜ・証拠」を報告に明記する(黙って変えない)。

## 規模トリアージ: 重量(heavy)

本タスクは危険地帯に該当するため、開発段階ダイヤルが探索期でも**軽量パスにはしない**:
新規の永続化層+並行処理(`internal/casino`)、自己登録レジストリ(`internal/commands/registry.go`)、
`cmd/bot/main.go` のディスパッチ配線、任意の通貨を発行できる管理者コマンド。
全ゲート(計画レビュー・実装レビュー一次+クロスAI二次・検証)を通す。

## Step 0(必須): 実装前の計画レビュー

**この計画のレビューは未了である。** 2026-07-10の作成時にClaude側 plan-reviewer(セッション上限)と
Codex CLI(利用上限)の両方が上限に達して失敗した。したがって最初にやるのは実装ではなく計画レビュー:

- メイン側 `plan-reviewer` サブエージェント(planner とは別・最上位モデル)による一次レビュー
- パートナーAI(Codex)による二次レビュー — **直接CLI**で呼ぶ:
  `codex exec --sandbox read-only -C <リポジトリ絶対パス> - < <ブリーフのパス>`
  (同期実行。**経過時間で打ち切らない** — 失敗の証拠が出たときだけ切る。プラグイン `codex:rescue` は使わない)
  - Codexがまた利用上限で失敗した場合は、メイン側の独立二段で代替し、
    **クロスAI二次を省略した旨を必ずユーザーへ報告する**(黙って省略しない)

レビュー観点(最重要3つ):
1. 設計書との数値整合 — 両替式 `(coins × rate × 97) / 100`(除算1回のみ)、レートの四捨五入
   (切り捨てではない)、クランプ端70/140での強制トレンド転換、ストリーク式、配当表とRTP93%±1%
2. `Update`(再入不可mutex)と `ensureTodayRateLocked`/`topAssetsLocked` の分離が本当に
   デッドロックを避けられるか — 全公開メソッドで `Update` クロージャ内から別の公開ロック取得
   メソッドを呼んでいないか
3. discordgo v0.29.0 の実APIとの整合 — `~/go/pkg/mod/github.com/bwmarrin/discordgo@v0.29.0/` の
   実ソースを読んで確認(`InteractionResponseEdit`、`WebhookEdit.Content` が `*string`、
   `Contexts`/`InteractionContextGuild`、`DefaultMemberPermissions`、`UserValue`/`ChannelValue`、
   イベントハンドラが個別goroutineで起動されること)

**blockerが出たら実装に入らず、計画を修正してからユーザーに報告する。**

## 実装の進め方

1. 作業ブランチ `feat/casino-c1` を現在のHEADから作成する。
2. **メインセッションはコードを書かない**(PreToolUseフックが `.go` の Edit/Write を物理的にブロックする)。
   実装は `implementer` サブエージェントに割り当てる。難所(`internal/casino` の並行性・スロット抽選)は
   上位モデルで起動して昇格させる。サブエージェントへのブリーフには必ず含める:
   「割り当てパスの外は触らない」「検証コマンドの実出力を返す」「**同一手法の失敗2回で停止して親に返す**」
3. Task 1から順に、各タスクの **Red(失敗するテストを先に書き、失敗を実際に確認)→ Green(最小実装)** を守る。
4. コミットは計画のコミット境界どおり4つ(A: Task1 / B: Task2〜7 / C: Task8〜16 / D: Task17〜19)。
   各コミット時点で `go build ./... && go vet ./... && go test ./...` がすべて通ること。
   件名は日本語・命令形、1コミット=1論理変更、余計なフッターを付けない。
   コミット境界はオーケストレーターが所有する(サブエージェントにコミットさせない)。
5. Task 19では `diff CLAUDE.md AGENTS.md` が空(完全一致)であることをコミット前に確認する。

## 特に厳守する点(計画のリスク節より)

- **スロット配当表の数値**(重み 28/22/21/18/7/4、倍率 ×7/×16/×22/×32/×98/×196、統計テスト
  seed=1)を**一字一句そのまま**転記する。もっともらしい別の数値に置き換えない。この数値は
  厳密分数演算+5シード×100万スピン実測で検証済み(RTP解析値92.99%)。
- `internal/casino/store.go` の `Update`(再入不可mutex)のクロージャ内から、ロックを取る
  公開メソッド(`s.Update`/`s.EnsureTodayRate`/`s.TopAssets`/`s.RecentRates` 等)を**絶対に呼ばない**
  — 必ず `ensureTodayRateLocked`/`topAssetsLocked` 等の非公開Lockedヘルパーを使う(デッドロック防止)。
  レビュー時に `grep -n "s\.Update\|s\.EnsureTodayRate\|s\.TopAssets\|s\.RecentRates" internal/casino/*.go`
  で機械的に確認する。
- カジノ系7コマンドと掲示スケジューラは必ず `casino.Default()` シングルトン経由(ミューテックス共有)。
  `casino.New(path)` はテストの `t.TempDir()` 専用。
- `/casino-admin` は `DefaultMemberPermissions`(Definition側)と実行時 `requireAdministrator`
  (Handle側)の**両方**を実装する。どちらか一方だけにしない。
- スロットは「**控除→抽選→払い戻しの永続化→演出**」の順序。演出(メッセージ編集)の失敗・
  クラッシュは払い戻しに一切影響させない(`runReveal` はエラーをログして `nil` を返す)。
- レートの丸めは**四捨五入**(`math.Round`)。通貨量の切り捨て規則をレートに適用しない。

## 書き込み許可パス(implementerに渡す)

- **許可**: `internal/casino/`(新規)、`internal/commands/`(計画記載の新規ファイル+
  `registry.go`/`registry_test.go`)、`cmd/bot/main.go`、`Docs/agent-guide/architecture.md`、
  `CLAUDE.md`、`AGENTS.md`
- **禁止**: 上記以外すべて。特に `config.json`・`data/`・`bin/`・`internal/store/`・
  `internal/choseisama/`・`internal/weather/`・`internal/today/`・`internal/config/`・
  `cmd/bot/main_test.go`・計画/設計書ドキュメント自体

## 検証(Doneの条件)

- `go build ./...` / `go vet ./...` / `go test ./...` がエラー0・警告0・全8パッケージ `ok`
  (新規 `internal/casino` を含む)
- `go test -race ./internal/casino/...` — **このマシンはCコンパイラが無く `-race` が動かないことが
  既知**(フェーズA/Bで確認済み)。失敗した場合は非raceの成功を代替証拠とし、その旨を報告に明記する
  (黙って省略しない)
- `go test ./internal/casino/... -run TestSpin_RTP -v` が数秒以内にPASS
- `diff CLAUDE.md AGENTS.md` が無出力
- `make build` が成功
- 環境注意: Bashツール(Git Bash)では素の `go` がPATHに無い。
  `export PATH="/c/Program Files/Go/bin:$PATH" &&` を前置する。
  `/` で始まるコミットメッセージは `MSYS_NO_PATHCONV=1` を付けないとパス変換で壊れる(実績あり)。

## 実装後のレビューと収束

- メイン側 `impl-reviewer`(実装者とは別・最上位)による一次レビュー
- パートナーAI(Codex)による二次レビュー — **タスクの統合diffに対して1回**、直接CLIで
- **収束規則(最大2周)**: 1周目で指摘を blocking(正しさ・安全・データ損失・危険地帯の規約違反)と
  non-blocking(改善提案)に分類。修正必須は blocking のみ。2周目は**前回指摘への対応diffだけ**を
  確認する(全体を舐め直さない)。**3周目は存在しない** — 残った non-blocking は「残課題」として
  完了報告に記録し、返済サイクル(code-gardening)へ送る。
- 統合diffの check-scope 照合は1回だけ行う。

## 完了条件に含まれるもの

- 上記の全ゲートが緑(実出力を提示)
- `feat/casino-c1` を統合した後、**マージ後の片付け**まで: 全量マージ済みブランチを
  `git branch --merged` で確認して削除、役目を終えたworktreeは clean を確認して `git worktree remove`。
  未マージ・dirty・使用中のものは触らず報告する。
