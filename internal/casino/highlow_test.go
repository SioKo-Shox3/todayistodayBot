package casino

import (
	"errors"
	"math/rand"
	"testing"
)

// card is shorthand for the fixed decks below. The suit never decides
// anything in ハイ&ロー (only Rank is compared), so every fixed card is a
// spade unless a test needs two cards of the same rank to be distinguishable.
func card(rank int) Card { return Card{Rank: rank, Suit: SuitSpade} }

// highLowWith builds a board in mid-game: the tests that pin the pot cap and
// the streak cap need a starting pot and streak that would take a dozen real
// presses to reach. Same package, so this is the honest way to state "given a
// board in THIS state" instead of driving it there through the rules under
// test.
func highLowWith(bet, pot int64, streak int, current Card, deck *Deck) *HighLowGame {
	return &HighLowGame{bet: bet, pot: pot, streak: streak, current: current, deck: deck, state: HighLowPlaying}
}

func TestHighLow_NewDealsFromAFullShuffledDeck(t *testing.T) {
	game := NewHighLow(500, rand.New(rand.NewSource(3)))

	if game.State() != HighLowPlaying {
		t.Fatalf("State() = %v, want HighLowPlaying", game.State())
	}
	// The opening card comes OUT of the deck: counting it as still in there
	// would let the odds display promise a card that can never be drawn.
	if got := game.deck.Len(); got != 51 {
		t.Fatalf("deck has %d cards after the deal, want 51", got)
	}
	if game.Current().Rank < MinRank || game.Current().Rank > MaxRank {
		t.Fatalf("Current() = %v, want a rank in %d..%d", game.Current(), MinRank, MaxRank)
	}
	if game.Pot() != 500 || game.Bet() != 500 {
		t.Fatalf("Pot()/Bet() = %d/%d, want 500/500 (the pot starts at the bet)", game.Pot(), game.Bet())
	}
	if game.Streak() != 0 {
		t.Fatalf("Streak() = %d, want 0", game.Streak())
	}
}

func TestHighLow_MultiplierIsTheFairOddsCutToNinetyFivePercent(t *testing.T) {
	tests := []struct {
		name      string
		winning   int
		remaining int
		want      int64
	}{
		{name: "every card wins pays the house cut", winning: 51, remaining: 51, want: 95},
		{name: "one card in 51", winning: 1, remaining: 51, want: 4845},
		{name: "28 of 51 truncates 173.03 down", winning: 28, remaining: 51, want: 173},
		{name: "2 of 3 truncates 142.5 down", winning: 2, remaining: 3, want: 142},
		{name: "no winning card is unselectable", winning: 0, remaining: 51, want: 0},
		{name: "negative winning count is unselectable", winning: -1, remaining: 51, want: 0},
		{name: "an empty deck is unselectable", winning: 3, remaining: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Multiplier(tt.winning, tt.remaining); got != tt.want {
				t.Fatalf("Multiplier(%d, %d) = %d, want %d", tt.winning, tt.remaining, got, tt.want)
			}
		})
	}
}

func TestHighLow_OddsCountTiesOnNeitherSide(t *testing.T) {
	// Current 7; remaining: 9 and 10 are high, 3 is low, the two 7s are ties
	// and belong to neither numerator — but to the denominator.
	game := highLowWith(100, 100, 0, card(7), deckOf(card(9), card(3), card(7), card(10), Card{Rank: 7, Suit: SuitHeart}))

	odds := game.Odds()
	if odds.Remaining != 5 {
		t.Fatalf("Remaining = %d, want 5", odds.Remaining)
	}
	if odds.HighCards != 2 || odds.LowCards != 1 {
		t.Fatalf("HighCards/LowCards = %d/%d, want 2/1 (the two 7s are ties)", odds.HighCards, odds.LowCards)
	}
	// 95*5/2 = 237.5 -> 237, 95*5/1 = 475.
	if odds.HighMultiplier != 237 || odds.LowMultiplier != 475 {
		t.Fatalf("multipliers = %d/%d, want 237/475", odds.HighMultiplier, odds.LowMultiplier)
	}
}

