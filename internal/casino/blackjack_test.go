package casino

import (
	"errors"
	"math/rand"
	"testing"
)

// blackjackWith builds a hand in mid-play: the tests that pin the dealer's
// strategy and the double need a specific pair of starting hands that a real
// deal would take dozens of shuffles to produce. Same package, so this is the
// honest way to say "given THESE two hands" instead of driving the rules under
// test to reach them. The shoe holds only the cards the test expects to be
// drawn — an unexpected extra draw therefore panics rather than passing.
func blackjackWith(bet int64, player, dealer []Card, shoe *Deck) *BlackjackGame {
	return &BlackjackGame{bet: bet, player: player, dealer: dealer, shoe: shoe, state: BlackjackPlaying}
}

func TestBlackjack_NewDealsFourCardsFromASixDeckShoe(t *testing.T) {
	game := NewBlackjack(500, rand.New(rand.NewSource(7)))

	// 6*52 = 312, four of them dealt: the dealt cards are OUT of the shoe, or
	// a later hit could draw a card already lying on the table.
	if got := game.shoe.Len(); got != 308 {
		t.Fatalf("shoe has %d cards after the deal, want 308", got)
	}
	if len(game.Player()) != 2 || len(game.Dealer()) != 2 {
		t.Fatalf("hands = %v / %v, want two cards each", game.Player(), game.Dealer())
	}
	if game.Bet() != 500 || game.TotalBet() != 500 {
		t.Fatalf("Bet()/TotalBet() = %d/%d, want 500/500", game.Bet(), game.TotalBet())
	}
	if game.Doubled() {
		t.Fatalf("Doubled() = true on a fresh hand")
	}
	// A natural would legitimately finish this hand at the deal; anything else
	// must be live and must offer the double.
	if isNatural(game.Player()) || isNatural(game.Dealer()) {
		return
	}
	if game.State() != BlackjackPlaying || game.Result() != BlackjackUndecided {
		t.Fatalf("state/result = %v/%v, want BlackjackPlaying/BlackjackUndecided", game.State(), game.Result())
	}
	if !game.CanDouble() {
		t.Fatalf("CanDouble() = false on the first decision")
	}
}

func TestBlackjack_NaturalsAreResolvedAtTheDeal(t *testing.T) {
	// Cards come out in table order: player, dealer, player, dealer.
	tests := []struct {
		name       string
		shoe       *Deck
		wantResult BlackjackResult
		wantPayout int64 // bet is 1001 everywhere, so the 3:2 floor is visible
	}{
		{
			// Player A♠ K♠ = 21, dealer A♠ Q♠ = 21: nobody is paid.
			name:       "both naturals push",
			shoe:       deckOf(card(14), card(14), card(13), card(12)),
			wantResult: BlackjackPush,
			wantPayout: 1001,
		},
		{
			// Player A♠ K♠ = 21 against a dealer 16: 3:2, so the stake back
			// plus floor(1001*3/2) = 1501.
			name:       "the player alone is paid three to two",
			shoe:       deckOf(card(14), card(9), card(13), card(7)),
			wantResult: BlackjackNatural,
			wantPayout: 2502,
		},
		{
			// Dealer A♠ Q♠ = 21 against a player 16: an immediate loss, with
			// no chance to hit into a 21 that would not have counted anyway.
			name:       "the dealer alone wins immediately",
			shoe:       deckOf(card(9), card(14), card(7), card(12)),
			wantResult: BlackjackDealerWin,
			wantPayout: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			game := newBlackjackFromShoe(1001, tt.shoe)

			if game.State() != BlackjackFinished {
				t.Fatalf("State() = %v, want BlackjackFinished", game.State())
			}
			if game.Result() != tt.wantResult {
				t.Fatalf("Result() = %v, want %v", game.Result(), tt.wantResult)
			}
			if got := game.Settle(); got != tt.wantPayout {
				t.Fatalf("Settle() = %d, want %d", got, tt.wantPayout)
			}
			// A hand decided at the deal has no buttons: the presses on the
			// message that announced it must not reopen it.
			if game.CanDouble() {
				t.Fatalf("CanDouble() = true on a hand finished at the deal")
			}
			if _, err := game.Hit(); !errors.Is(err, ErrNoGameInProgress) {
				t.Fatalf("Hit() error = %v, want ErrNoGameInProgress", err)
			}
			if err := game.Stand(); !errors.Is(err, ErrNoGameInProgress) {
				t.Fatalf("Stand() error = %v, want ErrNoGameInProgress", err)
			}
		})
	}
}

