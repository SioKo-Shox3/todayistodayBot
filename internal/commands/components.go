package commands

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// custom_id の規約(設計書 §4): casino:<game>:<sessionID>:<action>
// 例 casino:highlow:8f3a…:high。Discord の custom_id 上限は 100 文字。
const (
	componentIDNamespace = "casino"
	componentIDSeparator = ":"
	componentIDMaxLen    = 100
	componentIDParts     = 4
)

// BuildCustomID composes the custom_id carried by a casino game button.
// The caller is responsible for keeping sessionID short enough that the
// result stays within componentIDMaxLen — ParseCustomID rejects anything
// longer, so an over-long ID fails the round trip instead of silently
// reaching Discord.
func BuildCustomID(game, sessionID, action string) string {
	return strings.Join([]string{componentIDNamespace, game, sessionID, action}, componentIDSeparator)
}

// ParseCustomID splits a custom_id built by BuildCustomID. ok is false for
// anything this package did not build: a different namespace, an ID over
// 100 characters (Discord would have rejected it on the way out), a part
// count other than 4 (too few *or* too many — no game, session ID or
// action of ours contains the separator), or an empty element.
func ParseCustomID(id string) (game, sessionID, action string, ok bool) {
	if len(id) > componentIDMaxLen {
		return "", "", "", false
	}
	parts := strings.Split(id, componentIDSeparator)
	if len(parts) != componentIDParts {
		return "", "", "", false
	}
	if parts[0] != componentIDNamespace {
		return "", "", "", false
	}
	for _, p := range parts[1:] {
		if p == "" {
			return "", "", "", false
		}
	}
	return parts[1], parts[2], parts[3], true
}

// ComponentHandler is the contract a game implements to receive its button
// presses. Prefix() is the <game> element of the custom_id ("highlow",
// "blackjack"); sessionID and action are the parsed remainder.
type ComponentHandler interface {
	Prefix() string
	HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate, sessionID, action string) error
}

var registeredComponents = map[string]ComponentHandler{}

// RegisterComponent adds h to the set dispatched by DispatchComponent.
// Call it from an init() in the file that defines the game, the same way
// Register works for slash commands — main.go must not change when a game
// is added. Duplicate prefixes panic: every RegisterComponent call happens
// from init(), before the bot runs, so this is a startup-time
// programming-error check rather than a runtime failure path.
func RegisterComponent(h ComponentHandler) {
	prefix := h.Prefix()
	if _, dup := registeredComponents[prefix]; dup {
		panic(fmt.Sprintf("commands: duplicate component prefix %q registered via RegisterComponent — every game must have a unique Prefix()", prefix))
	}
	registeredComponents[prefix] = h
}

// unknownComponentMessage は、この Bot が知らないボタン(古いメッセージ・
// 未登録のゲーム・壊れた custom_id)を押されたときの文言。
const unknownComponentMessage = "❌ このボタンは無効です"

// notSessionOwnerMessage は、他人のゲームのボタンを押されたときの文言
// (設計書 §9)。
const notSessionOwnerMessage = "❌ これはあなたのゲームではありません"

// ephemeralResponse wraps content as a reply only the presser can see.
func ephemeralResponse(content string) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}
}

// DispatchComponent routes a message-component interaction to the game
// that registered the custom_id's prefix. An unparsable ID or an
// unregistered prefix gets the ephemeral ❌ reply — never silence, because
// an unanswered interaction shows the user "この操作は失敗しました".
func DispatchComponent(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// Comma-ok rather than i.MessageComponentData(): that accessor panics on
	// anything but a component interaction, and a dispatcher must not take
	// the process down over a payload Discord shaped unexpectedly.
	if data, isComponent := i.Data.(discordgo.MessageComponentInteractionData); isComponent {
		if game, sessionID, action, ok := ParseCustomID(data.CustomID); ok {
			if h, found := registeredComponents[game]; found {
				return h.HandleComponent(s, i, sessionID, action)
			}
		}
	}
	return respond(s, i.Interaction, ephemeralResponse(unknownComponentMessage))
}

// requireSessionOwner returns a non-empty ❌ Japanese message if the user
// who pressed the button is not the session's owner, or "" if the check
// passes. Sending it ephemerally is the caller's job — mirroring
// requireGuildContext / requireAdministrator, which also only return text.
func requireSessionOwner(i *discordgo.InteractionCreate, ownerID string) string {
	presser := resolveUserID(i)
	// An empty ID means the interaction carried neither Member nor User, or
	// the session lost its owner. Comparing "" == "" would hand the board to
	// anyone, so treat either empty side as a mismatch.
	if presser == "" || ownerID == "" || presser != ownerID {
		return notSessionOwnerMessage
	}
	return ""
}

// resetComponentsForTest clears the component registry; test-only helper,
// mirroring resetForTest, so each test starts from a clean slate despite
// the package-level map being shared with the init()-registered production
// games in the same test binary.
func resetComponentsForTest() {
	registeredComponents = map[string]ComponentHandler{}
}
