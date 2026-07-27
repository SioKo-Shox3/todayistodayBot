package casino

import "math"

// Rate model constants (設計書 §日次レート). baseRate is a guild's fixed
// day-1 starting point, minRate/maxRate are the clamp the rate can never
// escape, trendTransitionProb is the daily chance the hidden trend flips, and
// eventProb is the daily chance of a 暴騰/暴落 draw.
const (
	baseRate            = 100
	minRate             = 70
	maxRate             = 140
	trendTransitionProb = 0.15
	eventProb           = 0.05
)

// validRate reports whether rate is inside the [minRate, maxRate] clamp, whose
// bounds are both inclusive and both reachable. nextRate clamps every value it
// returns, so a rate failing this check never came from this package: it was
// hand-edited into data/casino.json, or the record was written partially (one
// with no "rate" field unmarshals as Rate 0). Guards against such values live
// in ensureTodayRateLocked (the source) and in the two exchange helpers below
// (defence in depth) — see their doc comments for the crash-loop this prevents.
func validRate(rate int) bool { return rate >= minRate && rate <= maxRate }

// nextRate computes tomorrow's rate, trend and event kind from today's
// rate/trend, using rng for all randomness. Pure — no I/O, no locking, no
// wall-clock reads. hasPrev=false means "this guild's very first day":
// returns exactly (baseRate, TrendFlat, EventNone) with NO randomness
// consumed (day 1 is a fixed starting point per 設計書 "基準100", not a
// random draw — a disclosed interpretation since the design doc doesn't
// state day-1 behavior explicitly).
//
// The returned EventKind is what makes the 🚀/💥 announcement reliable: it
// reports the DRAW, not the visible outcome, so a surge whose move was
// eaten by the 70/140 clamp is still reported as EventSurge.
//
// Precedence when hasPrev is true (disclosed interpretation of 設計書,
// which doesn't spell out interaction with the 15% roll): a boundary-forced
// transition REPLACES the 15% roll for that day (not "both apply") — if
// prevRate is at the 70/140 clamp, the trend is forced toward the center
// and the independent 15% transition check is skipped entirely for this
// call. Because the check is on prevRate, the force applies on every day the
// rate is still sitting on the boundary, not only the first.
func nextRate(prevRate int, prevTrend TrendState, hasPrev bool, rng randSource) (rate int, trend TrendState, event EventKind) {
	if !hasPrev {
		return baseRate, TrendFlat, EventNone
	}

	trend = prevTrend
	switch {
	case prevRate <= minRate:
		trend = TrendBull
	case prevRate >= maxRate:
		trend = TrendBear
	case rng.Float64() < trendTransitionProb:
		trend = otherTrend(prevTrend, rng)
	}

	var pct float64
	event = EventNone
	if rng.Float64() < eventProb {
		magnitude := 15 + rng.Float64()*15 // 15..30
		if rng.Float64() < 0.5 {
			pct, event = magnitude, EventSurge
		} else {
			pct, event = -magnitude, EventCrash
		}
	} else {
		var drift float64
		switch trend {
		case TrendBull:
			drift = 1 + rng.Float64()*3 // 1..4
		case TrendBear:
			drift = -(1 + rng.Float64()*3) // -1..-4
		case TrendFlat:
			drift = 0
		}
		noise := -3 + rng.Float64()*6 // -3..3
		pct = drift + noise
	}

	raw := float64(prevRate) * (1 + pct/100)
	clamped := math.Max(minRate, math.Min(maxRate, raw))
	return int(math.Round(clamped)), trend, event // math.Round = 四捨五入(半分は0から遠い方へ), never floor
}

// otherTrend picks uniformly among the two trend states that are not the
// current one, so a transition always actually transitions.
func otherTrend(current TrendState, rng randSource) TrendState {
	candidates := make([]TrendState, 0, 2)
	for _, t := range []TrendState{TrendBull, TrendBear, TrendFlat} {
		if t != current {
			candidates = append(candidates, t)
		}
	}
	return candidates[rng.Intn(len(candidates))]
}

// dailyBonusAmount returns the chip amount for a claim landing on the
// given streak (already incremented for today's claim; streak>=1).
// 200 + 50*(min(streak,7)-1), plus +500 "大入り袋" when streak%7==0.
func dailyBonusAmount(streak int) int64 {
	capped := streak
	if capped > 7 {
		capped = 7
	}
	amount := int64(200 + 50*(capped-1))
	if streak%7 == 0 {
		amount += 500
	}
	return amount
}

// exchangeCoinToChip converts coins to chips at rate, 3% fee applied on the
// output side, truncated via exactly ONE final integer division (設計書
// — do not compute an intermediate percentage step, which would
// double-truncate). Returns ErrAmountOverflow if coins*rate*97 would
// overflow int64. That guard is a defensive addition the design doc does not
// call for; under the MaxCoins economic cap it is unreachable in production
// (the threshold at rate 140 is ~6.79e14 against a 1e12 cap), and it is kept
// as the last line of defence for callers outside that cap.
//
// Returns ErrInvalidAmount — the package's existing "input outside the allowed
// domain" sentinel — for a rate outside the clamp, BEFORE any division. rate 0
// (what a persisted record with no "rate" field unmarshals to) would otherwise
// make math.MaxInt64/(rate*97) an integer division by zero, i.e. a panic that
// discordgo's handler goroutines do not recover from. ensureTodayRateLocked is
// the primary fix and keeps this unreachable through the store; this function
// is pure and package-wide, so it must not rely on its caller for safety.
func exchangeCoinToChip(coins int64, rate int) (int64, error) {
	if !validRate(rate) {
		return 0, ErrInvalidAmount
	}
	if coins > math.MaxInt64/(int64(rate)*97) {
		return 0, ErrAmountOverflow
	}
	return coins * int64(rate) * 97 / 100, nil
}

// exchangeChipToCoin converts chips to coins at rate, single final division.
// Rejects an out-of-clamp rate first, for the same reason exchangeCoinToChip
// does: here the zero divisor is the 100*rate of the payout expression itself.
func exchangeChipToCoin(chips int64, rate int) (int64, error) {
	if !validRate(rate) {
		return 0, ErrInvalidAmount
	}
	if chips > math.MaxInt64/97 {
		return 0, ErrAmountOverflow
	}
	return chips * 97 / (100 * int64(rate)), nil
}