func TestBlackjack_ThreeCardTwentyOneIsNotANatural(t *testing.T) {
	// Player 7 7 against a dealer 20, hitting to exactly 21: the win pays 2x,
	// not 2.5x. Reaching 21 late is not what the 3:2 is for.
	game := blackjackWith(1000, []Card{card(7), card(7)}, []Card{card(10), card(10)}, deckOf(card(7)))

	if _, err := game.Hit(); err != nil {
		t.Fatalf("Hit returned error: %v", err)
	}
	if game.PlayerValue() != 21 {
		t.Fatalf("PlayerValue() = %d, want 21", game.PlayerValue())
	}
	if err := game.Stand(); err != nil {
		t.Fatalf("Stand returned error: %v", err)
	}
	if game.Result() != BlackjackPlayerWin {
		t.Fatalf("Result() = %v, want BlackjackPlayerWin", game.Result())
	}
	if got := game.Settle(); got != 2000 {
		t.Fatalf("Settle() = %d, want 2000 (2x the bet, not the 3:2 natural)", got)
	}
}

func TestBlackjack_PlayerBustEndsTheHandAndRefusesFurtherPresses(t *testing.T) {
	// 16 plus a ten. The shoe holds ONE card, so a dealer that wrongly drew
	// against the bust would panic on the empty shoe.
	game := blackjackWith(1000, []Card{card(10), card(6)}, []Card{card(10), card(7)}, deckOf(card(10)))

	drawn, err := game.Hit()
	if err != nil {
		t.Fatalf("Hit returned error: %v", err)
	}
	if drawn != card(10) || game.PlayerValue() != 26 {
		t.Fatalf("drew %v to a value of %d, want 10♠ and 26", drawn, game.PlayerValue())
	}
	if game.State() != BlackjackFinished || game.Result() != BlackjackDealerWin {
		t.Fatalf("state/result = %v/%v, want BlackjackFinished/BlackjackDealerWin", game.State(), game.Result())
	}
	if got := game.Settle(); got != 0 {
		t.Fatalf("Settle() = %d, want 0", got)
	}
	// The dealer does not play against a bust — the hole card stays where it
	// is, and the 17 rule never runs.
	if len(game.Dealer()) != 2 {
		t.Fatalf("dealer has %d cards, want 2 (no draw against a bust)", len(game.Dealer()))
	}
	// バースト後は Hit 不可: the second press on the same message must not
	// deal another card into a hand that is already settled.
	if _, err := game.Hit(); !errors.Is(err, ErrNoGameInProgress) {
		t.Fatalf("Hit() after the bust = %v, want ErrNoGameInProgress", err)
	}
	if _, err := game.Double(); !errors.Is(err, ErrNoGameInProgress) {
		t.Fatalf("Double() after the bust = %v, want ErrNoGameInProgress", err)
	}
	if err := game.Stand(); !errors.Is(err, ErrNoGameInProgress) {
		t.Fatalf("Stand() after the bust = %v, want ErrNoGameInProgress", err)
	}
	if len(game.Player()) != 3 {
		t.Fatalf("player has %d cards, want 3 (the refused presses drew nothing)", len(game.Player()))
	}
}

func TestBlackjack_DealerDrawsToSeventeenAndBusts(t *testing.T) {
	// Player 18 stands; the dealer's 16 must draw and the 9 busts it.
	game := blackjackWith(1000, []Card{card(10), card(8)}, []Card{card(10), card(6)}, deckOf(card(9)))

	if err := game.Stand(); err != nil {
		t.Fatalf("Stand returned error: %v", err)
	}
	if game.DealerValue() != 25 || len(game.Dealer()) != 3 {
		t.Fatalf("dealer = %v (%d), want three cards busting at 25", game.Dealer(), game.DealerValue())
	}
	if game.Result() != BlackjackPlayerWin {
		t.Fatalf("Result() = %v, want BlackjackPlayerWin", game.Result())
	}
	if got := game.Settle(); got != 2000 {
		t.Fatalf("Settle() = %d, want 2000 (2x the total bet)", got)
	}
}

