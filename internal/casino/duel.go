package casino

// duel.go is the PvP coin toss (設計書 C-3b §4.5): the pure half, with no
// discordgo and no Store in it. The escrow and the settlement live in
// store.go, for the same reason lottery.go splits from store.go — the odds
// and the payout table are worth testing without a file on disk.

// GameDuel names the duel in the escrow trio (UserAccount.EscrowGame) and in
// the session/custom_id namespace, alongside GameHighLow and GameBlackjack.
//
// It is load-bearing rather than decorative: a duel is the first game whose
// settlement can be triggered by SOMEBODY ELSE's button press (the opponent
// accepts, the sweeper expires it), so AcceptDuel and DeclineDuel have to be
// able to tell "this escrow is the challenge you are answering" from "this
// escrow is a blackjack hand the challenger started after the duel closed".
// Escrow > 0 alone cannot distinguish them, and refunding a blackjack stake
// out from under its own board would pay the stake twice.
const GameDuel GameKind = "duel"

// DuelStage is where a challenge stands. It lives on the in-memory session
// board only — a duel is never persisted as a board, so this never reaches
// data/casino.json.
type DuelStage string

const (
	// DuelPending is 待機: the challenger's stake is in escrow and the
	// opponent has pressed neither button yet.
	DuelPending DuelStage = "pending"
	// DuelSettled is 決着: the coin has been tossed and both accounts have
	// been settled, or the challenge was declined/expired. Either way the
	// board is spent and every button on it is a no-op.
	DuelSettled DuelStage = "settled"
)

// DuelState is one challenge's board. OpponentID is here — rather than being
// left implicit in the message's mention — because the owner check for a
// duel is inverted from every other game in this package: Session.UserID is
// the CHALLENGER, but the only person allowed to press ⚔️/🚫 is the
// opponent. The command layer reads this field to make that decision, so a
// board without it cannot be guarded at all.
type DuelState struct {
	ChallengerID string
	OpponentID   string
	Bet          int64
	Stage        DuelStage
}

// FlipDuel tosses the coin: true means the challenger wins. It is a single
// Intn(2), which is what makes the duel a fair 50/50 with no house edge —
// 設計書 §2 forbids a house cut here precisely because there is no skill in
// a coin toss to pay for one.
//
// Callers must hold the Store's lock when rng is a *rand.Rand (it is not
// concurrency safe), exactly as drawSymbol and PickLotteryWinner require.
func FlipDuel(rng randSource) (challengerWins bool) {
	return rng.Intn(2) == 0
}

// DuelPayout is the duel's payout table: the winner takes the whole pot,
// 2 × bet (their own stake back plus the loser's), and the loser takes 0.
// Returned as a pair rather than as "the winner's amount" so the caller
// cannot mix up which account it is crediting — the loser's 0 is an explicit
// settlement, not an omission.
//
// A bet outside [1, MaxChips] pays nothing at all. That range is enforced by
// AcceptDuel before this is reached, so the guard is defense in depth of the
// arithmetic rather than a behaviour anyone can observe: without it an
// out-of-range bet from a future caller would compute a pot that wraps
// int64, and a wrapped pot credits a negative payout. Inside the range the
// widest value here is 2 × MaxChips = 2e12, 4.6e6x below math.MaxInt64.
func DuelPayout(bet int64, challengerWins bool) (challengerPayout, opponentPayout int64) {
	if bet < 1 || bet > MaxChips {
		return 0, 0
	}
	pot := 2 * bet
	if challengerWins {
		return pot, 0
	}
	return 0, pot
}
