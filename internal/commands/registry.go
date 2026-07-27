package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

// Command is the contract every internal/commands/*.go file implements and
// self-registers via init(). main.go only depends on this interface and on
// All() — it never imports individual command types.
type Command interface {
	Definition() *discordgo.ApplicationCommand
	Handle(s *discordgo.Session, i *discordgo.InteractionCreate) error
}

var (
	registered      []Command
	registeredNames = map[string]bool{}
)

// Register adds cmd to the set dispatched by main.go. Call this from an
// init() function in the file that defines the command — see ping.go for
// the canonical example. Do not call Register from anywhere except an
// init() in this package; main.go must not need to change when a command
// is added. Register panics if cmd.Definition().Name duplicates an already
// registered command — this is a startup-time programming-error check (all
// Register calls happen from init(), before the bot ever runs), not a
// runtime failure path.
func Register(cmd Command) {
	name := cmd.Definition().Name
	if registeredNames[name] {
		panic(fmt.Sprintf("commands: duplicate command name %q registered via Register — every command must have a unique Definition().Name", name))
	}
	registeredNames[name] = true
	registered = append(registered, cmd)
}

// All returns every self-registered command, in registration order.
func All() []Command {
	return registered
}

// resetForTest clears the registry; test-only helper so each test starts
// from a clean slate despite the package-level var being shared across the
// init()-registered production commands in the same test binary.
func resetForTest() {
	registered = nil
	registeredNames = map[string]bool{}
}
