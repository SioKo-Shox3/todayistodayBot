package casino

// SlotSymbol is one reel face. The values are the literal emoji rendered in
// the /slot response, so they double as the display strings.
type SlotSymbol string

const (
	SymbolCherry  SlotSymbol = "🍒"
	SymbolLemon   SlotSymbol = "🍋"
	SymbolGrape   SlotSymbol = "🍇"
	SymbolBell    SlotSymbol = "🔔"
	SymbolDiamond SlotSymbol = "💎"
	SymbolSeven   SlotSymbol = "7️⃣"
)

// reelWeights is the fixed draw order for the cumulative-distribution
// lookup in drawSymbol. MUST be a slice, not a map iterated directly — Go
// randomizes map iteration order, which would make Spin's output
// non-deterministic for a given rng sequence even with a seeded source.
// Sum of Weight == 100 (verified: 28+22+21+18+7+4=100).
//
// These weights, triplePayout and cherryPairPayout are ONE calibrated set,
// not three independent knobs: they were derived together by enumerating all
// 6³ = 216 ordered outcomes in exact rational arithmetic, yielding an RTP of
// exactly 929900/1000000 = 92.99% and a near-miss rate of 18279/1000000 =
// 1-in-54.71. TestPayoutTable_ExactLiterals re-derives all four numerators and
// fails on any change, deliberately: do not "tune" a single number here.
var reelWeights = []struct {
	Symbol SlotSymbol
	Weight int
}{
	{SymbolCherry, 28}, {SymbolLemon, 22}, {SymbolGrape, 21},
	{SymbolBell, 18}, {SymbolDiamond, 7}, {SymbolSeven, 4},
}

// triplePayout is the bet multiplier for three of a kind. Monotone with
// rarity: the most frequent symbol pays least.
var triplePayout = map[SlotSymbol]int64{
	SymbolCherry: 7, SymbolLemon: 16, SymbolGrape: 22,
	SymbolBell: 32, SymbolDiamond: 98, SymbolSeven: 196,
}

const cherryPairPayout int64 = 1 // "🍒2個でベット額返還"

// jackpotReels is the ONE combination that empties the pool (設計書 C-3a §2);
// diamondReels pays its ×98 and the public celebration but never touches the
// pool. Both are named so store.go's Spin and IsJackpot below decide from the
// same literal.
var (
	jackpotReels = [3]SlotSymbol{SymbolSeven, SymbolSeven, SymbolSeven}
	diamondReels = [3]SlotSymbol{SymbolDiamond, SymbolDiamond, SymbolDiamond}
)

// The jackpot pool's two constants (設計書 C-3a §2). The accrual comes out of
// the HOUSE's edge, not the player's: the payout table above is unchanged and
// the player is still debited exactly `bet`, so the pool is funded from the
// 7.01% the table already keeps. That is why nothing here feeds payoutFor.
//
// bet × JackpotContributionPercent is counted in 1/100 chips and carried in
// GuildEconomy.JackpotAccum, so the minimum bet (10 chips → 0.2 chips) still
// accrues in full over five spins instead of truncating to 0 every time.
const (
	JackpotSeed                int64 = 1_000 // floor the pool is seeded and reset to
	JackpotContributionPercent int64 = 2     // percent of each bet that feeds the pool
	jackpotAccumScale          int64 = 100   // 1/100-chip units per chip
)

// SpinResult is Spin's outcome — exported because internal/commands/slot.go
// (a different package) renders it into the Discord response.
type SpinResult struct {
	Reels [3]SlotSymbol
	Bet   int64
	// Payout is what actually reached the account (jackpot included); 0 on a
	// loss. Owed is what the table said to pay. The two differ only when
	// MaxChips had no room for all of it — Store.Spin credits what fits and
	// does not fail — so renderers and 純利 use Payout, the same split
	// SettleResult makes.
	Payout    int64
	Owed      int64
	IsJackpot bool // true iff Reels is 💎💎💎 or 7️⃣7️⃣7️⃣ — triggers the public celebration message
	// JackpotWon is the pool paid out on top of the table payout, 0 when the
	// pool did not fire. It is a COMPONENT of Owed, not an extra amount to
	// credit — a renderer that adds it to Payout double-counts it.
	JackpotWon int64
	// JackpotPool is the pool AFTER this spin: the post-reset JackpotSeed on a
	// win, otherwise the pool including this spin's own accrual.
	JackpotPool int64
}

// drawSymbol draws one reel face from the weighted distribution. Callers must
// hold the Store's lock when rng is a *rand.Rand (it is not concurrency safe).
func drawSymbol(rng randSource) SlotSymbol {
	n := rng.Intn(100)
	acc := 0
	for _, e := range reelWeights {
		acc += e.Weight
		if n < acc {
			return e.Symbol
		}
	}
	return reelWeights[len(reelWeights)-1].Symbol // unreachable: weights sum to 100
}

// spin draws 3 independent reels and resolves the TABLE payout. Pure — bet is
// NOT deducted or credited here (that's Store.Spin's job, inside a locked
// Update transaction). The pool lives in GuildEconomy, which this function
// cannot see, so JackpotWon and JackpotPool are left zero for Store.Spin to
// fill in: everything here depends only on the reels and the bet.
func spin(bet int64, rng randSource) SpinResult {
	reels := [3]SlotSymbol{drawSymbol(rng), drawSymbol(rng), drawSymbol(rng)}
	payout := payoutFor(reels, bet)
	// Payout and Owed start out equal: nothing has been credited yet, so the
	// table figure is both. Store.Spin adds the pool to Owed and then
	// overwrites Payout with what the cap let through.
	return SpinResult{
		Reels:     reels,
		Bet:       bet,
		Payout:    payout,
		Owed:      payout,
		IsJackpot: reels == diamondReels || reels == jackpotReels,
	}
}

// payoutFor resolves reels into the chips paid back for bet: three of a kind
// pays triplePayout, otherwise exactly two 🍒 refund the bet, otherwise
// nothing. Three cherries take the triple branch (×7), never the refund.
func payoutFor(reels [3]SlotSymbol, bet int64) int64 {
	if reels[0] == reels[1] && reels[1] == reels[2] {
		return bet * triplePayout[reels[0]]
	}
	cherryCount := 0
	for _, r := range reels {
		if r == SymbolCherry {
			cherryCount++
		}
	}
	if cherryCount == 2 {
		return bet * cherryPairPayout
	}
	return 0
}
