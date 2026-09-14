package casino

import "errors"

// --- ブラックジャック, pure game logic (設計書 §7) --------------------------
//
// Same discipline as highlow.go: the rules and the money arithmetic, nothing
// else. No Store, no Discord, no clock. The session manager (設計書 §5) holds
// the hand in memory and the Store holds the stake in escrow; Settle's return
// is the one number that crosses back over to SettleGame.

// ErrDoubleUnavailable rejects a double that the rules do not offer: the
// player has already hit, or has already doubled. 設計書 §7 allows it on the
// FIRST decision only.
//
// The command layer must check CanDouble before it calls AddToEscrow —
// otherwise a stale message's ⏫ press would take the extra stake and then be
// refused here, leaving chips in escrow that the hand never accounts for.
var ErrDoubleUnavailable = errors.New("casino: double is only available on the first decision")

const (
	// blackjackShoeDecks is the number of 52-card decks in the shoe (設計書 §7).
	blackjackShoeDecks = 6
	// blackjackTarget is the value a hand must not exceed.
	blackjackTarget = 21
	// blackjackDealerStandsAt is where the dealer stops. The comparison is on
	// the hand's value alone, which is what makes a SOFT 17 stand too
	// (設計書 §7: ソフト 17 もスタンド) — there is deliberately no "is this
	// total soft" branch to get wrong.
	blackjackDealerStandsAt = 17
	// blackjackNaturalNumerator / Denominator are the 3:2 natural payout.
	blackjackNaturalNumerator   = 3
	blackjackNaturalDenominator = 2
)

// BlackjackState is where a hand is in its lifecycle. Only BlackjackPlaying
// accepts presses.
type BlackjackState int

const (
	BlackjackPlaying BlackjackState = iota
	BlackjackFinished
)

// BlackjackResult is what a finished hand pays, as a rule rather than a
// number: Settle turns it into chips. A bust is not its own result — it is a
// win for the other side, and the busted total stays visible in the hand.
type BlackjackResult int

const (
	BlackjackUndecided BlackjackResult = iota
	BlackjackPlayerWin
	BlackjackDealerWin
	BlackjackPush
	// BlackjackNatural is the player's two-card 21 against a dealer who does
	// not have one: the only result paid 3:2.
	BlackjackNatural
)

// BlackjackGame is one player's hand. Every field is unexported for the same
// reason as HighLowGame's: bet and doubled decide the payout, so nothing
// outside this file may assign them.
type BlackjackGame struct {
	bet     int64
	doubled bool
	player  []Card
	dealer  []Card
	shoe    *Deck
	state   BlackjackState
	result  BlackjackResult
}

// NewBlackjack shuffles a six-deck shoe, deals the opening four cards and
// resolves the naturals (設計書 §7): both hands natural is a push, the player
// alone is an immediate 3:2, the dealer alone an immediate loss. All three end
// the hand before any button exists, which is why Double can never apply to a
// natural — TotalBet is still the bet at that point.
//
// Cards are dealt in table order (player, dealer, player, dealer); the tests'
// fixed shoes list them that way.
//
// Callers must hold the Store's lock when rng is a *rand.Rand (it is not
// concurrency safe) — same contract as NewDeck.
func NewBlackjack(bet int64, rng randSource) *BlackjackGame {
	return newBlackjackFromShoe(bet, NewDeck(blackjackShoeDecks, rng))
}

// newBlackjackFromShoe is NewBlackjack without the shuffle: the deal and the
// natural check, over a shoe the caller supplies. It exists so the deal itself
// can be driven by a fixed shoe instead of being restated in the tests — a
// natural resolved at the deal is a rule, and a test that built the finished
// hand by hand would not exercise it.
func newBlackjackFromShoe(bet int64, shoe *Deck) *BlackjackGame {
	game := &BlackjackGame{
		bet:    bet,
		shoe:   shoe,
		player: make([]Card, 0, 4),
		dealer: make([]Card, 0, 4),
	}
	for i := 0; i < 2; i++ {
		game.player = append(game.player, game.mustDraw())
		game.dealer = append(game.dealer, game.mustDraw())
	}

	playerNatural := isNatural(game.player)
	dealerNatural := isNatural(game.dealer)
	switch {
	case playerNatural && dealerNatural:
		game.finish(BlackjackPush)
	case playerNatural:
		game.finish(BlackjackNatural)
	case dealerNatural:
		game.finish(BlackjackDealerWin)
	}
	return game
}

