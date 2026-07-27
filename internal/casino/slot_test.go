package casino

import (
	"math/rand"
	"testing"
)

// scriptedIntn answers Intn from a fixed script, wrapping around, and fails
// the test on any Float64 draw — slot code must never consume the rate
// generator's randomness surface. Wrapping (rather than exhausting) lets a
// test script exactly one reel triple and reuse it for every spin.
type scriptedIntn struct {
	t     *testing.T
	intns []int
	calls int
}

func (r *scriptedIntn) Intn(n int) int {
	r.t.Helper()
	if len(r.intns) == 0 {
		r.t.Fatal("scriptedIntn was built with an empty script")
		return 0
	}
	v := r.intns[r.calls%len(r.intns)]
	r.calls++
	if v < 0 || v >= n {
		r.t.Fatalf("scripted Intn value %d is outside the requested range [0,%d)", v, n)
	}
	return v
}

func (r *scriptedIntn) Float64() float64 {
	r.t.Helper()
	r.t.Fatal("unexpected randSource.Float64 draw: the slot machine only draws Intn(100)")
	return 0
}

// symbolDraw returns an Intn(100) value that lands drawSymbol on symbol,
// namely the first index of symbol's slice in the cumulative distribution.
func symbolDraw(t *testing.T, symbol SlotSymbol) int {
	t.Helper()
	acc := 0
	for _, e := range reelWeights {
		if e.Symbol == symbol {
			return acc
		}
		acc += e.Weight
	}
	t.Fatalf("symbol %q is not in reelWeights", symbol)
	return 0
}

func TestPayoutFor_TripleSeven(t *testing.T) {
	reels := [3]SlotSymbol{SymbolSeven, SymbolSeven, SymbolSeven}

	if got := payoutFor(reels, 100); got != 100*196 {
		t.Fatalf("payoutFor(7️⃣7️⃣7️⃣, 100) = %d, want %d (×196)", got, 100*196)
	}
}

func TestPayoutFor_TripleDiamond(t *testing.T) {
	reels := [3]SlotSymbol{SymbolDiamond, SymbolDiamond, SymbolDiamond}

	if got := payoutFor(reels, 100); got != 100*98 {
		t.Fatalf("payoutFor(💎💎💎, 100) = %d, want %d (×98)", got, 100*98)
	}
}

func TestPayoutFor_TripleCherry(t *testing.T) {
	reels := [3]SlotSymbol{SymbolCherry, SymbolCherry, SymbolCherry}

	// Three cherries is a TRIPLE (×7), not the ×1 cherry-pair refund: the
	// triple branch must be evaluated before the cherry count.
	if got := payoutFor(reels, 100); got != 100*7 {
		t.Fatalf("payoutFor(🍒🍒🍒, 100) = %d, want %d (×7, not the ×1 pair refund)", got, 100*7)
	}
}

func TestPayoutFor_CherryPair(t *testing.T) {
	cases := []struct {
		name  string
		reels [3]SlotSymbol
	}{
		{"cherries on reels 1,2", [3]SlotSymbol{SymbolCherry, SymbolCherry, SymbolLemon}},
		{"cherries on reels 1,3", [3]SlotSymbol{SymbolCherry, SymbolBell, SymbolCherry}},
		{"cherries on reels 2,3", [3]SlotSymbol{SymbolSeven, SymbolCherry, SymbolCherry}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := payoutFor(tc.reels, 250); got != 250 {
				t.Fatalf("payoutFor(%v, 250) = %d, want 250 (×1 bet refund)", tc.reels, got)
			}
		})
	}
}

func TestPayoutFor_NoMatch(t *testing.T) {
	cases := []struct {
		name  string
		reels [3]SlotSymbol
	}{
		{"three different, no cherry", [3]SlotSymbol{SymbolLemon, SymbolGrape, SymbolBell}},
		{"one cherry only", [3]SlotSymbol{SymbolCherry, SymbolGrape, SymbolBell}},
		{"pair that is not cherry", [3]SlotSymbol{SymbolBell, SymbolBell, SymbolGrape}},
		{"diamond pair (a near miss pays nothing)", [3]SlotSymbol{SymbolDiamond, SymbolDiamond, SymbolLemon}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := payoutFor(tc.reels, 100); got != 0 {
				t.Fatalf("payoutFor(%v, 100) = %d, want 0", tc.reels, got)
			}
		})
	}
}