func TestBlackjack_DealerStandsOnSeventeenIncludingASoftOne(t *testing.T) {
	tests := []struct {
		name       string
		dealer     []Card
		shoe       *Deck
		wantCards  int
		wantValue  int
		wantResult BlackjackResult
	}{
		{
			// A♠ 6♠ is a SOFT 17 and the dealer stands on it (設計書 §7).
			// Hitting would make it A 6 4 = 21 and turn the player's 19 into a
			// loss, so the result alone tells the two behaviours apart.
			name:       "soft seventeen stands",
			dealer:     []Card{card(14), card(6)},
			shoe:       deckOf(card(4), card(10)),
			wantCards:  2,
			wantValue:  17,
			wantResult: BlackjackPlayerWin,
		},
		{
			name:       "hard seventeen stands",
			dealer:     []Card{card(10), card(7)},
			shoe:       deckOf(card(4), card(10)),
			wantCards:  2,
			wantValue:  17,
			wantResult: BlackjackPlayerWin,
		},
		{
			// Sixteen must draw — the stand rule is 17 or more, not "stop
			// whenever the total looks respectable".
			name:       "sixteen draws",
			dealer:     []Card{card(10), card(6)},
			shoe:       deckOf(card(4), card(10)),
			wantCards:  3,
			wantValue:  20,
			wantResult: BlackjackPlayerWin,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The player holds 21 in three cards, which beats every dealer
			// total above that does not bust.
			game := blackjackWith(1000, []Card{card(10), card(8), card(3)}, tt.dealer, tt.shoe)

			if err := game.Stand(); err != nil {
				t.Fatalf("Stand returned error: %v", err)
			}
			if len(game.Dealer()) != tt.wantCards || game.DealerValue() != tt.wantValue {
				t.Fatalf("dealer = %v (%d), want %d cards totalling %d",
					game.Dealer(), game.DealerValue(), tt.wantCards, tt.wantValue)
			}
			if game.Result() != tt.wantResult {
				t.Fatalf("Result() = %v, want %v", game.Result(), tt.wantResult)
			}
		})
	}
}

func TestBlackjack_EqualTotalsPush(t *testing.T) {
	// Both on 19 with a dealer who is already past 17: nothing is drawn (the
	// empty shoe proves it) and the stake comes back whole.
	game := blackjackWith(1000, []Card{card(10), card(9)}, []Card{card(12), card(9)}, deckOf())

	if err := game.Stand(); err != nil {
		t.Fatalf("Stand returned error: %v", err)
	}
	if game.Result() != BlackjackPush {
		t.Fatalf("Result() = %v, want BlackjackPush", game.Result())
	}
	if got := game.Settle(); got != 1000 {
		t.Fatalf("Settle() = %d, want 1000 (the total bet back)", got)
	}
}

func TestBlackjack_LowerTotalLoses(t *testing.T) {
	game := blackjackWith(1000, []Card{card(10), card(8)}, []Card{card(12), card(9)}, deckOf())

	if err := game.Stand(); err != nil {
		t.Fatalf("Stand returned error: %v", err)
	}
	if game.Result() != BlackjackDealerWin {
		t.Fatalf("Result() = %v, want BlackjackDealerWin", game.Result())
	}
	if got := game.Settle(); got != 0 {
		t.Fatalf("Settle() = %d, want 0", got)
	}
}

func TestBlackjack_DoubleDrawsExactlyOneCardAndStands(t *testing.T) {
	// Eleven doubled: the 9 makes 20, then the dealer's 16 draws the ten and
	// busts. The second card in the shoe is the dealer's — if the double drew
	// twice, the player would hold 30 instead.
	game := blackjackWith(1000, []Card{card(5), card(6)}, []Card{card(10), card(6)}, deckOf(card(9), card(10)))

	drawn, err := game.Double()
	if err != nil {
		t.Fatalf("Double returned error: %v", err)
	}
	if drawn != card(9) {
		t.Fatalf("Double drew %v, want 9♠", drawn)
	}
	if len(game.Player()) != 3 || game.PlayerValue() != 20 {
		t.Fatalf("player = %v (%d), want three cards totalling 20 (one card only)",
			game.Player(), game.PlayerValue())
	}
	if !game.Doubled() || game.TotalBet() != 2000 {
		t.Fatalf("Doubled()/TotalBet() = %v/%d, want true/2000", game.Doubled(), game.TotalBet())
	}
	// 自動スタンド: the dealer has already played, so the hand is over.
	if game.State() != BlackjackFinished || game.Result() != BlackjackPlayerWin {
		t.Fatalf("state/result = %v/%v, want BlackjackFinished/BlackjackPlayerWin", game.State(), game.Result())
	}
	if got := game.Settle(); got != 4000 {
		t.Fatalf("Settle() = %d, want 4000 (2x the doubled stake)", got)
	}
	if game.CanDouble() {
		t.Fatalf("CanDouble() = true after doubling")
	}
	if _, err := game.Double(); !errors.Is(err, ErrNoGameInProgress) {
		t.Fatalf("second Double() = %v, want ErrNoGameInProgress", err)
	}
}

