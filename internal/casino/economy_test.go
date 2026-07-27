package casino

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

// fixedRand answers every Float64 draw with the same value and every Intn draw
// with 0. It suits tests that only care which branch nextRate takes, not how
// many draws that branch costs.
type fixedRand struct{ float float64 }

func (r fixedRand) Float64() float64 { return r.float }

func (r fixedRand) Intn(int) int { return 0 }

// cyclicRand replays a fixed sequence of Float64 draws, wrapping around, so a
// test can hold nextRate on one branch for as many simulated days as it likes.
// The sequence length must match the number of draws that branch consumes per
// day, otherwise the branch drifts.
type cyclicRand struct {
	floats []float64
	calls  int
}

func (r *cyclicRand) Float64() float64 {
	v := r.floats[r.calls%len(r.floats)]
	r.calls++
	return v
}

func (r *cyclicRand) Intn(int) int { return 0 }

// scriptedRand answers Float64 from a fixed script and fails the test when the
// script runs out, pinning both the branch taken and the number of draws.
type scriptedRand struct {
	t      *testing.T
	floats []float64
	calls  int
}

func (r *scriptedRand) Float64() float64 {
	r.t.Helper()
	if r.calls >= len(r.floats) {
		r.t.Fatalf("randSource.Float64 was called %d times but the script only has %d values", r.calls+1, len(r.floats))
	}
	v := r.floats[r.calls]
	r.calls++
	return v
}

func (r *scriptedRand) Intn(int) int { return 0 }

// forbiddenRand fails the test on any draw at all, proving a code path
// consumes no randomness.
type forbiddenRand struct {
	t      *testing.T
	reason string
}

func (r forbiddenRand) Float64() float64 {
	r.t.Helper()
	r.t.Fatalf("unexpected randSource.Float64 draw: %s", r.reason)
	return 0
}

func (r forbiddenRand) Intn(int) int {
	r.t.Helper()
	r.t.Fatalf("unexpected randSource.Intn draw: %s", r.reason)
	return 0
}

func TestNextRate_NeverEscapesClampRange(t *testing.T) {
	const days = 100000

	for _, seed := range []int64{1, 7, 20260710} {
		rng := rand.New(rand.NewSource(seed))
		rate, trend, _ := nextRate(0, TrendFlat, false, rng)
		if rate != baseRate {
			t.Fatalf("seed %d: first day rate = %d, want %d", seed, rate, baseRate)
		}

		for day := 1; day <= days; day++ {
			var event EventKind
			rate, trend, event = nextRate(rate, trend, true, rng)
			if rate < minRate || rate > maxRate {
				t.Fatalf("seed %d day %d: rate %d escaped the [%d,%d] clamp (trend=%q event=%q)", seed, day, rate, minRate, maxRate, trend, event)
			}
			switch trend {
			case TrendBull, TrendBear, TrendFlat:
			default:
				t.Fatalf("seed %d day %d: trend = %q, want one of bull/bear/flat", seed, day, trend)
			}
			switch event {
			case EventNone, EventSurge, EventCrash:
			default:
				t.Fatalf("seed %d day %d: event = %q, want one of none/surge/crash", seed, day, event)
			}
		}
	}
}

func TestNextRate_ForcedCentralTransition_AtFloor(t *testing.T) {
	for _, prevTrend := range []TrendState{TrendBull, TrendBear, TrendFlat} {
		for _, draw := range []float64{0, 0.14, 0.5, 0.99} {
			_, trend, _ := nextRate(minRate, prevTrend, true, fixedRand{float: draw})
			if trend != TrendBull {
				t.Fatalf("at the %d floor with prevTrend=%q and every draw = %v: trend = %q, want %q (the floor forces the trend toward the centre regardless of the rng)", minRate, prevTrend, draw, trend, TrendBull)
			}
		}
	}
}

func TestNextRate_ForcedCentralTransition_AtCeiling(t *testing.T) {
	for _, prevTrend := range []TrendState{TrendBull, TrendBear, TrendFlat} {
		for _, draw := range []float64{0, 0.14, 0.5, 0.99} {
			_, trend, _ := nextRate(maxRate, prevTrend, true, fixedRand{float: draw})
			if trend != TrendBear {
				t.Fatalf("at the %d ceiling with prevTrend=%q and every draw = %v: trend = %q, want %q (the ceiling forces the trend toward the centre regardless of the rng)", maxRate, prevTrend, draw, trend, TrendBear)
			}
		}
	}
}