func TestSpin_UsesInjectedRNG_Deterministic(t *testing.T) {
	cherry, seven := symbolDraw(t, SymbolCherry), symbolDraw(t, SymbolSeven)
	lemon := symbolDraw(t, SymbolLemon)

	cases := []struct {
		name        string
		draws       []int
		wantReels   [3]SlotSymbol
		wantPayout  int64
		wantJackpot bool
	}{
		{"triple seven", []int{seven, seven, seven}, [3]SlotSymbol{SymbolSeven, SymbolSeven, SymbolSeven}, 100 * 196, true},
		{"triple cherry is not a jackpot", []int{cherry, cherry, cherry}, [3]SlotSymbol{SymbolCherry, SymbolCherry, SymbolCherry}, 100 * 7, false},
		{"cherry pair refund", []int{cherry, lemon, cherry}, [3]SlotSymbol{SymbolCherry, SymbolLemon, SymbolCherry}, 100, false},
		{"loss", []int{lemon, seven, cherry}, [3]SlotSymbol{SymbolLemon, SymbolSeven, SymbolCherry}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := &scriptedIntn{t: t, intns: tc.draws}

			got := spin(100, rng)

			if got.Reels != tc.wantReels {
				t.Fatalf("Reels = %v, want %v — the reels must come from the injected rng in draw order", got.Reels, tc.wantReels)
			}
			if got.Bet != 100 {
				t.Fatalf("Bet = %d, want 100 (spin echoes the bet, it does not move balances)", got.Bet)
			}
			if got.Payout != tc.wantPayout {
				t.Fatalf("Payout = %d, want %d", got.Payout, tc.wantPayout)
			}
			if got.IsJackpot != tc.wantJackpot {
				t.Fatalf("IsJackpot = %v, want %v (only 💎💎💎 and 7️⃣7️⃣7️⃣ are jackpots)", got.IsJackpot, tc.wantJackpot)
			}
			if rng.calls != 3 {
				t.Fatalf("spin consumed %d Intn draws, want exactly 3 (one per reel)", rng.calls)
			}
		})
	}
}

func TestSpin_IsJackpot_TripleDiamond(t *testing.T) {
	diamond := symbolDraw(t, SymbolDiamond)
	rng := &scriptedIntn{t: t, intns: []int{diamond, diamond, diamond}}

	got := spin(10, rng)

	if !got.IsJackpot {
		t.Fatalf("spin(💎💎💎).IsJackpot = false, want true: %+v", got)
	}
	if got.Payout != 10*98 {
		t.Fatalf("Payout = %d, want %d (×98)", got.Payout, 10*98)
	}
}

func TestDrawSymbol_CumulativeDistributionBoundaries(t *testing.T) {
	// The exact boundary values of the fixed cumulative distribution:
	// 🍒 [0,28) 🍋 [28,50) 🍇 [50,71) 🔔 [71,89) 💎 [89,96) 7️⃣ [96,100).
	cases := []struct {
		draw int
		want SlotSymbol
	}{
		{0, SymbolCherry}, {27, SymbolCherry},
		{28, SymbolLemon}, {49, SymbolLemon},
		{50, SymbolGrape}, {70, SymbolGrape},
		{71, SymbolBell}, {88, SymbolBell},
		{89, SymbolDiamond}, {95, SymbolDiamond},
		{96, SymbolSeven}, {99, SymbolSeven},
	}
	for _, tc := range cases {
		rng := &scriptedIntn{t: t, intns: []int{tc.draw}}
		if got := drawSymbol(rng); got != tc.want {
			t.Fatalf("drawSymbol with Intn(100)=%d = %q, want %q", tc.draw, got, tc.want)
		}
	}
}

func TestReelWeights_SumTo100(t *testing.T) {
	sum := 0
	for _, e := range reelWeights {
		sum += e.Weight
	}
	if sum != 100 {
		t.Fatalf("reelWeights sum to %d, want exactly 100 — drawSymbol maps Intn(100) onto this cumulative distribution, so any other sum makes symbols unreachable or the fallback return reachable", sum)
	}
}

