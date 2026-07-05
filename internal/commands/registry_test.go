package commands

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

type fakeCommand struct {
	name string
}

func (f *fakeCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{Name: f.name}
}

func (f *fakeCommand) Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return nil
}

func TestRegisterAndAll(t *testing.T) {
	resetForTest()
	Register(&fakeCommand{name: "foo"})
	Register(&fakeCommand{name: "bar"})

	all := All()
	if len(all) != 2 {
		t.Fatalf("expected 2 registered commands, got %d", len(all))
	}
	names := map[string]bool{}
	for _, c := range all {
		names[c.Definition().Name] = true
	}
	if !names["foo"] || !names["bar"] {
		t.Fatalf("expected foo and bar registered, got %v", names)
	}
}
