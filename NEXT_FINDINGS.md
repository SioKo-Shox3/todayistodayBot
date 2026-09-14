# NEXT_FINDINGS — 次の反復がタスクより先に処理する

評価者(Astra, `codex exec -m gpt-6-astra -s read-only`)の `NEEDS_WORK` を落とす場所。
処理したら該当節を消す。全文: `.harness/runs/20260914-222248/astra-C3-07.txt`。

## C3-07 の区切り評価(2026-09-14, 対象コミット 58b721b)= NEEDS_WORK

### 1. [P2] プールへの加算に桁あふれ検査が無い(`internal/casino/store.go` 1100)

`economy.Jackpot += house + (prize - paid)` は上限を見ていない。反例はどれも**ハンド編集された JSON** が要る
(`MaxChips` は `math.MaxInt64` よりはるかに小さいので、通常の遊びでは到達しない)が、C3-07 は加算する額を
`house` から `house + 残額` へ増やしたので、あふれるまでの余裕はこの変更のぶん狭くなっている。

- `Jackpot = MaxInt64-10`・売上 100・繰り越し 0・当選者が `MaxChips-50`: 賞金 90 / 入金 50 なので 50 を足して**負値**になる。
  変更前(10 を足す)なら `MaxInt64` に収まっていた。
- 売上 100・`Carryover = MaxInt64-40`: `LotteryPrize`(`lottery.go` 60)の加算が先にあふれて `prize` が負。
  `creditChipsCappedLocked` は負の `amount` に 0 を返すので `prize - paid` も負になり、プールが減る。

**判断が要る**(次の反復の担当者へ): これは C-3a の範囲を超える「ハンド編集された値への耐性」の話で、
評価者自身が差分の外にも同類(下記)があると書いている。`Jackpot` を飽和加算にする小さな手当てで済ませるか、
不正値の検査を 1 か所(ロールオーバーの入口)に集約するかは設計判断 — 決められないなら `blocked/` に落として親へ。

### 2. [P2, 差分外] 当選者がいない回でハウス分が消える(`internal/casino/store.go` 1085)

`winner == ""` の経路は `lottery.Carryover = prize` だけを残し、`house` をどこにも入れていない。
売上 100・券なしなら 90 だけが繰り越り、**ハウス分 10 が消える**(プールにも入らない)。
C-1 の「丸め損は常にハウス側」と C3-07 で決めた「通貨は消えない」の両方に反する。C3-07 と同じ結論
(`Jackpot += house`)で直せるはずだが、`paths:` の外なので別タスクに切ること。

### 3. [P2, 差分外] `creditChipsCappedLocked` の `headroom` があふれる

`MaxChips - account.Escrow - account.Chips` は `Chips = Escrow = MaxInt64` のハンド編集であふれる。上の 1 と同じ系統。

### 4. [P3] 旧挙動を書いたコメントが残っている(`internal/commands/casino_announce.go` 108 付近)

C3-07 で「当選者がいた回の繰り越し」は無くなったのに、祝い処理のコメントがまだ「the pot rolls forward」と説明している。
`internal/casino/announce_test.go` 457 のテスト本文のコメントも同じ(挙動への依存は無く、**文字だけ**が古い)。
コメントのみの修正で、テストは触らない。

### 評価者が PASS と認めた点(再確認は不要)

`seedJackpotLocked` の順序、残額の移送先、`Carryover = 0`、`LastDraw.Prize = paid`、表示側に旧繰り越しへの
動作上の依存が無いこと、追加テストがトートロジーでないこと(mutation で落ちることまで確認済み)。