// TestNextRate_ForcedCentralTransition_PersistsWhileAtBoundary pins the part of
// the design a single-day assertion cannot see: the forced central transition
// is not a one-shot nudge on the day the clamp is first hit, it applies on
// EVERY day the rate is still sitting on the boundary.
func TestNextRate_ForcedCentralTransition_PersistsWhileAtBoundary(t *testing.T) {
	const days = 5

	t.Run("ceiling", func(t *testing.T) {
		// At the boundary the trend switch consumes no draw, so each day costs
		// exactly three: event check (hit), magnitude (=15%), direction (surge).
		// 140 * 1.15 = 161 -> clamped back to 140, so the rate never leaves the
		// ceiling.
		rng := &cyclicRand{floats: []float64{0, 0, 0.4}}
		rate, trend := maxRate, TrendBull
		for day := 1; day <= days; day++ {
			var event EventKind
			rate, trend, event = nextRate(rate, trend, true, rng)
			if rate != maxRate {
				t.Fatalf("day %d: rate = %d, want the run to stay pinned at the %d ceiling", day, rate, maxRate)
			}
			if event != EventSurge {
				t.Fatalf("day %d: event = %q, want %q", day, event, EventSurge)
			}
			if trend != TrendBear {
				t.Fatalf("day %d: trend = %q, want %q — the forced central transition must apply on every day spent at the ceiling, not just the first", day, trend, TrendBear)
			}
		}
	})

	t.Run("floor", func(t *testing.T) {
		// Same three draws per day, but the direction draw picks the crash
		// branch: 70 * 0.85 = 59.5 -> clamped back to 70.
		rng := &cyclicRand{floats: []float64{0, 0, 0.6}}
		rate, trend := minRate, TrendBear
		for day := 1; day <= days; day++ {
			var event EventKind
			rate, trend, event = nextRate(rate, trend, true, rng)
			if rate != minRate {
				t.Fatalf("day %d: rate = %d, want the run to stay pinned at the %d floor", day, rate, minRate)
			}
			if event != EventCrash {
				t.Fatalf("day %d: event = %q, want %q", day, event, EventCrash)
			}
			if trend != TrendBull {
				t.Fatalf("day %d: trend = %q, want %q — the forced central transition must apply on every day spent at the floor, not just the first", day, trend, TrendBull)
			}
		}
	})
}

func TestNextRate_FirstDay_ReturnsBaseRateNoRandomness(t *testing.T) {
	rng := forbiddenRand{t: t, reason: "a guild's first day is a fixed starting point (基準100), not a draw"}

	rate, trend, event := nextRate(0, TrendFlat, false, rng)
	if rate != baseRate {
		t.Fatalf("rate = %d, want %d", rate, baseRate)
	}
	if trend != TrendFlat {
		t.Fatalf("trend = %q, want %q", trend, TrendFlat)
	}
	if event != EventNone {
		t.Fatalf("event = %q, want %q", event, EventNone)
	}
}

// TestNextRate_RoundsHalfAwayFromZero_NotFloor pins math.Round rather than a
// truncating int64/int conversion. Truncation would bias every single day
// downward by up to one point, which over a month quietly turns the "凪" of a
// flat trend into a bear market. The two cases below are chosen so the
// truncating implementation returns a different, stated number.
func TestNextRate_RoundsHalfAwayFromZero_NotFloor(t *testing.T) {
	cases := []struct {
		name         string
		prevRate     int
		prevTrend    TrendState
		draws        []float64
		want         int
		ifTruncated  int
		rawExplained string
	}{
		{
			// No trend flip (0.5 >= 0.15), no event (0.5 >= 0.05), flat drift = 0,
			// noise = -3 + 0.6*6 = +0.6%.
			name:         "normal drift path",
			prevRate:     100,
			prevTrend:    TrendFlat,
			draws:        []float64{0.5, 0.5, 0.6},
			want:         101,
			ifTruncated:  100,
			rawExplained: "100 * 1.006 = 100.6",
		},
		{
			// No trend flip, event hit (0.0 < 0.05), magnitude = 15 + 0*15 = 15%,
			// direction 0.4 < 0.5 = surge. 100 * 1.15 evaluates to
			// 114.99999999999999 in float64, so only rounding reaches 115.
			name:         "event path",
			prevRate:     100,
			prevTrend:    TrendFlat,
			draws:        []float64{0.5, 0, 0, 0.4},
			want:         115,
			ifTruncated:  114,
			rawExplained: "100 * 1.15 = 114.99999999999999",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rate, _, _ := nextRate(tc.prevRate, tc.prevTrend, true, &scriptedRand{t: t, floats: tc.draws})
			if rate != tc.want {
				extra := ""
				if rate == tc.ifTruncated {
					extra = " — that is the truncated value; the rate must be rounded (math.Round), never floored"
				}
				t.Fatalf("rate = %d, want %d (raw: %s)%s", rate, tc.want, tc.rawExplained, extra)
			}
		})
	}
}

