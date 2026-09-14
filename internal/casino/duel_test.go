package casino

import "testing"

// duelRand scripts the coin toss and range-checks the n it is asked for. The
// range check is the point: FlipDuel is only a fair coin while it draws
// Intn(2), so a script that answered an Intn(100) would quietly pass a test
// of a biased toss.
type duelRand struct {
	t     *testing.T
	rolls []int
	calls int
}

func (r *duelRand) Float64() float64 {
	r.t.Helper()
	r.t.Fatal("unexpected randSource.Float64 draw: a duel is one Intn(2) and nothing else")
	return 0
}

func (r *duelRand) Intn(n int) int {
	r.t.Helper()
	if n != 2 {
		r.t.Fatalf("FlipDuel drew Intn(%d), want Intn(2) — a duel must be a fair coin with no house edge (設計書 §2)", n)
	}
	if r.calls >= len(r.rolls) {
		r.t.Fatalf("unexpected randSource.Intn(2) call #%d: the script has only %d roll(s)", r.calls+1, len(r.rolls))
	}
	roll := r.rolls[r.calls]
	r.calls++
	if roll < 0 || roll >= n {
		r.t.Fatalf("scripted roll %d is outside [0,%d)", roll, n)
	}
	return roll
}

func TestFlipDuel_IsOneFairCoinDrawnFromTheInjectedSource(t *testing.T) {
	rng := &duelRand{t: t, rolls: []int{0, 1, 0, 1}}
	want := []bool{true, false, true, false}
	for i, w := range want {
		if got := FlipDuel(rng); got != w {
			t.Fatalf("toss #%d: FlipDuel = %v, want %v", i+1, got, w)
		}
	}
	if rng.calls != len(want) {
		t.Fatalf("FlipDuel drew %d times for %d tosses, want one draw each", rng.calls, len(want))
	}
}

func TestDuelPayout_WinnerTakesTheWholePotAndTheLoserTakesNothing(t *testing.T) {
	tests := []struct {
		name                       string
		bet                        int64
		challengerWins             bool
		wantChallenger, wantOppont int64
	}{
		{name: "challenger wins the pot", bet: 100, challengerWins: true, wantChallenger: 200, wantOppont: 0},
		{name: "opponent wins the pot", bet: 100, challengerWins: false, wantChallenger: 0, wantOppont: 200},
		{name: "minimum bet", bet: 1, challengerWins: true, wantChallenger: 2, wantOppont: 0},
		{name: "the cap itself still pays", bet: MaxChips, challengerWins: false, wantChallenger: 0, wantOppont: 2 * MaxChips},
		// Out of range pays nothing at all rather than a wrapped pot. Unreachable
		// through AcceptDuel, which rejects the bet before it gets here.
		{name: "zero bet pays nobody", bet: 0, challengerWins: true},
		{name: "negative bet pays nobody", bet: -100, challengerWins: false},
		{name: "above the cap pays nobody", bet: MaxChips + 1, challengerWins: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotChallenger, gotOpponent := DuelPayout(tc.bet, tc.challengerWins)
			if gotChallenger != tc.wantChallenger || gotOpponent != tc.wantOppont {
				t.Fatalf("DuelPayout(%d, %v) = (%d, %d), want (%d, %d)",
					tc.bet, tc.challengerWins, gotChallenger, gotOpponent, tc.wantChallenger, tc.wantOppont)
			}
		})
	}
}

// The payout table is what makes the duel zero-sum, so state that property
// over the table itself rather than only over the store: whatever the bet and
// whichever side wins, exactly one pot of 2 × bet leaves the table for the
// 2 × bet that was staked.
func TestDuelPayout_PaysOutExactlyWhatTheTwoStakesPutIn(t *testing.T) {
	for _, bet := range []int64{1, 10, 999, 1_000, 1_000_000, MaxChips} {
		for _, challengerWins := range []bool{true, false} {
			challengerPayout, opponentPayout := DuelPayout(bet, challengerWins)
			if got, want := challengerPayout+opponentPayout, 2*bet; got != want {
				t.Fatalf("DuelPayout(%d, %v) pays %d in total, want the two stakes %d",
					bet, challengerWins, got, want)
			}
		}
	}
}
