package casino

import (
	"math/rand"
	"testing"
)

// deckOf builds a deck that draws the given cards in the order they are
// listed. Deck draws from the END of its slice, so the order is reversed
// here — the tests read as the sequence the player sees.
func deckOf(cards ...Card) *Deck {
	reversed := make([]Card, len(cards))
	for i, card := range cards {
		reversed[len(cards)-1-i] = card
	}
	return &Deck{cards: reversed}
}

func TestDeck_NewDeckHoldsEveryCardOfEveryDeck(t *testing.T) {
	tests := []struct {
		name  string
		decks int
		want  int // times each (rank, suit) must appear
	}{
		{name: "one deck", decks: 1, want: 1},
		{name: "two decks", decks: 2, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deck := NewDeck(tt.decks, rand.New(rand.NewSource(1)))

			if got := deck.Len(); got != 52*tt.decks {
				t.Fatalf("Len() = %d, want %d", got, 52*tt.decks)
			}
			counts := map[Card]int{}
			for _, card := range deck.Remaining() {
				counts[card]++
			}
			for _, suit := range suits {
				for rank := MinRank; rank <= MaxRank; rank++ {
					card := Card{Rank: rank, Suit: suit}
					if counts[card] != tt.want {
						t.Fatalf("%v appears %d times, want %d", card, counts[card], tt.want)
					}
				}
			}
			// No rank outside 2..14 — a rank-1 ace would break the "compare
			// with >" rule the whole game rests on.
			if len(counts) != 52 {
				t.Fatalf("distinct cards = %d, want 52", len(counts))
			}
		})
	}
}

func TestDeck_NonPositiveCountIsAnEmptyDeck(t *testing.T) {
	for _, n := range []int{0, -1} {
		deck := NewDeck(n, rand.New(rand.NewSource(1)))
		if deck.Len() != 0 {
			t.Fatalf("NewDeck(%d) has %d cards, want 0", n, deck.Len())
		}
		if _, ok := deck.Draw(); ok {
			t.Fatalf("NewDeck(%d).Draw() reported a card", n)
		}
	}
}

func TestDeck_SameSeedShufflesIdentically(t *testing.T) {
	first := NewDeck(1, rand.New(rand.NewSource(42))).Remaining()
	again := NewDeck(1, rand.New(rand.NewSource(42))).Remaining()
	other := NewDeck(1, rand.New(rand.NewSource(43))).Remaining()

	for i := range first {
		if first[i] != again[i] {
			t.Fatalf("seed 42 produced different decks at index %d: %v vs %v", i, first[i], again[i])
		}
	}
	// A different seed must actually shuffle differently, or every "seeded"
	// test below would be testing a constant order.
	same := true
	for i := range first {
		if first[i] != other[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("seeds 42 and 43 produced the same deck order")
	}
	// And the shuffle must not leave the build order in place.
	built := NewDeck(1, sequenceRand{})
	if unshuffled := built.Remaining(); unshuffled[0] == first[0] && unshuffled[1] == first[1] {
		t.Fatal("the shuffled deck starts with the unshuffled build order")
	}
}

// sequenceRand is a randSource whose Intn always returns 0. Fisher-Yates with
// j == 0 still moves cards, so it gives a fixed, non-identity permutation to
// compare a seeded shuffle against.
type sequenceRand struct{}

func (sequenceRand) Intn(int) int     { return 0 }
func (sequenceRand) Float64() float64 { return 0 }

func TestDeck_DrawTakesEachCardOnceThenReportsEmpty(t *testing.T) {
	deck := NewDeck(1, rand.New(rand.NewSource(7)))

	seen := map[Card]bool{}
	for i := 0; i < 52; i++ {
		card, ok := deck.Draw()
		if !ok {
			t.Fatalf("Draw() %d of 52 reported an empty deck", i+1)
		}
		if seen[card] {
			t.Fatalf("Draw() returned %v twice", card)
		}
		seen[card] = true
		if want := 51 - i; deck.Len() != want {
			t.Fatalf("after %d draws Len() = %d, want %d", i+1, deck.Len(), want)
		}
	}
	if _, ok := deck.Draw(); ok {
		t.Fatal("the 53rd Draw() returned a card")
	}
}

func TestDeck_DrawFollowsTheDeckOrder(t *testing.T) {
	want := []Card{{Rank: 14, Suit: SuitSpade}, {Rank: 2, Suit: SuitHeart}, {Rank: 10, Suit: SuitClub}}
	deck := deckOf(want...)

	for i, expected := range want {
		got, ok := deck.Draw()
		if !ok {
			t.Fatalf("Draw() %d reported an empty deck", i+1)
		}
		if got != expected {
			t.Fatalf("Draw() %d = %v, want %v", i+1, got, expected)
		}
	}
}

func TestDeck_RemainingIsACopy(t *testing.T) {
	deck := deckOf(Card{Rank: 5, Suit: SuitSpade}, Card{Rank: 9, Suit: SuitHeart})

	snapshot := deck.Remaining()
	snapshot[0] = Card{Rank: 14, Suit: SuitDiamond}
	snapshot = snapshot[:0]

	if deck.Len() != 2 {
		t.Fatalf("Len() = %d after mutating the Remaining() copy, want 2", deck.Len())
	}
	for _, card := range deck.Remaining() {
		if card.Rank == 14 {
			t.Fatal("a write to the Remaining() copy reached the deck")
		}
	}
}

func TestDeck_CardStringNamesTheFaceCards(t *testing.T) {
	tests := []struct {
		card Card
		want string
	}{
		{card: Card{Rank: 2, Suit: SuitSpade}, want: "2♠"},
		{card: Card{Rank: 10, Suit: SuitHeart}, want: "10♥"},
		{card: Card{Rank: 11, Suit: SuitDiamond}, want: "J♦"},
		{card: Card{Rank: 12, Suit: SuitClub}, want: "Q♣"},
		{card: Card{Rank: 13, Suit: SuitSpade}, want: "K♠"},
		{card: Card{Rank: 14, Suit: SuitHeart}, want: "A♥"},
	}
	for _, tt := range tests {
		if got := tt.card.String(); got != tt.want {
			t.Fatalf("Card{%d, %q}.String() = %q, want %q", tt.card.Rank, tt.card.Suit, got, tt.want)
		}
	}
}