func TestBlackjack_DoubleThatBustsLosesTheWholeStake(t *testing.T) {
	// The doubled stake is at risk, and the dealer still does not draw against
	// a bust: the single card in the shoe is the player's.
	game := blackjackWith(1000, []Card{card(10), card(6)}, []Card{card(10), card(6)}, deckOf(card(10)))

	if _, err := game.Double(); err != nil {
		t.Fatalf("Double returned error: %v", err)
	}
	if game.PlayerValue() != 26 || game.Result() != BlackjackDealerWin {
		t.Fatalf("player %d / result %v, want 26 / BlackjackDealerWin", game.PlayerValue(), game.Result())
	}
	if len(game.Dealer()) != 2 {
		t.Fatalf("dealer has %d cards, want 2 (no draw against a bust)", len(game.Dealer()))
	}
	if got := game.Settle(); got != 0 {
		t.Fatalf("Settle() = %d, want 0", got)
	}
}

func TestBlackjack_DoubleIsRefusedAfterTheFirstDecision(t *testing.T) {
	// Hitting is the first decision; the ⏫ that arrives afterwards (a stale
	// message, a hand-built interaction) must be refused, because the extra
	// stake would no longer be a double-down — it would be a second bet on a
	// hand the player has already improved.
	game := blackjackWith(1000, []Card{card(5), card(6)}, []Card{card(10), card(6)}, deckOf(card(2), card(9), card(10)))

	if _, err := game.Hit(); err != nil {
		t.Fatalf("Hit returned error: %v", err)
	}
	if game.CanDouble() {
		t.Fatalf("CanDouble() = true after a hit")
	}
	if _, err := game.Double(); !errors.Is(err, ErrDoubleUnavailable) {
		t.Fatalf("Double() after a hit = %v, want ErrDoubleUnavailable", err)
	}
	// The refusal changed nothing: no card was dealt and no stake was taken.
	if len(game.Player()) != 3 {
		t.Fatalf("player has %d cards, want 3 (the refused double drew nothing)", len(game.Player()))
	}
	if game.Doubled() || game.TotalBet() != 1000 {
		t.Fatalf("Doubled()/TotalBet() = %v/%d, want false/1000", game.Doubled(), game.TotalBet())
	}
	if game.State() != BlackjackPlaying {
		t.Fatalf("State() = %v, want BlackjackPlaying (a refusal is not a resolution)", game.State())
	}
}

func TestBlackjack_AutoResolveStandsOnWhatThePlayerHolds(t *testing.T) {
	// An abandoned hand is stood, not forfeited: 20 against a dealer who draws
	// to 18 and loses.
	abandoned := blackjackWith(1000, []Card{card(10), card(10)}, []Card{card(10), card(6)}, deckOf(card(2)))
	stood := blackjackWith(1000, []Card{card(10), card(10)}, []Card{card(10), card(6)}, deckOf(card(2)))

	payout := abandoned.AutoResolve()
	if err := stood.Stand(); err != nil {
		t.Fatalf("Stand returned error: %v", err)
	}
	if payout != stood.Settle() || payout != 2000 {
		t.Fatalf("AutoResolve() = %d, want %d (= Stand then Settle, 2000)", payout, stood.Settle())
	}
	if abandoned.Result() != stood.Result() || abandoned.DealerValue() != stood.DealerValue() {
		t.Fatalf("abandoned %v/%d vs stood %v/%d, want identical",
			abandoned.Result(), abandoned.DealerValue(), stood.Result(), stood.DealerValue())
	}
	// Reporting the payout again does not re-resolve the hand.
	if got := abandoned.AutoResolve(); got != payout {
		t.Fatalf("second AutoResolve() = %d, want %d", got, payout)
	}
}

func TestBlackjack_AutoResolveOnAFinishedHandKeepsItsPayout(t *testing.T) {
	// A natural that timed out before anyone saw the message still pays 3:2.
	game := newBlackjackFromShoe(1000, deckOf(card(14), card(9), card(13), card(7)))

	if got := game.AutoResolve(); got != 2500 {
		t.Fatalf("AutoResolve() = %d, want 2500", got)
	}
	if game.Result() != BlackjackNatural || len(game.Dealer()) != 2 {
		t.Fatalf("result %v / dealer %v, want the natural untouched", game.Result(), game.Dealer())
	}
}

