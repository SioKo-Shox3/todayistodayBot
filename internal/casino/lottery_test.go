package casino

import (
	"fmt"
	"testing"
	"time"
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

// TestLotteryDrawDate_KeysOffTheMostRecentNineAM pins the boundary the draw
// is scheduled on (設計書 C-3a §3: 毎日 9:00 JST). Keying it off the calendar
// date alone settles the pot at midnight, nine hours early — which both
// draws a ticket bought at noon the day before it was promised, and leaves a
// ticket bought at 08:00 in a pot that has already been settled, so the
// 09:00 the confirmation named passes it by.
func TestLotteryDrawDate_KeysOffTheMostRecentNineAM(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"just before 9am belongs to the previous day's draw", time.Date(2026, 7, 11, 8, 59, 59, 0, jst), "2026-07-10"},
		{"9am sharp is the new draw", time.Date(2026, 7, 11, 9, 0, 0, 0, jst), "2026-07-11"},
		{"a second past 9am is the new draw", time.Date(2026, 7, 11, 9, 0, 1, 0, jst), "2026-07-11"},
		{"noon is the new draw", time.Date(2026, 7, 11, 12, 0, 0, 0, jst), "2026-07-11"},
		{"midnight still belongs to the previous day", time.Date(2026, 7, 11, 0, 0, 0, 0, jst), "2026-07-10"},
		// Same instants, expressed in UTC: the host may run in any zone, so
		// the boundary must come from the JST projection and never from the
		// input's own location. 23:59:59Z on the 10th is 08:59:59 JST on the
		// 11th; 00:00:00Z on the 11th is 09:00 JST on the 11th.
		{"a UTC instant before the JST boundary", time.Date(2026, 7, 10, 23, 59, 59, 0, time.UTC), "2026-07-10"},
		{"a UTC instant at the JST boundary", time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC), "2026-07-11"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lotteryDrawDate(tc.at); got != tc.want {
				t.Fatalf("lotteryDrawDate(%s) = %q, want %q", tc.at.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

// TestNextLotteryDrawAt_NeverAnswersADrawThatHasAlreadyRun pins the reason
// the lottery does not use nextRunAt directly: a request whose clock reading
// predates the 09:00 draw it lands after must be told about the NEXT draw —
// the one its tickets are actually in — not the one that just settled.
func TestNextLotteryDrawAt_NeverAnswersADrawThatHasAlreadyRun(t *testing.T) {
	justBeforeNine := time.Date(2026, 9, 14, 8, 59, 59, 0, jst)
	tests := []struct {
		name         string
		lastDrawDate string
		now          time.Time
		want         time.Time
	}{
		// The bug: 09:00 on the 14th has already been drawn, so a reading
		// from one second earlier must still answer the 15th.
		{
			name: "today's draw already settled", lastDrawDate: "2026-09-14", now: justBeforeNine,
			want: time.Date(2026, 9, 15, 9, 0, 0, 0, jst),
		},
		// The ordinary case, unchanged: yesterday's draw is the last one, so
		// the schedule alone gives the right answer.
		{
			name: "yesterday's draw was the last", lastDrawDate: "2026-09-13", now: justBeforeNine,
			want: time.Date(2026, 9, 14, 9, 0, 0, 0, jst),
		},
		// Never drawn: nothing to push the answer past, so the plain schedule.
		{
			name: "never drawn", lastDrawDate: "", now: justBeforeNine,
			want: time.Date(2026, 9, 14, 9, 0, 0, 0, jst),
		},
		// The stored date is far in the PAST — the bot was down for a week.
		// The schedule is the later of the two and must win, or the answer
		// would be an instant days gone by.
		{
			name: "a stale draw date never drags the answer backwards", lastDrawDate: "2026-09-01", now: justBeforeNine,
			want: time.Date(2026, 9, 14, 9, 0, 0, 0, jst),
		},
		// Month end: the day after 09-30 is 10-01, not 09-31.
		{
			name: "the draw date is the last day of the month", lastDrawDate: "2026-09-30",
			now:  time.Date(2026, 9, 30, 8, 59, 59, 0, jst),
			want: time.Date(2026, 10, 1, 9, 0, 0, 0, jst),
		},
		// A hand-edited file cannot make the answer nonsense: an unparseable
		// date falls back to the schedule rather than to a zero time.
		{
			name: "a corrupt draw date falls back to the schedule", lastDrawDate: "not-a-date", now: justBeforeNine,
			want: time.Date(2026, 9, 14, 9, 0, 0, 0, jst),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextLotteryDrawAt(tc.lastDrawDate, tc.now)
			if !got.Equal(tc.want) {
				t.Fatalf("nextLotteryDrawAt(%q, %s) = %s, want %s",
					tc.lastDrawDate, tc.now.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}
