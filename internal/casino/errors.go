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
)

// ErrInsufficientChips carries the current balance so the command layer can
// render "❌ チップが足りません(現在: N枚)" without a second store round-trip.
type ErrInsufficientChips struct{ Balance int64 }

func (e *ErrInsufficientChips) Error() string {
	return fmt.Sprintf("casino: insufficient chips (balance=%d)", e.Balance)
}

// ErrInsufficientCoins mirrors ErrInsufficientChips for coin→chip exchange.
type ErrInsufficientCoins struct{ Balance int64 }

func (e *ErrInsufficientCoins) Error() string {
	return fmt.Sprintf("casino: insufficient coins (balance=%d)", e.Balance)
}