func TestHighLow_GuessGrowsThePotByTheOfferedMultiplier(t *testing.T) {
	// Remaining 9 and 3 against a 7: one high card in two, so ハイ pays
	// 95*2/1 = 190 (1.90x).
	game := highLowWith(1000, 1000, 0, card(7), deckOf(card(9), card(3)))

	step, err := game.Guess(true)
	if err != nil {
		t.Fatalf("Guess returned error: %v", err)
	}
	if !step.Won || step.Finished {
		t.Fatalf("step = %+v, want a win that leaves the game running", step)
	}
	if step.Multiplier != 190 {
		t.Fatalf("Multiplier = %d, want 190", step.Multiplier)
	}
	if step.Pot != 1900 || game.Pot() != 1900 {
		t.Fatalf("pot = %d (step) / %d (game), want 1900", step.Pot, game.Pot())
	}
	if step.Previous != card(7) || step.Card != card(9) {
		t.Fatalf("step cards = %v -> %v, want 7♠ -> 9♠", step.Previous, step.Card)
	}
	// The drawn card becomes the one the next guess is measured against, and
	// it is gone from the deck the odds are computed over.
	if game.Current() != card(9) {
		t.Fatalf("Current() = %v, want 9♠", game.Current())
	}
	if game.Streak() != 1 {
		t.Fatalf("Streak() = %d, want 1", game.Streak())
	}
	if got := game.Odds().Remaining; got != 1 {
		t.Fatalf("Odds().Remaining = %d, want 1", got)
	}
}

func TestHighLow_LowGuessGrowsThePotAndAdvancesTheBoard(t *testing.T) {
	// The mirror of the ハイ case above: remaining 3 and 9 against a 7, so
	// ロー has one winning card in two and pays 95*2/1 = 190 (1.90x). Both
	// directions need a fixed deck — a ロー win that advanced the board
	// wrongly (kept the old current card, or counted the streak on the wrong
	// side) would be invisible in a ハイ-only test.
	game := highLowWith(1000, 1000, 0, card(7), deckOf(card(3), card(9)))

	step, err := game.Guess(false)
	if err != nil {
		t.Fatalf("Guess returned error: %v", err)
	}
	if !step.Won || step.Finished {
		t.Fatalf("step = %+v, want a win that leaves the game running", step)
	}
	if step.Multiplier != 190 {
		t.Fatalf("Multiplier = %d, want 190", step.Multiplier)
	}
	if step.Pot != 1900 || game.Pot() != 1900 {
		t.Fatalf("pot = %d (step) / %d (game), want 1900", step.Pot, game.Pot())
	}
	if step.Previous != card(7) || step.Card != card(3) {
		t.Fatalf("step cards = %v -> %v, want 7♠ -> 3♠", step.Previous, step.Card)
	}
	if game.Current() != card(3) {
		t.Fatalf("Current() = %v, want 3♠ (the drawn card becomes the current one)", game.Current())
	}
	if step.Streak != 1 || game.Streak() != 1 {
		t.Fatalf("streak = %d (step) / %d (game), want 1", step.Streak, game.Streak())
	}
	// The next guess is measured against the 3, so the remaining 9 is high
	// and nothing is low — ロー must now be the unselectable side.
	odds := game.Odds()
	if odds.Remaining != 1 || odds.HighCards != 1 || odds.LowCards != 0 {
		t.Fatalf("odds after the win = %+v, want Remaining 1 / High 1 / Low 0", odds)
	}
	if odds.LowMultiplier != 0 {
		t.Fatalf("LowMultiplier = %d, want 0 (no card can win ロー against a 3)", odds.LowMultiplier)
	}
}

func TestHighLow_PotTruncatesDown(t *testing.T) {
	// Every remaining card wins, so the multiplier is exactly the house cut:
	// 1001 * 0.95 = 950.95, and the player is paid 950.
	game := highLowWith(1001, 1001, 0, card(2), deckOf(card(5), card(9)))

	step, err := game.Guess(true)
	if err != nil {
		t.Fatalf("Guess returned error: %v", err)
	}
	if step.Multiplier != 95 {
		t.Fatalf("Multiplier = %d, want 95", step.Multiplier)
	}
	if step.Pot != 950 {
		t.Fatalf("Pot = %d, want 950 (950.95 truncated toward the house)", step.Pot)
	}
}

func TestHighLow_LosingGuessEndsTheGameWithNothing(t *testing.T) {
	tests := []struct {
		name  string
		high  bool
		deck  *Deck
		drawn Card
	}{
		// Each deck holds one card that would have won the guess, so the
		// guess itself is legal and the loss is the rules, not a refusal.
		{name: "lower card loses ハイ", high: true, deck: deckOf(card(4), card(9)), drawn: card(4)},
		{name: "higher card loses ロー", high: false, deck: deckOf(card(9), card(4)), drawn: card(9)},
		{name: "the same rank loses ハイ", high: true, deck: deckOf(card(7), card(9)), drawn: card(7)},
		{name: "the same rank loses ロー", high: false, deck: deckOf(card(7), card(4)), drawn: card(7)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			game := highLowWith(1000, 4000, 3, card(7), tt.deck)

			step, err := game.Guess(tt.high)
			if err != nil {
				t.Fatalf("Guess returned error: %v", err)
			}
			if step.Won {
				t.Fatalf("step.Won = true for %v against 7♠", tt.drawn)
			}
			if step.Card != tt.drawn {
				t.Fatalf("step.Card = %v, want %v", step.Card, tt.drawn)
			}
			if !step.Finished || step.AutoCashOut {
				t.Fatalf("step = %+v, want Finished without AutoCashOut", step)
			}
			if step.Pot != 0 || step.Payout != 0 || game.Pot() != 0 {
				t.Fatalf("pot/payout = %d/%d (game %d), want 0/0/0 — a loss keeps nothing", step.Pot, step.Payout, game.Pot())
			}
			if game.State() != HighLowLost {
				t.Fatalf("State() = %v, want HighLowLost", game.State())
			}
		})
	}
}

