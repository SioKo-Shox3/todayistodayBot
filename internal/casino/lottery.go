package casino

import "sort"

// The daily lottery's constants (設計書 C-3a §3). One ticket is 50 chips, a
// buyer may hold at most 10 tickets in any one draw, and 90% of the takings
// go to the single winner — the remaining 10% is the house's cut and is fed
// straight into the slot jackpot pool rather than disappearing, so the two
// C-3a features share one pot of house money.
const (
	LotteryTicketPrice       int64 = 50 // chips per ticket
	LotteryMaxTicketsPerDraw int   = 10 // per buyer, per draw (cumulative)
	LotteryPrizePercent      int64 = 90 // percent of sales paid to the winner
)

// LotteryPrize splits `sales` into the winner's prize and the house's cut,
// and adds `carryover` (the pot rolled forward from draws nobody entered) to
// the prize: prize = floor(sales*90/100) + carryover, house = sales - that
// same floor. Taking the house cut as a REMAINDER rather than as its own
// floor(sales*10/100) is what makes the split exact — the truncated fraction
// lands in the house's share instead of vanishing, so prize + house is
// always sales + carryover, whatever sales is.
//
// Negative inputs (reachable only from a hand-edited file) are read as 0:
// Go's / truncates toward zero, so a negative sales would otherwise hand
// back a negative prize and a house cut that SHRINKS the jackpot pool.
//
// Cannot overflow: sales is chips that were actually debited from accounts,
// so it is bounded by MaxChips (1e12), and 1e12*90 = 9e13 is about 102,000x
// below math.MaxInt64.
func LotteryPrize(sales, carryover int64) (prize, house int64) {
	if sales < 0 {
		sales = 0
	}
	if carryover < 0 {
		carryover = 0
	}
	share := sales * LotteryPrizePercent / 100
	return share + carryover, sales - share
}

// PickLotteryWinner draws one winner from `tickets` (userID -> tickets held),
// weighted by ticket count: holding 3 of the 10 tickets sold wins 3 times as
// often as holding 1. Returns "" when there is nothing to draw from — no
// buyers, or only non-positive counts — and the caller must treat that as
// "no draw happened" rather than as a winner with an empty ID.
//
// The walk is over user IDs sorted ASCENDING, not over the map, so one
// rng.Intn draw always maps to the same winner: Go randomises map iteration
// order on purpose, and without the sort the same seeded rng would pick a
// different user on every run — the draw would be unreproducible and no test
// could pin it. Non-positive counts are skipped rather than clamped so they
// cannot claim a slice of the range they contribute nothing to.
func PickLotteryWinner(tickets map[string]int, rng randSource) string {
	ids := make([]string, 0, len(tickets))
	total := 0
	for id, n := range tickets {
		if n <= 0 {
			continue
		}
		// Guard the sum rather than the individual counts: BuyLotteryTickets
		// caps every holding at LotteryMaxTicketsPerDraw, so only a hand
		// -edited file can reach anywhere near this, and a wrapped total
		// would make Intn panic on a negative argument — in a discordgo
		// handler goroutine, which has no recover, that kills the bot.
		if n > maxInt-total {
			return ""
		}
		ids = append(ids, id)
		total += n
	}
	if total <= 0 {
		return ""
	}
	sort.Strings(ids)
	roll := rng.Intn(total)
	cumulative := 0
	for _, id := range ids {
		cumulative += tickets[id]
		if roll < cumulative {
			return id
		}
	}
	// Unreachable: roll < total == cumulative after the last id. Returning
	// the last holder rather than "" keeps a broken randSource (one whose
	// Intn hands back a value outside [0, n)) from silently voiding a draw
	// that had real buyers.
	return ids[len(ids)-1]
}

// maxInt is the largest int on this platform — 2^63-1 on every target this
// bot builds for, but derived rather than assumed so a 32-bit build keeps
// PickLotteryWinner's overflow guard correct.
const maxInt = int(^uint(0) >> 1)
