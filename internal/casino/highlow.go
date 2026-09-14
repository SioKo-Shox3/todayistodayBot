package casino

import (
	"errors"
	"math"
)

// --- ハイ&ロー, pure game logic (設計書 §6) --------------------------------
//
// This file owns the rules and the money arithmetic and nothing else: it
// never touches the Store, Discord, or the clock. The session manager
// (設計書 §5) holds the board in memory and the Store holds the stake in
// escrow; what connects them is the payout this file computes, handed to
// SettleGame exactly once.

// ErrChoiceUnavailable rejects a guess that no remaining card can win — ハイ
// on an ace, ロー on a two. The command layer disables those buttons, but the
// rule is enforced HERE as well: a disabled button is a rendering decision,
// and a stale message (or a hand-built interaction) can still deliver the
// press. Paying a multiplier for an impossible outcome would divide by zero.
//
// It lives here rather than in errors.go because it is the game's
// vocabulary; errors.go carries the economy's sentinels.
var ErrChoiceUnavailable = errors.New("casino: no remaining card can win that guess")

// errDeckExhausted means the shoe ran out mid-hand. It is unreachable by
// construction — Odds counts the same deck Guess then draws from, so a
// non-zero winning count proves there is a card left — and is therefore
// unexported and untested: it exists so that a future rule change that
// breaks that coupling fails loudly instead of paying out on a zero Card.
var errDeckExhausted = errors.New("casino: the deck is out of cards")

const (
	// highLowPayoutPercent is the house edge, expressed as the share of a
	// FAIR multiplier the player is paid: 95 % (設計書 §6, ハウス 5 %).
	highLowPayoutPercent = 95
	// highLowMaxStreak auto-cashes a player out after ten straight wins.
	highLowMaxStreak = 10
	// highLowPotCapMultiple caps the pot at 100x the bet. Reaching it also
	// ends the game (設計書 §6: 超えたら上限で自動キャッシュアウト) — the pot
	// is paid AT the cap, not above it.
	highLowPotCapMultiple = 100
)

// HighLowState is where a board is in its lifecycle. Only HighLowPlaying
// accepts presses; the other two are terminal and mean SettleGame has been
// (or is about to be) called with the payout.
type HighLowState int

const (
	HighLowPlaying HighLowState = iota
	HighLowCashedOut
	HighLowLost
)

// HighLowGame is one player's board. Every field is unexported: the command
// layer renders it through the accessors, and the pot in particular must
// never be assignable from outside — it is the number that becomes chips.
type HighLowGame struct {
	bet     int64
	pot     int64
	streak  int
	current Card
	deck    *Deck
	state   HighLowState
}

// NewHighLow deals the opening card from a freshly shuffled 52-card deck.
// The pot starts AT the bet, which is what makes an untouched game's cash-out
// a full refund (設計書 §6) without a special case anywhere.
//
// Callers must hold the Store's lock when rng is a *rand.Rand.
func NewHighLow(bet int64, rng randSource) *HighLowGame {
	deck := NewDeck(1, rng)
	// A 52-card deck always yields a first card; ok is checked anyway so a
	// future NewDeck(0, …) cannot deal a zero Card as a rank-0 card.
	current, ok := deck.Draw()
	if !ok {
		return &HighLowGame{bet: bet, pot: bet, deck: deck, state: HighLowLost}
	}
	return &HighLowGame{bet: bet, pot: bet, current: current, deck: deck, state: HighLowPlaying}
}

// Bet is the staked amount — the escrow the Store is holding.
func (g *HighLowGame) Bet() int64 { return g.bet }

// Pot is what a cash-out would pay right now.
func (g *HighLowGame) Pot() int64 { return g.pot }

// Streak is how many guesses have been won in a row.
func (g *HighLowGame) Streak() int { return g.streak }

// Current is the face-up card the next guess is measured against.
func (g *HighLowGame) Current() Card { return g.current }

// State reports whether the board still accepts presses.
func (g *HighLowGame) State() HighLowState { return g.state }

// HighLowOdds is everything the buttons need to be readable (設計書 §6:
// それぞれの当たる確率と倍率を…読めること). The counts are the honest
// denominators — the deck is NOT reshuffled between guesses, so the odds
// shift as cards come out and the display must shift with them.
type HighLowOdds struct {
	Remaining int // cards left in the deck
	HighCards int // cards strictly above the current rank
	LowCards  int // cards strictly below it
	// Multipliers are x100 integers (12345 = 123.45x). 0 means the guess
	// cannot win and the button must be disabled.
	HighMultiplier int64
	LowMultiplier  int64
}

// Odds counts the remaining deck against the current card. Cards of the SAME
// rank are counted on neither side: a tie loses (設計書 §6), so they are part
// of the denominator and of neither numerator — which is exactly where the
// house edge beyond the 5 % cut comes from.
func (g *HighLowGame) Odds() HighLowOdds {
	odds := HighLowOdds{Remaining: g.deck.Len()}
	for _, card := range g.deck.cards {
		switch {
		case card.Rank > g.current.Rank:
			odds.HighCards++
		case card.Rank < g.current.Rank:
			odds.LowCards++
		}
	}
	odds.HighMultiplier = Multiplier(odds.HighCards, odds.Remaining)
	odds.LowMultiplier = Multiplier(odds.LowCards, odds.Remaining)
	return odds
}