func TestNextRate_EventKind_Surge(t *testing.T) {
	// No trend flip, event hit, magnitude 15%, direction < 0.5 = surge.
	rate, _, event := nextRate(100, TrendFlat, true, &scriptedRand{t: t, floats: []float64{0.5, 0, 0, 0.4}})
	if event != EventSurge {
		t.Fatalf("event = %q, want %q", event, EventSurge)
	}
	if rate <= 100 {
		t.Fatalf("rate = %d, want a surge to move the rate up from 100", rate)
	}
}

func TestNextRate_EventKind_Crash(t *testing.T) {
	// Same draws except the direction: 0.6 >= 0.5 = crash.
	rate, _, event := nextRate(100, TrendFlat, true, &scriptedRand{t: t, floats: []float64{0.5, 0, 0, 0.6}})
	if event != EventCrash {
		t.Fatalf("event = %q, want %q", event, EventCrash)
	}
	if rate >= 100 {
		t.Fatalf("rate = %d, want a crash to move the rate down from 100", rate)
	}
}

func TestNextRate_EventKind_None(t *testing.T) {
	// No trend flip, no event: the ordinary drift+noise path.
	_, _, event := nextRate(100, TrendFlat, true, &scriptedRand{t: t, floats: []float64{0.5, 0.5, 0.6}})
	if event != EventNone {
		t.Fatalf("event = %q, want %q", event, EventNone)
	}
}

// TestNextRate_SurgeClampedToCeiling_StillReportsEventSurge is the regression
// behind persisting EventKind instead of re-deriving it from the day-over-day
// move: a +15% surge from 130 clamps to 140, which is only +7.69% — below any
// sane threshold — so a guessing display would silently drop the 🚀.
func TestNextRate_SurgeClampedToCeiling_StillReportsEventSurge(t *testing.T) {
	const prevRate = 130

	rate, _, event := nextRate(prevRate, TrendFlat, true, &scriptedRand{t: t, floats: []float64{0.5, 0, 0, 0.4}})
	if rate != maxRate {
		t.Fatalf("rate = %d, want the surge to be clamped to %d", rate, maxRate)
	}
	if event != EventSurge {
		t.Fatalf("event = %q, want %q even though the clamp swallowed the move", event, EventSurge)
	}
	if dayOverDay := float64(rate-prevRate) / prevRate * 100; dayOverDay >= 10 {
		t.Fatalf("precondition: the visible move is %.2f%%, which is no longer below a 10%% threshold — this test no longer proves the clamp can hide a surge", dayOverDay)
	}
}

func TestDailyBonusAmount_Table(t *testing.T) {
	cases := []struct {
		streak int
		want   int64
	}{
		{streak: 1, want: 200},
		{streak: 2, want: 250},
		{streak: 3, want: 300},
		{streak: 6, want: 450},
		{streak: 7, want: 1000}, // 500 + the 500 大入り袋
		{streak: 8, want: 500},  // capped at the 7-day amount, no 大入り袋
		{streak: 13, want: 500},
		{streak: 14, want: 1000}, // every 7th day pays the 大入り袋 again
		{streak: 21, want: 1000},
	}

	for _, tc := range cases {
		if got := dailyBonusAmount(tc.streak); got != tc.want {
			t.Errorf("dailyBonusAmount(%d) = %d, want %d", tc.streak, got, tc.want)
		}
	}
}

// TestExchangeCoinToChip_SingleTruncation pins the "exactly one integer
// division" rule. Splitting the fee into its own step truncates twice and
// returns 291 instead of 293 (floor(3*101/100)*97 = 291 and
// 3*floor(101*97/100) = 291).
func TestExchangeCoinToChip_SingleTruncation(t *testing.T) {
	got, err := exchangeCoinToChip(3, 101)
	if err != nil {
		t.Fatalf("exchangeCoinToChip(3, 101) returned error: %v", err)
	}
	if got != 293 {
		msg := ""
		if got == 291 {
			msg = " — that is the double-truncated value; 3*101*97/100 must be evaluated with a single final division"
		}
		t.Fatalf("exchangeCoinToChip(3, 101) = %d, want 293%s", got, msg)
	}
}