// mustDraw takes the next card from the shoe. A 312-card shoe cannot run out
// inside one hand — the player would have to reach 21 in aces and twos — so an
// empty shoe means the shoe is not the one NewBlackjack built. Returning a
// zero Card would render as a rank-0 card and score as 0; a panic is the
// honest answer to a corrupted shoe, and no reachable input produces it.
func (g *BlackjackGame) mustDraw() Card {
	card, ok := g.shoe.Draw()
	if !ok {
		panic("casino: blackjack shoe ran out mid-hand")
	}
	return card
}

// Bet is the opening stake.
func (g *BlackjackGame) Bet() int64 { return g.bet }

// TotalBet is everything staked on this hand: the bet, doubled if the player
// doubled. It is the base of every payout Settle computes.
func (g *BlackjackGame) TotalBet() int64 {
	if g.doubled {
		return g.bet * 2
	}
	return g.bet
}

// Doubled reports whether the extra stake was taken.
func (g *BlackjackGame) Doubled() bool { return g.doubled }

// Player returns the player's cards as a COPY — the renderer reads them on
// every press and must not be able to reorder the hand it is displaying.
func (g *BlackjackGame) Player() []Card { return copyHand(g.player) }

// Dealer returns the dealer's cards as a COPY, INCLUDING the hole card. Hiding
// the second card while the hand is live is the renderer's job (設計書 §7:
// 1 枚が伏せ) — the rules need the whole hand, and a game that withheld it
// from itself could not score the dealer.
func (g *BlackjackGame) Dealer() []Card { return copyHand(g.dealer) }

// PlayerValue is the player's hand value, aces counted high where they fit.
func (g *BlackjackGame) PlayerValue() int { return HandValue(g.player) }

// DealerValue is the dealer's WHOLE hand, hole card included — see Dealer.
func (g *BlackjackGame) DealerValue() int { return HandValue(g.dealer) }

// State reports whether the hand still accepts presses.
func (g *BlackjackGame) State() BlackjackState { return g.state }

// Result is what the finished hand pays; BlackjackUndecided while it runs.
func (g *BlackjackGame) Result() BlackjackResult { return g.result }

// CanDouble reports whether ⏫ is offered: a live hand, on the first decision
// (still two cards), not already doubled. The command layer disables the
// button on false AND must not stake the extra chips without asking first —
// the same "a disabled button is only a rendering decision" argument as
// ErrChoiceUnavailable.
func (g *BlackjackGame) CanDouble() bool {
	return g.state == BlackjackPlaying && !g.doubled && len(g.player) == 2
}

// Hit draws one card into the player's hand and returns it. Busting ends the
// hand immediately as a loss — the dealer does not draw against a bust
// (設計書 §7), which is where this game's house edge actually lives.
//
// Returns ErrNoGameInProgress once the hand is over: that is the double-press
// guard, and it is what makes "バースト後は Hit 不可" a rule rather than a
// rendering accident.
func (g *BlackjackGame) Hit() (Card, error) {
	if g.state != BlackjackPlaying {
		return Card{}, ErrNoGameInProgress
	}
	card := g.mustDraw()
	g.player = append(g.player, card)
	if HandValue(g.player) > blackjackTarget {
		g.finish(BlackjackDealerWin)
	}
	return card, nil
}

// Stand passes to the dealer, who draws to blackjackDealerStandsAt; the two
// totals are then compared. It always ends the hand.
func (g *BlackjackGame) Stand() error {
	if g.state != BlackjackPlaying {
		return ErrNoGameInProgress
	}
	g.playDealer()
	return nil
}