// Multiplier is the payout for a guess that `winning` of `remaining` cards
// would win, as an x100 integer (9500 = 95.00x). It is the fair multiplier
// remaining/winning cut to 95 % for the house, computed in ONE integer
// expression — 95*remaining/winning — so the truncation happens exactly once,
// at the end, and always in the house's favour.
//
// winning == 0 returns 0, the "this guess cannot win" marker the display
// turns into a disabled button. remaining <= 0 returns 0 for the same reason:
// no cards, no bet.
func Multiplier(winning, remaining int) int64 {
	if winning <= 0 || remaining <= 0 {
		return 0
	}
	return int64(highLowPayoutPercent) * int64(remaining) / int64(winning)
}

// StepResult is what one guess did, and is the ONLY thing the command layer
// needs to render the next message and to settle.
type StepResult struct {
	Won         bool
	Previous    Card  // the card the guess was made against
	Card        Card  // the card turned over (the new current card on a win)
	Multiplier  int64 // x100, the multiplier the guess was offered at
	Pot         int64 // the pot after this step; 0 on a loss
	Streak      int
	Finished    bool  // the game is over — settle now
	AutoCashOut bool  // a WIN ended it: the streak or the pot cap was reached
	Payout      int64 // chips to hand SettleGame when Finished; 0 on a loss
}

// Guess turns over the next card. high means "the next card is higher"; a
// tie loses either way.
//
// On a win the pot grows by the multiplier the odds showed BEFORE the draw,
// the drawn card becomes the current one and the streak advances; ten wins or
// a pot at 100x the bet end the game with an automatic cash-out. On a loss
// the pot is gone and the game is over.
//
// Returns ErrNoGameInProgress once the game is finished (the double-press
// guard: two presses on the same board must not resolve it twice) and
// ErrChoiceUnavailable for a guess no remaining card can win. Neither one
// mutates the board.
func (g *HighLowGame) Guess(high bool) (StepResult, error) {
	if g.state != HighLowPlaying {
		return StepResult{}, ErrNoGameInProgress
	}
	odds := g.Odds()
	winning, multiplier := odds.LowCards, odds.LowMultiplier
	if high {
		winning, multiplier = odds.HighCards, odds.HighMultiplier
	}
	if winning == 0 {
		return StepResult{}, ErrChoiceUnavailable
	}
	drawn, ok := g.deck.Draw()
	if !ok {
		return StepResult{}, errDeckExhausted
	}

	previous := g.current
	won := drawn.Rank > previous.Rank
	if !high {
		won = drawn.Rank < previous.Rank
	}
	// Equal ranks fall through as won == false on both sides: 同ランクは負け.
	if !won {
		g.pot = 0
		g.state = HighLowLost
		return StepResult{
			Previous: previous, Card: drawn, Multiplier: multiplier,
			Streak: g.streak, Finished: true,
		}, nil
	}

	g.pot, _ = growPot(g.pot, multiplier, g.potCap())
	g.current = drawn
	g.streak++
	result := StepResult{
		Won: true, Previous: previous, Card: drawn, Multiplier: multiplier,
		Pot: g.pot, Streak: g.streak,
	}
	if g.streak >= highLowMaxStreak || g.pot >= g.potCap() {
		g.state = HighLowCashedOut
		result.Finished = true
		result.AutoCashOut = true
		result.Payout = g.pot
	}
	return result, nil
}

// potCap is the most this board can ever pay: 100x the bet (設計書 §6).
// bet cannot overflow it — escrow can never exceed MaxChips (1e12), and
// 1e12*100 is 1e14.
func (g *HighLowGame) potCap() int64 { return g.bet * highLowPotCapMultiple }

// growPot applies an x100 multiplier to the pot, truncating (通貨の切り捨て
// 規則) and clamping at capAt. It reports whether the cap was reached, which
// is what ends the game.
//
// The overflow guard is not decoration: it makes the clamp total — any
// product too large to represent is, by definition, at or past the cap.
func growPot(pot, multiplierX100, capAt int64) (int64, bool) {
	if multiplierX100 <= 0 || pot > math.MaxInt64/multiplierX100 {
		return capAt, true
	}
	grown := pot * multiplierX100 / 100
	if grown >= capAt {
		return capAt, true
	}
	return grown, false
}

// CashOut ends the game and returns the chips to pay — the pot, which for an
// untouched board is the bet itself (設計書 §6: 未プレイのキャッシュアウトは
// 全額返金). A board that is already finished pays 0: the payout was reported
// by the step that ended it, and paying it again here would double-settle a
// hand (the Store's ErrNoGameInProgress is the second half of that guard).
func (g *HighLowGame) CashOut() int64 {
	if g.state != HighLowPlaying {
		return 0
	}
	g.state = HighLowCashedOut
	return g.pot
}

// AutoResolve is what the session manager calls when a board times out or the
// process is shutting down: the player keeps whatever the pot holds. It is
// CashOut — resolving an abandoned game in the player's favour is the same
// operation as their own cash-out, and giving it a separate name keeps the
// session manager's call site honest about why it fired.
func (g *HighLowGame) AutoResolve() int64 { return g.CashOut() }
