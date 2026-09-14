package casino

import (
	"fmt"
	"testing"
)

// lotteryRand scripts the Intn draws the lottery winner is picked with, and
// answers Float64 from a fixed value so a store-level test does not also have
// to script the daily-rate walk that runs in the same rollover. Every roll is
// range-checked against the n it is asked for: a script that hands back a
// value outside [0, n) would silently exercise PickLotteryWinner's
// unreachable tail instead of the weighting under test.
type lotteryRand struct {
	t     *testing.T
	float float64
	rolls []int
	calls int
}

func (r *lotteryRand) Float64() float64 { return r.float }

func (r *lotteryRand) Intn(n int) int {
	r.t.Helper()
	if r.calls >= len(r.rolls) {
		r.t.Fatalf("unexpected randSource.Intn(%d) call #%d: the script has only %d roll(s) — a draw happened that the test did not expect", n, r.calls+1, len(r.rolls))
	}
	roll := r.rolls[r.calls]
	r.calls++
	if roll < 0 || roll >= n {
		r.t.Fatalf("scripted roll %d is outside [0,%d) — the ticket total the draw was run over is not what the test set up", roll, n)
	}
	return roll
}

func TestLotteryPrize_SplitsSalesAndAddsTheCarryover(t *testing.T) {
	tests := []struct {
		name                 string
		sales, carryover     int64
		wantPrize, wantHouse int64
	}{
		// The plain case: 10 tickets at 50 chips. 90% to the winner.
		{name: "ten tickets, no carryover", sales: 500, carryover: 0, wantPrize: 450, wantHouse: 50},
		{name: "ten tickets, with carryover", sales: 500, carryover: 1000, wantPrize: 1450, wantHouse: 50},
		// One ticket: 50*90/100 = 45 exactly, house keeps 5.
		{name: "single ticket", sales: 50, carryover: 0, wantPrize: 45, wantHouse: 5},
		// Nobody bought: the whole carryover rolls forward and the house
		// takes nothing — this is the path drawLotteryLocked uses to decide
		// what a buyer-less day carries over.
		{name: "no sales at all", sales: 0, carryover: 777, wantPrize: 777, wantHouse: 0},
		// The truncated fraction must land in the HOUSE's share, never
		// vanish: 1 chip of sales pays 0 and the house keeps all 1, so
		// prize+house == sales+carryover still holds at the worst rounding.
		{name: "rounding remainder goes to the house", sales: 1, carryover: 0, wantPrize: 0, wantHouse: 1},
		{name: "rounding remainder, larger", sales: 1234, carryover: 0, wantPrize: 1110, wantHouse: 124},
		// Hand-edited junk reads as 0 rather than shrinking the jackpot pool
		// (Go's / truncates toward zero, so a negative sales would produce a
		// negative house cut).
		{name: "negative sales read as zero", sales: -500, carryover: 100, wantPrize: 100, wantHouse: 0},
		{name: "negative carryover read as zero", sales: 500, carryover: -100, wantPrize: 450, wantHouse: 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prize, house := LotteryPrize(tt.sales, tt.carryover)
			if prize != tt.wantPrize || house != tt.wantHouse {
				t.Fatalf("LotteryPrize(%d, %d) = (prize=%d, house=%d), want (prize=%d, house=%d)",
					tt.sales, tt.carryover, prize, house, tt.wantPrize, tt.wantHouse)
			}
			// The invariant behind the table: nothing is created and nothing
			// is destroyed by the split.
			sales, carryover := tt.sales, tt.carryover
			if sales < 0 {
				sales = 0
			}
			if carryover < 0 {
				carryover = 0
			}
			if prize+house != sales+carryover {
				t.Fatalf("prize+house = %d, want %d (= sales+carryover): the split lost or invented chips",
					prize+house, sales+carryover)
			}
		})
	}
}

// TestPickLotteryWinner_WeightsByTicketCountOverAscendingIDs pins the whole
// mapping from roll to winner: with 1/3/6 tickets the ranges are u1 [0,1),
// u2 [1,4), u3 [4,10) — i.e. weighted by count, walked in ascending user-ID
// order. Both halves matter: the weighting is the rule, and the ordering is
// what makes the rule reproducible (Go randomises map iteration).
func TestPickLotteryWinner_WeightsByTicketCountOverAscendingIDs(t *testing.T) {
	want := []string{"u1", "u2", "u2", "u2", "u3", "u3", "u3", "u3", "u3", "u3"}

	for roll, wantWinner := range want {
		tickets := map[string]int{"u2": 3, "u3": 6, "u1": 1} // insertion order deliberately not sorted
		got := PickLotteryWinner(tickets, &lotteryRand{t: t, rolls: []int{roll}})
		if got != wantWinner {
			t.Fatalf("PickLotteryWinner with roll %d = %q, want %q (ranges: u1 [0,1), u2 [1,4), u3 [4,10))",
				roll, got, wantWinner)
		}
	}
}

// TestPickLotteryWinner_IsDeterministicAcrossRuns is the regression that
// catches a walk over the map itself: Go randomises map iteration order per
// range statement, so an unsorted implementation returns a DIFFERENT winner
// for the same roll on repeated calls — the draw would be unreproducible and
// the test above would only pass by luck.
func TestPickLotteryWinner_IsDeterministicAcrossRuns(t *testing.T) {
	const roll = 5 // inside u3's range
	for i := 0; i < 200; i++ {
		tickets := map[string]int{}
		// Build the map in a different order every iteration, so no single
		// insertion order can be the one the implementation happens to like.
		for _, id := range []string{"u1", "u2", "u3", "u4", "u5"} {
			tickets[fmt.Sprintf("%s-%d", id, i%3)] = 0 // decoys: zero tickets never win
		}
		tickets["u1"], tickets["u2"], tickets["u3"] = 1, 3, 6
		if got := PickLotteryWinner(tickets, &lotteryRand{t: t, rolls: []int{roll}}); got != "u3" {
			t.Fatalf("iteration %d: PickLotteryWinner with roll %d = %q, want %q — the walk is not over sorted user IDs",
				i, roll, got, "u3")
		}
	}
}

// TestPickLotteryWinner_NoEligibleTickets reports "" rather than naming a
// user who bought nothing. drawLotteryLocked reads that as "nobody entered"
// and rolls the pot forward, so the distinction is what keeps a quiet day
// from paying a prize to an empty user ID.
func TestPickLotteryWinner_NoEligibleTickets(t *testing.T) {
	forbidden := &lotteryRand{t: t} // any Intn call fails: there is nothing to draw from
	for _, tickets := range []map[string]int{
		nil,
		{},
		{"u1": 0},
		{"u1": 0, "u2": -3}, // hand-edited junk
	} {
		if got := PickLotteryWinner(tickets, forbidden); got != "" {
			t.Fatalf("PickLotteryWinner(%v) = %q, want \"\"", tickets, got)
		}
	}
}
