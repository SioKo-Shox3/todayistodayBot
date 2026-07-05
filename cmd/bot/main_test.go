package main

import (
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