func TestBlackjack_PayoutNeverExceedsTwoAndAHalfTimesTheTotalBet(t *testing.T) {
	tests := []struct {
		name       string
		game       *BlackjackGame
		wantPayout int64
	}{
		{
			name:       "a natural pays the maximum",
			game:       &BlackjackGame{bet: 1000, state: BlackjackFinished, result: BlackjackNatural},
			wantPayout: 2500,
		},
		{
			// The smallest bet: 1 + floor(1*3/2) = 2, and the half chip the
			// truncation drops is the one that would have made it 2.5.
			name:       "the 3:2 on a one-chip bet truncates down",
			game:       &BlackjackGame{bet: 1, state: BlackjackFinished, result: BlackjackNatural},
			wantPayout: 2,
		},
		{
			name:       "a win pays twice the total bet",
			game:       &BlackjackGame{bet: 1000, state: BlackjackFinished, result: BlackjackPlayerWin},
			wantPayout: 2000,
		},
		{
			name:       "a doubled win pays twice the doubled stake",
			game:       &BlackjackGame{bet: 1000, doubled: true, state: BlackjackFinished, result: BlackjackPlayerWin},
			wantPayout: 4000,
		},
		{
			name:       "a doubled push returns the doubled stake",
			game:       &BlackjackGame{bet: 1000, doubled: true, state: BlackjackFinished, result: BlackjackPush},
			wantPayout: 2000,
		},
		{
			name:       "a loss pays nothing",
			game:       &BlackjackGame{bet: 1000, state: BlackjackFinished, result: BlackjackDealerWin},
			wantPayout: 0,
		},
		{
			// An unfinished hand has nothing to pay: settling it early must
			// not hand the stake back while the cards are still live.
			name:       "an unfinished hand pays nothing",
			game:       &BlackjackGame{bet: 1000, state: BlackjackPlaying, result: BlackjackUndecided},
			wantPayout: 0,
		},
		{
			// The cap holds at the top of the chip range too: MaxChips as the
			// bet is 2.5e12, well inside int64.
			name:       "the largest possible natural does not overflow",
			game:       &BlackjackGame{bet: MaxChips, state: BlackjackFinished, result: BlackjackNatural},
			wantPayout: MaxChips + MaxChips*3/2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.game.Settle()
			if got != tt.wantPayout {
				t.Fatalf("Settle() = %d, want %d", got, tt.wantPayout)
			}
			// 2*payout <= 5*totalBet is "payout <= 2.5x" without the rounding
			// a division would introduce.
			if 2*got > 5*tt.game.TotalBet() {
				t.Fatalf("Settle() = %d exceeds 2.5x the total bet (%d)", got, tt.game.TotalBet())
			}
		})
	}
}

func TestHandValue(t *testing.T) {
	tests := []struct {
		name string
		hand []Card
		want int
	}{
		{name: "an empty hand is nothing", hand: nil, want: 0},
		{name: "faces are ten each", hand: []Card{card(11), card(12), card(13)}, want: 30},
		{name: "a ten and a jack", hand: []Card{card(10), card(11)}, want: 20},
		{name: "an ace alone is eleven", hand: []Card{card(14)}, want: 11},
		{name: "an ace with a king is twenty-one", hand: []Card{card(14), card(13)}, want: 21},
		{name: "two aces count one high and one low", hand: []Card{card(14), card(14)}, want: 12},
		{name: "two aces and a nine reach twenty-one", hand: []Card{card(14), card(14), card(9)}, want: 21},
		{name: "three aces", hand: []Card{card(14), card(14), card(14)}, want: 13},
		{name: "four aces and a seven", hand: []Card{card(14), card(14), card(14), card(14), card(7)}, want: 21},
		{name: "an ace drops to one when eleven would bust", hand: []Card{card(14), card(9), card(5)}, want: 15},
		{name: "an ace with two tens is twenty-one", hand: []Card{card(14), card(10), card(10)}, want: 21},
		{name: "a bust without an ace stays busted", hand: []Card{card(10), card(6), card(9)}, want: 25},
		{name: "small cards add up", hand: []Card{card(2), card(3), card(4)}, want: 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HandValue(tt.hand); got != tt.want {
				t.Fatalf("HandValue(%v) = %d, want %d", tt.hand, got, tt.want)
			}
		})
	}
}