func TestHighLow_GuessNoCardCanWinIsRefused(t *testing.T) {
	tests := []struct {
		name    string
		current Card
		high    bool
	}{
		{name: "ハイ on an ace", current: card(14), high: true},
		{name: "ロー on a two", current: card(2), high: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			game := highLowWith(1000, 2000, 1, tt.current, deckOf(card(5), card(9), card(12)))

			odds := game.Odds()
			winning, multiplier := odds.LowCards, odds.LowMultiplier
			if tt.high {
				winning, multiplier = odds.HighCards, odds.HighMultiplier
			}
			if winning != 0 || multiplier != 0 {
				t.Fatalf("odds = %d cards / multiplier %d, want 0/0 (the button must be disabled)", winning, multiplier)
			}

			_, err := game.Guess(tt.high)
			if !errors.Is(err, ErrChoiceUnavailable) {
				t.Fatalf("Guess = %v, want ErrChoiceUnavailable", err)
			}
			// A refusal must not spend a card or touch the money: the player
			// presses the other button next.
			if game.Pot() != 2000 || game.Streak() != 1 || game.Current() != tt.current {
				t.Fatalf("board moved on a refusal: pot %d, streak %d, current %v", game.Pot(), game.Streak(), game.Current())
			}
			if game.deck.Len() != 3 || game.State() != HighLowPlaying {
				t.Fatalf("deck %d cards, state %v — want 3 and HighLowPlaying", game.deck.Len(), game.State())
			}
		})
	}
}

func TestHighLow_TenWinsCashOutAutomatically(t *testing.T) {
	// An ascending run: every card left is higher than the current one, so
	// ハイ always pays exactly the house cut (0.95x) and the pot shrinks —
	// the streak cap, not the pot cap, is what ends this game.
	game := highLowWith(1000, 1000, 0, card(2), deckOf(
		card(3), card(4), card(5), card(6), card(7),
		card(8), card(9), card(10), card(11), card(12),
	))

	var last StepResult
	for i := 1; i <= highLowMaxStreak; i++ {
		step, err := game.Guess(true)
		if err != nil {
			t.Fatalf("guess %d returned error: %v", i, err)
		}
		if !step.Won {
			t.Fatalf("guess %d lost against an ascending deck", i)
		}
		if step.Finished != (i == highLowMaxStreak) {
			t.Fatalf("guess %d Finished = %v, want %v", i, step.Finished, i == highLowMaxStreak)
		}
		last = step
	}

	// 1000 * 0.95 ten times, truncated at every step:
	// 950, 902, 856, 813, 772, 733, 696, 661, 627, 595.
	if last.Pot != 595 || last.Payout != 595 {
		t.Fatalf("final pot/payout = %d/%d, want 595/595", last.Pot, last.Payout)
	}
	if !last.AutoCashOut || last.Streak != highLowMaxStreak {
		t.Fatalf("final step = %+v, want AutoCashOut at streak %d", last, highLowMaxStreak)
	}
	if game.State() != HighLowCashedOut {
		t.Fatalf("State() = %v, want HighLowCashedOut", game.State())
	}
	// The eleventh press lands on a finished board and must pay nothing.
	if _, err := game.Guess(true); !errors.Is(err, ErrNoGameInProgress) {
		t.Fatalf("Guess after the streak cap = %v, want ErrNoGameInProgress", err)
	}
	if game.CashOut() != 0 {
		t.Fatal("CashOut after an automatic cash-out paid a second time")
	}
}

func TestHighLow_PotCapIsPaidAtTheCap(t *testing.T) {
	// bet 10 caps the pot at 1,000. A 1.90x win on a pot of 900 computes
	// 1,710 — the player is paid the cap, and the game ends there.
	game := highLowWith(10, 900, 3, card(7), deckOf(card(9), card(3)))

	step, err := game.Guess(true)
	if err != nil {
		t.Fatalf("Guess returned error: %v", err)
	}
	if !step.Won || !step.Finished || !step.AutoCashOut {
		t.Fatalf("step = %+v, want a win that auto-cashes out", step)
	}
	if step.Pot != 1000 || step.Payout != 1000 || game.Pot() != 1000 {
		t.Fatalf("pot/payout = %d/%d (game %d), want the 1000 cap", step.Pot, step.Payout, game.Pot())
	}
	if step.Streak != 4 {
		t.Fatalf("Streak = %d, want 4", step.Streak)
	}
	if game.State() != HighLowCashedOut {
		t.Fatalf("State() = %v, want HighLowCashedOut", game.State())
	}
}