// rtpTrials is the sample size of both statistical tests. 1e6 spins keeps the
// standard error of the RTP estimator near 0.44 percentage points (variance of
// one spin's multiplier ≈ 19.31), which is small against the ±1pt band below.
const rtpTrials = 1_000_000

// rtpSeed is fixed so `go test` is deterministic: a statistical test that can
// flake on a rerun is not a quality gate. Seed 1 was one of five seeds
// measured during planning (0.93079, 0.92539, 0.92424, 0.92148, 0.92293) and
// sits closest to the analytic 0.929900.
const rtpSeed = 1

func TestSpin_RTP_WithinTargetBand_OneMillionSpins(t *testing.T) {
	// The analytic RTP of the fixed payout table is exactly 929900/1000000 =
	// 92.99% (derived by full 216-outcome enumeration — see
	// TestPayoutTable_ExactLiterals, which checks that numerator directly).
	// This test is the empirical counterpart: it proves the SAMPLED behaviour
	// of spin() (draw order, per-reel independence, payout resolution) matches
	// that analysis rather than merely that the constants are typed correctly.
	rng := rand.New(rand.NewSource(rtpSeed))
	const bet = int64(100)

	var totalPayout int64
	for i := 0; i < rtpTrials; i++ {
		totalPayout += spin(bet, rng).Payout
	}

	rtp := float64(totalPayout) / float64(bet*rtpTrials)
	if rtp < 0.92 || rtp > 0.94 {
		t.Fatalf("RTP over %d spins = %.5f, want within [0.92, 0.94] (analytic value 0.929900). DO NOT retune the payout table to fix this — the table is fixed by design; report the failure instead", rtpTrials, rtp)
	}
	t.Logf("RTP over %d spins (seed %d) = %.5f (analytic 0.929900)", rtpTrials, rtpSeed, rtp)
}

func TestSpin_NearMissFrequency_WithinTensOfSpinsBand(t *testing.T) {
	// A "near miss" is exactly two 💎 or exactly two 7️⃣ (third reel
	// different) — the outcome the reveal animation in Commit 6 dramatises.
	// Analytic frequency: 18279/1000000 = 1-in-54.71, i.e. "a few dozen spins
	// apart". The band [1/90, 1/15] is deliberately wide; the exact numerator
	// is pinned by TestPayoutTable_ExactLiterals.
	rng := rand.New(rand.NewSource(rtpSeed))

	nearMisses := 0
	for i := 0; i < rtpTrials; i++ {
		if isNearMiss(spin(100, rng).Reels) {
			nearMisses++
		}
	}

	frequency := float64(nearMisses) / float64(rtpTrials)
	if frequency < 1.0/90.0 || frequency > 1.0/15.0 {
		t.Fatalf("near-miss frequency over %d spins = %.5f (1-in-%.1f), want within [1/90, 1/15] (analytic 1-in-54.71). DO NOT retune the payout table to fix this — report the failure instead", rtpTrials, frequency, 1/frequency)
	}
	t.Logf("near miss over %d spins (seed %d): %d hits = 1-in-%.1f (analytic 1-in-54.71)", rtpTrials, rtpSeed, nearMisses, 1/frequency)
}

// isNearMiss reports whether reels hold exactly two 💎 or exactly two 7️⃣.
// Test-only: production code does not branch on near misses (Commit 6's reveal
// animation derives its own drama from SpinResult.Reels).
func isNearMiss(reels [3]SlotSymbol) bool {
	count := func(symbol SlotSymbol) int {
		n := 0
		for _, r := range reels {
			if r == symbol {
				n++
			}
		}
		return n
	}
	return count(SymbolDiamond) == 2 || count(SymbolSeven) == 2
}

