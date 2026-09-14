package main

import (
	"errors"
	"testing"

	"github.com/SioKo-Shox3/todayistodayBot/internal/commands"
	"github.com/bwmarrin/discordgo"
)

type stubCommand struct {
	name    string
	handled bool
}

func (s *stubCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{Name: s.name}
}

func (s *stubCommand) Handle(sess *discordgo.Session, i *discordgo.InteractionCreate) error {
	s.handled = true
	return nil
}

func TestFindCommandByName(t *testing.T) {
	cmds := []commands.Command{
		&stubCommand{name: "ping"},
		&stubCommand{name: "dice"},
	}

	found := findCommandByName(cmds, "dice")
	if found == nil {
		t.Fatal("expected to find 'dice' command")
	}
	if found.Definition().Name != "dice" {
		t.Fatalf("expected dice command, got %q", found.Definition().Name)
	}

	notFound := findCommandByName(cmds, "nonexistent")
	if notFound != nil {
		t.Fatal("expected nil for unregistered command name")
	}
}

// --- 評価者の指摘(反復 1): 返金は受付より先 --------------------------------

// 設計書 §3: the boot-time refund returns the escrow of boards that died with
// the previous process. That reading only holds while no new game can start,
// so the refund must come BEFORE the gateway opens and before the commands
// are registered. With the old order a /blackjack started during boot had its
// fresh escrow refunded out from under it, and settling that hand answered
// ErrNoGameInProgress.
func TestStartupRefundsBeforeAcceptingAnything(t *testing.T) {
	var order []string
	steps := startupSteps{
		refundStaleEscrows: func() (int, error) { order = append(order, "refund"); return 2, nil },
		openSession:        func() error { order = append(order, "open"); return nil },
		registerCommands:   func() error { order = append(order, "register"); return nil },
	}

	if err := steps.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"refund", "open", "register"}
	if len(order) != len(want) {
		t.Fatalf("ran %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ran %v, want %v", order, want)
		}
	}
}

// A refund that did not happen must not be followed by an open gateway: the
// bot would take bets while stale escrow still blocks those very accounts.
func TestStartupStopsWhenTheRefundFails(t *testing.T) {
	refusal := errors.New("the casino file is unwritable")
	var accepted bool
	steps := startupSteps{
		refundStaleEscrows: func() (int, error) { return 0, refusal },
		openSession:        func() error { accepted = true; return nil },
		registerCommands:   func() error { accepted = true; return nil },
	}

	err := steps.run()
	if !errors.Is(err, refusal) {
		t.Fatalf("run returned %v, want the refund's own error", err)
	}
	if accepted {
		t.Error("the bot started accepting interactions after the refund failed")
	}
}

// The commands are registered only once the gateway is up, and a failure
// there is reported rather than swallowed.
func TestStartupStopsWhenRegisteringCommandsFails(t *testing.T) {
	refusal := errors.New("discord refused the command")
	steps := startupSteps{
		refundStaleEscrows: func() (int, error) { return 0, nil },
		openSession:        func() error { return nil },
		registerCommands:   func() error { return refusal },
	}

	if err := steps.run(); !errors.Is(err, refusal) {
		t.Fatalf("run returned %v, want the registration error", err)
	}
}