func TestHighLow_CashOutBeforePlayingRefundsTheWholeBet(t *testing.T) {
	game := NewHighLow(750, rand.New(rand.NewSource(11)))

	if got := game.CashOut(); got != 750 {
		t.Fatalf("CashOut() = %d, want the whole bet (750)", got)
	}
	if game.State() != HighLowCashedOut {
		t.Fatalf("State() = %v, want HighLowCashedOut", game.State())
	}
	// Settling twice would pay the bet out of nothing; the board refuses
	// before the Store's ErrNoGameInProgress ever has to.
	if got := game.CashOut(); got != 0 {
		t.Fatalf("second CashOut() = %d, want 0", got)
	}
	if _, err := game.Guess(true); !errors.Is(err, ErrNoGameInProgress) {
		t.Fatalf("Guess after cashing out = %v, want ErrNoGameInProgress", err)
	}
}

func TestHighLow_CashOutPaysThePotAfterAWin(t *testing.T) {
	game := highLowWith(1000, 1000, 0, card(7), deckOf(card(9), card(3)))

	if _, err := game.Guess(true); err != nil {
		t.Fatalf("Guess returned error: %v", err)
	}
	if got := game.CashOut(); got != 1900 {
		t.Fatalf("CashOut() = %d, want the pot (1900)", got)
	}
}

func TestHighLow_CashOutAfterALossPaysNothing(t *testing.T) {
	game := highLowWith(1000, 1000, 0, card(7), deckOf(card(3), card(9)))

	if _, err := game.Guess(true); err != nil {
		t.Fatalf("Guess returned error: %v", err)
	}
	if got := game.CashOut(); got != 0 {
		t.Fatalf("CashOut() after a loss = %d, want 0", got)
	}
}

func TestHighLow_AutoResolvePaysWhatACashOutWould(t *testing.T) {
	// A timed-out board and a pressed 💰 button must settle for the same
	// number — an abandoned game is resolved in the player's favour.
	abandoned := highLowWith(1000, 1000, 0, card(7), deckOf(card(9), card(3)))
	pressed := highLowWith(1000, 1000, 0, card(7), deckOf(card(9), card(3)))
	if _, err := abandoned.Guess(true); err != nil {
		t.Fatalf("Guess returned error: %v", err)
	}
	if _, err := pressed.Guess(true); err != nil {
		t.Fatalf("Guess returned error: %v", err)
	}

	auto, manual := abandoned.AutoResolve(), pressed.CashOut()
	if auto != manual || auto != 1900 {
		t.Fatalf("AutoResolve/CashOut = %d/%d, want both 1900", auto, manual)
	}
	if abandoned.State() != HighLowCashedOut {
		t.Fatalf("State() = %v after AutoResolve, want HighLowCashedOut", abandoned.State())
	}
	if abandoned.AutoResolve() != 0 {
		t.Fatal("a second AutoResolve paid again")
	}
}

// TestHighLow_PerStepReturnIsNinetyFivePercent is the statistical half of the
// contract: whatever a player presses, one guess returns 95 % of what it
// risked (設計書 §6: 1 手あたり RTP 95 %). A seeded source makes the number
// reproducible, so a failure is a real regression in the multiplier, never
// flakiness. The bet is large so that the per-step truncation (always toward
// the house) is a rounding detail rather than the result.
func TestHighLow_PerStepReturnIsNinetyFivePercent(t *testing.T) {
	const (
		hands = 1_000_000
		bet   = int64(1_000_000)
	)
	rng := rand.New(rand.NewSource(20260914))

	var staked, returned int64
	for i := 0; i < hands; i++ {
		game := NewHighLow(bet, rng)
		odds := game.Odds()
		// Press either button, falling back to the other when the opening
		// card makes one of them impossible (an ace or a two).
		high := rng.Intn(2) == 0
		if high && odds.HighCards == 0 {
			high = false
		} else if !high && odds.LowCards == 0 {
			high = true
		}
		step, err := game.Guess(high)
		if err != nil {
			t.Fatalf("hand %d: Guess returned error: %v", i, err)
		}
		staked += bet
		returned += step.Pot // 0 on a loss
	}

	rtp := float64(returned) / float64(staked)
	if rtp < 0.94 || rtp > 0.96 {
		t.Fatalf("per-step RTP = %.4f over %d hands, want 0.95 ±0.01", rtp, hands)
	}
	t.Logf("per-step RTP = %.4f over %d hands (staked %d, returned %d)", rtp, hands, staked, returned)
}