// TestPayoutTable_ExactLiterals is the regression that actually protects the
// economy (SF-4). The two statistical tests above accept any table with an RTP
// inside a ±1pt band, so they pass even if weights and multipliers are
// swapped around; this one pins every literal AND re-derives the four exact
// numerators from a full 216-outcome (6³) enumeration in int64 arithmetic —
// no floating point, no rounding. Changing any weight or multiplier changes at
// least one numerator, so the table cannot silently drift.
func TestPayoutTable_ExactLiterals(t *testing.T) {
	wantWeights := []struct {
		Symbol SlotSymbol
		Weight int
	}{
		{SymbolCherry, 28}, {SymbolLemon, 22}, {SymbolGrape, 21},
		{SymbolBell, 18}, {SymbolDiamond, 7}, {SymbolSeven, 4},
	}
	if len(reelWeights) != len(wantWeights) {
		t.Fatalf("reelWeights has %d entries, want %d", len(reelWeights), len(wantWeights))
	}
	// Order matters as much as the values: drawSymbol walks reelWeights to
	// build a cumulative distribution, so permuting the slice reassigns which
	// Intn(100) values map to which symbol.
	for i, want := range wantWeights {
		if reelWeights[i].Symbol != want.Symbol || reelWeights[i].Weight != want.Weight {
			t.Fatalf("reelWeights[%d] = {%q,%d}, want {%q,%d}", i, reelWeights[i].Symbol, reelWeights[i].Weight, want.Symbol, want.Weight)
		}
	}

	wantTriple := map[SlotSymbol]int64{
		SymbolCherry: 7, SymbolLemon: 16, SymbolGrape: 22,
		SymbolBell: 32, SymbolDiamond: 98, SymbolSeven: 196,
	}
	if len(triplePayout) != len(wantTriple) {
		t.Fatalf("triplePayout has %d entries, want exactly %d", len(triplePayout), len(wantTriple))
	}
	for symbol, want := range wantTriple {
		if got := triplePayout[symbol]; got != want {
			t.Fatalf("triplePayout[%q] = %d, want %d", symbol, got, want)
		}
	}

	if cherryPairPayout != 1 {
		t.Fatalf("cherryPairPayout = %d, want 1 (exactly two 🍒 refunds the bet)", cherryPairPayout)
	}

	// Full enumeration of the 6³ = 216 ordered outcomes. Each outcome's weight
	// is w_i*w_j*w_k, and the weights sum to 100 per reel, so the denominator
	// is exactly 100³ = 1,000,000. payoutFor is called with bet=1, making the
	// returned payout the multiplier itself.
	const wantDenominator int64 = 1_000_000
	var denominator, tripleNumerator, cherryPairNumerator, nearMissNumerator int64
	for _, a := range reelWeights {
		for _, b := range reelWeights {
			for _, c := range reelWeights {
				weight := int64(a.Weight) * int64(b.Weight) * int64(c.Weight)
				denominator += weight
				reels := [3]SlotSymbol{a.Symbol, b.Symbol, c.Symbol}
				multiplier := payoutFor(reels, 1)
				switch {
				case reels[0] == reels[1] && reels[1] == reels[2]:
					tripleNumerator += weight * multiplier
				default:
					cherryPairNumerator += weight * multiplier
				}
				if isNearMiss(reels) {
					nearMissNumerator += weight
				}
			}
		}
	}
	rtpNumerator := tripleNumerator + cherryPairNumerator

	if denominator != wantDenominator {
		t.Fatalf("enumerated weight total = %d, want %d (100³) — the per-reel weights no longer sum to 100", denominator, wantDenominator)
	}
	if tripleNumerator != 760556 {
		t.Fatalf("three-of-a-kind contribution = %d/%d, want 760556/%d", tripleNumerator, wantDenominator, wantDenominator)
	}
	if cherryPairNumerator != 169344 {
		t.Fatalf("🍒-pair contribution = %d/%d, want 169344/%d", cherryPairNumerator, wantDenominator, wantDenominator)
	}
	if rtpNumerator != 929900 {
		t.Fatalf("RTP = %d/%d, want 929900/%d (= 92.99%%)", rtpNumerator, wantDenominator, wantDenominator)
	}
	if nearMissNumerator != 18279 {
		t.Fatalf("near-miss probability = %d/%d, want 18279/%d (= 1-in-54.71)", nearMissNumerator, wantDenominator, wantDenominator)
	}
	t.Logf("216-outcome enumeration: triple=%d/%d cherryPair=%d/%d rtp=%d/%d nearMiss=%d/%d",
		tripleNumerator, denominator, cherryPairNumerator, denominator, rtpNumerator, denominator, nearMissNumerator, denominator)
}
