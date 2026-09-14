package casino

import (
	"errors"
	"fmt"
)

// This package returns only structured sentinel/typed errors and never
// carries user-facing Japanese wording — internal/commands owns every ❌
// message and translates these with errors.Is/errors.As, exactly as
// internal/store does for the chosei-sama commands.
var (
	ErrAlreadyClaimedToday  = errors.New("casino: daily bonus already claimed today")
	ErrBetOutOfRange        = errors.New("casino: bet out of range")
	ErrInvalidAmount        = errors.New("casino: amount must be a positive integer")
	ErrBelowMinimumExchange = errors.New("casino: exchange result would be below the 1-coin minimum")
	ErrAmountOverflow       = errors.New("casino: amount would overflow int64")
	// ErrChipCapExceeded / ErrCoinCapExceeded are the economic-cap
	// violations: the credit would push the account past MaxChips/MaxCoins.
	// Returning them from an Update closure aborts the whole transaction, so
	// nothing (not even a partial debit) is persisted.
	ErrChipCapExceeded = errors.New("casino: chip balance would exceed MaxChips")
	ErrCoinCapExceeded = errors.New("casino: coin balance would exceed MaxCoins")
	// ErrGameInProgress / ErrNoGameInProgress guard the escrow state machine
	// (設計書 §3). An account holds at most one in-flight game: opening a
	// second one would silently overwrite the first bet's escrow (the chips
	// would be unrecoverable), and settling an account with no escrow would
	// pay a second time for a hand already settled. Both are returned from
	// inside the Update closure, so a refusal persists nothing.
	ErrGameInProgress   = errors.New("casino: a game is already in progress")
	ErrNoGameInProgress = errors.New("casino: no game in progress")
	// ErrDuelSelf refuses a duel whose two sides are the same user. The
	// command layer rejects this first with its own 「❌ 自分自身とは対戦
	// できません」 (設計書 §4.5), so this is defense in depth — but of the
	// arithmetic, not of the wording. ensureAccountLocked would hand back
	// the SAME *UserAccount for both sides, and the settlement would then
	// stake one bet, pay out two, and mint 2 × bet chips out of nothing.
	ErrDuelSelf = errors.New("casino: cannot duel yourself")
	// ErrDuelStakeMismatch means the challenger's escrow is not the bet the
	// acceptance is settling. The duel is a zero-sum transfer only while the
	// two stakes are equal: the pot paid to the winner is 2 × bet, so a
	// challenger holding anything else in escrow makes the settlement create
	// or destroy the difference. It can only be reached by a hand-edited
	// file or a caller that lost track of its own board, and in both cases
	// refusing (which persists nothing, leaving the escrow intact) is the
	// only answer that cannot move chips.
	ErrDuelStakeMismatch = errors.New("casino: challenger's escrow does not match the duel bet")
)

// ErrInsufficientChips carries the current balance so the command layer can
// render "❌ チップが足りません(現在: N枚)" without a second store round-trip.
type ErrInsufficientChips struct{ Balance int64 }

func (e *ErrInsufficientChips) Error() string {
	return fmt.Sprintf("casino: insufficient chips (balance=%d)", e.Balance)
}

// ErrLotteryLimit is returned when a purchase would take the buyer past the
// per-draw ticket cap (LotteryMaxTicketsPerDraw). It carries the headroom
// that is actually left so the command layer can render
// "❌ 1 回の抽選で買えるのは 10 枚までです(あと N 枚)" without a second
// store round-trip — same shape, and same reason, as ErrInsufficientChips.
// Remaining is never negative: a hand-edited holding above the cap reports 0.
type ErrLotteryLimit struct{ Remaining int }

func (e *ErrLotteryLimit) Error() string {
	return fmt.Sprintf("casino: lottery ticket cap reached (remaining=%d)", e.Remaining)
}

// ErrInsufficientCoins mirrors ErrInsufficientChips for coin→chip exchange.
type ErrInsufficientCoins struct{ Balance int64 }

func (e *ErrInsufficientCoins) Error() string {
	return fmt.Sprintf("casino: insufficient coins (balance=%d)", e.Balance)
}