// Double takes the extra stake, draws exactly one card and stands
// automatically (設計書 §7: ダブル後は 1 枚だけ引いて自動スタンド). The drawn
// card is returned so the renderer can show it even when it busted the hand.
//
// The caller is responsible for having moved the extra stake into escrow
// first; see ErrDoubleUnavailable for why the order matters.
func (g *BlackjackGame) Double() (Card, error) {
	if g.state != BlackjackPlaying {
		return Card{}, ErrNoGameInProgress
	}
	if !g.CanDouble() {
		return Card{}, ErrDoubleUnavailable
	}
	g.doubled = true
	card := g.mustDraw()
	g.player = append(g.player, card)
	if HandValue(g.player) > blackjackTarget {
		g.finish(BlackjackDealerWin)
		return card, nil
	}
	g.playDealer()
	return card, nil
}

// playDealer runs the dealer's fixed strategy and records the result. It is
// only reached with a player hand of 21 or less: a bust is settled where it
// happens, before the dealer ever acts.
func (g *BlackjackGame) playDealer() {
	for HandValue(g.dealer) < blackjackDealerStandsAt {
		g.dealer = append(g.dealer, g.mustDraw())
	}
	player, dealer := HandValue(g.player), HandValue(g.dealer)
	switch {
	case dealer > blackjackTarget, player > dealer:
		g.finish(BlackjackPlayerWin)
	case player < dealer:
		g.finish(BlackjackDealerWin)
	default:
		g.finish(BlackjackPush)
	}
}

// finish is the single place a hand becomes terminal, so a result can never be
// recorded on a hand that still accepts presses.
func (g *BlackjackGame) finish(result BlackjackResult) {
	g.state = BlackjackFinished
	g.result = result
}

// Settle is the chips to hand SettleGame: 2x the total bet for a win, the
// total bet back for a push, nothing for a loss, and bet + floor(bet*3/2) for
// a natural (設計書 §7). An unfinished hand pays 0 — there is nothing to pay
// yet, and a caller that settles early must not get the stake back for free.
//
// Unlike HighLowGame.CashOut this is a pure read: the hand is already
// terminal, so calling it twice returns the same number rather than a second
// payout. Not settling twice is the session manager's job (the hand leaves the
// map) and the Store's (ErrNoGameInProgress).
//
// The maximum is 2.5x the total bet, reached only by a natural — which cannot
// be doubled, so the two multipliers can never compound.
func (g *BlackjackGame) Settle() int64 {
	switch g.result {
	case BlackjackNatural:
		return g.bet + g.bet*blackjackNaturalNumerator/blackjackNaturalDenominator
	case BlackjackPlayerWin:
		return 2 * g.TotalBet()
	case BlackjackPush:
		return g.TotalBet()
	default:
		return 0
	}
}

// AutoResolve is what the session manager calls when a hand times out or the
// process is shutting down: the player stands on what they hold (設計書 §7 の
// 自動決着 = スタンド). Standing, not forfeiting — an abandoned hand still has
// a claim, exactly as ハイ&ロー's abandoned board keeps its pot.
//
// A finished hand is left alone and its existing payout reported.
func (g *BlackjackGame) AutoResolve() int64 {
	if g.state == BlackjackPlaying {
		g.playDealer()
	}
	return g.Settle()
}

// HandValue scores a hand with each ace worth 11 where that fits and 1 where
// it does not (設計書 §7). Faces are 10; this deck's ace is rank MaxRank
// (cards.go keeps the ace HIGH so ハイ&ロー can compare ranks with <).
//
// The reduction is per ace, not all-or-nothing: A A 9 is 21 (11 + 1 + 9), so a
// hand with two aces must be able to count one high and one low.
func HandValue(hand []Card) int {
	value, aces := 0, 0
	for _, card := range hand {
		switch {
		case card.Rank == MaxRank:
			aces++
			value += 11
		case card.Rank > 10:
			value += 10
		default:
			value += card.Rank
		}
	}
	for value > blackjackTarget && aces > 0 {
		value -= 10
		aces--
	}
	return value
}

// isNatural reports a two-card 21. Three cards to 21 is not a natural and is
// not paid 3:2.
func isNatural(hand []Card) bool {
	return len(hand) == 2 && HandValue(hand) == blackjackTarget
}

// copyHand hands out a hand without handing out the slice it lives in.
func copyHand(hand []Card) []Card {
	out := make([]Card, len(hand))
	copy(out, hand)
	return out
}