// TestExchangeChipToCoin_SingleTruncation pins the same rule in the other
// direction. Taking the fee before the division is provably indistinguishable
// here (floor(97c/100)/r == floor(97c/(100r)) for all c,r), so the misreading
// this identifies is the other one: converting first and charging the fee
// afterwards, floor(floor(1100/70)*97/100) = floor(15*0.97) = 14.
func TestExchangeChipToCoin_SingleTruncation(t *testing.T) {
	got, err := exchangeChipToCoin(1100, 70)
	if err != nil {
		t.Fatalf("exchangeChipToCoin(1100, 70) returned error: %v", err)
	}
	if got != 15 {
		msg := ""
		if got == 14 {
			msg = " — that is the convert-then-charge-the-fee value; 1100*97/(100*70) must be evaluated with a single final division"
		}
		t.Fatalf("exchangeChipToCoin(1100, 70) = %d, want 15%s", got, msg)
	}
}

// TestExchange_RoundTrip_NeverIncreasesTotalValue is the design doc's
// acceptance property: the 3% fee plus truncation must make a round trip a
// strict loss, never a mint.
func TestExchange_RoundTrip_NeverIncreasesTotalValue(t *testing.T) {
	amounts := []int64{1, 2, 3, 7, 10, 99, 100, 1_000, 12_345, 1_000_000, 999_999_999}

	for rate := minRate; rate <= maxRate; rate++ {
		for _, start := range amounts {
			chips, err := exchangeCoinToChip(start, rate)
			if err != nil {
				t.Fatalf("exchangeCoinToChip(%d, %d) returned error: %v", start, rate, err)
			}
			back, err := exchangeChipToCoin(chips, rate)
			if err != nil {
				t.Fatalf("exchangeChipToCoin(%d, %d) returned error: %v", chips, rate, err)
			}
			if back > start {
				t.Fatalf("coin round trip minted currency at rate %d: %d coins -> %d chips -> %d coins", rate, start, chips, back)
			}

			coins, err := exchangeChipToCoin(start, rate)
			if err != nil {
				t.Fatalf("exchangeChipToCoin(%d, %d) returned error: %v", start, rate, err)
			}
			backChips, err := exchangeCoinToChip(coins, rate)
			if err != nil {
				t.Fatalf("exchangeCoinToChip(%d, %d) returned error: %v", coins, rate, err)
			}
			if backChips > start {
				t.Fatalf("chip round trip minted currency at rate %d: %d chips -> %d coins -> %d chips", rate, start, coins, backChips)
			}
		}
	}
}

func TestExchangeCoinToChip_OverflowGuard(t *testing.T) {
	const rate = maxRate
	threshold := int64(math.MaxInt64) / (rate * 97)

	if _, err := exchangeCoinToChip(threshold+1, rate); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("exchangeCoinToChip(%d, %d) = err %v, want ErrAmountOverflow", threshold+1, rate, err)
	}
	if _, err := exchangeCoinToChip(math.MaxInt64, rate); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("exchangeCoinToChip(MaxInt64, %d) = err %v, want ErrAmountOverflow", rate, err)
	}
	if _, err := exchangeCoinToChip(threshold, rate); err != nil {
		t.Fatalf("exchangeCoinToChip(%d, %d) returned %v, want the last in-range amount to be accepted", threshold, rate, err)
	}
}

func TestExchangeChipToCoin_OverflowGuard(t *testing.T) {
	const rate = minRate
	threshold := int64(math.MaxInt64) / 97

	if _, err := exchangeChipToCoin(threshold+1, rate); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("exchangeChipToCoin(%d, %d) = err %v, want ErrAmountOverflow", threshold+1, rate, err)
	}
	if _, err := exchangeChipToCoin(math.MaxInt64, rate); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("exchangeChipToCoin(MaxInt64, %d) = err %v, want ErrAmountOverflow", rate, err)
	}
	if _, err := exchangeChipToCoin(threshold, rate); err != nil {
		t.Fatalf("exchangeChipToCoin(%d, %d) returned %v, want the last in-range amount to be accepted", threshold, rate, err)
	}
}
