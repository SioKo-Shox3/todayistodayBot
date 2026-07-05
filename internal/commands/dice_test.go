package commands

import (
	"strings"
	"testing"
)

func TestDiceCommand_Roll_SingleRoll_Format(t *testing.T) {
	cmd := &DiceCommand{}
	msg, err := cmd.roll(6, 1, func(n int) int { return 4 }) // deterministic roll func for testing
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "🎲 6面ダイスを1回振って、結果は **4** です！" {
		t.Fatalf("unexpected message: %q", msg)
	}
}

func TestDiceCommand_Roll_MultipleRolls_Format(t *testing.T) {
	cmd := &DiceCommand{}
	seq := []int{2, 5, 3}
	idx := 0
	msg, err := cmd.roll(6, 3, func(n int) int {
		v := seq[idx]
		idx++
		return v
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "**10**") {
		t.Fatalf("expected total 10, got: %q", msg)
	}
	// Full-width parens + full-width plus — matches DiceCommand.cs:79-80
	// exactly. This assertion fails if the parens were accidentally ASCII,
	// unlike a loose "contains 2＋5＋3" check without the parens.
	if !strings.Contains(msg, "（2＋5＋3）") {
		t.Fatalf("expected full-width-paren individual results, got: %q", msg)
	}
}

func TestDiceCommand_ValidateArgs_SidesTooLow(t *testing.T) {
	cmd := &DiceCommand{}
	if err := cmd.validateArgs(1, 1); err == nil {
		t.Fatal("expected error for sides < 2")
	}
}

func TestDiceCommand_ValidateArgs_RollsTooMany(t *testing.T) {
	cmd := &DiceCommand{}
	if err := cmd.validateArgs(6, 101); err == nil {
		t.Fatal("expected error for rolls > 100")
	}
}

func TestDiceCommand_ValidateArgs_SidesTooHigh(t *testing.T) {
	cmd := &DiceCommand{}
	if err := cmd.validateArgs(1000001, 1); err == nil {
		t.Fatal("expected error for sides > 1,000,000")
	}
}
