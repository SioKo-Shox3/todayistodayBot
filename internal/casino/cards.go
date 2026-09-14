package casino

import "strconv"

// --- playing cards, shared by the button games (設計書 §6, §7) -------------

// Suit is a card's suit. Like SlotSymbol the values ARE the literal symbols
// rendered in the Discord response, so they double as the display strings.
type Suit string

const (
	SuitSpade   Suit = "♠"
	SuitHeart   Suit = "♥"
	SuitDiamond Suit = "♦"
	SuitClub    Suit = "♣"
)

// suits is the fixed build order NewDeck walks. MUST be a slice, not a map
// iterated directly — Go randomizes map iteration order, which would make a
// seeded deck non-reproducible (same reasoning as slot.go's reelWeights).
var suits = []Suit{SuitSpade, SuitHeart, SuitDiamond, SuitClub}

// Rank bounds. The ace is HIGH (設計書 §6: ランクは 2〜14、A が最高), which
// is why there is no rank 1: comparing two cards is then plain integer
// comparison, with no "is the ace 1 or 14 here" branch anywhere.
const (
	MinRank = 2
	MaxRank = 14
)

// Card is one playing card. Comparable (both fields are), so tests and the
// games can use == on whole cards.
type Card struct {
	Rank int
	Suit Suit
}

// String renders the card for Discord: "A♠", "10♦". Out-of-range ranks are
// printed as their number rather than hidden — a card that should not exist
// must be visible in the output, not silently rendered as something legal.
func (c Card) String() string {
	return rankLabel(c.Rank) + string(c.Suit)
}

// rankLabel names the four face/ace ranks and prints every other rank as its
// number.
func rankLabel(rank int) string {
	switch rank {
	case 11:
		return "J"
	case 12:
		return "Q"
	case 13:
		return "K"
	case 14:
		return "A"
	default:
		return strconv.Itoa(rank)
	}
}

// Deck is a shuffled shoe of n 52-card decks. It is NOT safe for concurrent
// use: one deck belongs to one game session, and the session manager
// serializes the presses that touch it (設計書 §5).
type Deck struct {
	// cards is drawn from the END, so Draw is a cheap slice truncation. The
	// order is therefore the reverse of the draw order — deckOf in the tests
	// hides that so a test can list the cards in the order they come out.
	cards []Card
}

// NewDeck builds n 52-card decks and shuffles the whole shoe with rng.
// n < 1 yields an empty deck rather than a silently-substituted single one:
// a caller that asked for no cards gets no cards, and Draw's ok=false makes
// that visible at the first draw instead of hiding a bad argument.
//
// Callers must hold the Store's lock when rng is a *rand.Rand (it is not
// concurrency safe) — same contract as drawSymbol.
func NewDeck(n int, rng randSource) *Deck {
	if n < 1 {
		return &Deck{cards: []Card{}}
	}
	cards := make([]Card, 0, n*len(suits)*(MaxRank-MinRank+1))
	for i := 0; i < n; i++ {
		for _, suit := range suits {
			for rank := MinRank; rank <= MaxRank; rank++ {
				cards = append(cards, Card{Rank: rank, Suit: suit})
			}
		}
	}
	// Fisher-Yates, descending: every permutation is equally likely given a
	// uniform Intn. Do NOT replace with sort.Slice + random less — that does
	// not produce a uniform shuffle.
	for i := len(cards) - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		cards[i], cards[j] = cards[j], cards[i]
	}
	return &Deck{cards: cards}
}

// Draw removes and returns the next card. ok is false when the deck is
// empty — the caller decides what an exhausted shoe means for its game
// rather than receiving a zero Card that looks like a rank-0 card.
func (d *Deck) Draw() (Card, bool) {
	if len(d.cards) == 0 {
		return Card{}, false
	}
	card := d.cards[len(d.cards)-1]
	d.cards = d.cards[:len(d.cards)-1]
	return card, true
}

// Remaining returns the cards still in the deck, as a COPY: the odds display
// reads it on every press, and handing out the live slice would let a caller
// reorder or truncate the shoe it is only supposed to count. The order is
// the internal one (reverse draw order) and carries no meaning — callers
// count these cards, they do not read them as a sequence.
func (d *Deck) Remaining() []Card {
	out := make([]Card, len(d.cards))
	copy(out, d.cards)
	return out
}

// Len is the number of cards left, for callers that only need the count.
func (d *Deck) Len() int { return len(d.cards) }
