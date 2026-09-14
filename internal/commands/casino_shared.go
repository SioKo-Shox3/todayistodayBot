package commands

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino" // required by rateRankCommentary/marketCommentary below
	"github.com/bwmarrin/discordgo"
)

// guildOnlyContexts restricts a slash command to guild contexts (hidden in
// DMs client-side). Shared Definition() field for all 7 casino commands.
var guildOnlyContexts = []discordgo.InteractionContextType{discordgo.InteractionContextGuild}

// permPtr returns a pointer to p — ApplicationCommand.DefaultMemberPermissions is *int64.
func permPtr(p int64) *int64 { return &p }

// requireGuildContext returns a non-empty ❌ Japanese message if i was
// invoked outside a guild, or "" if the check passes. All 7 casino
// commands call this first, IN ADDITION to Contexts: &guildOnlyContexts in
// Definition() — Discord's client-side Contexts enforcement is not a
// substitute for this server-side check (設計書の二重ガード方針).
func requireGuildContext(i *discordgo.InteractionCreate) string {
	if i.GuildID == "" {
		return "❌ このコマンドはサーバー内で使用してください。"
	}
	return ""
}

// requireAdministrator returns a non-empty ❌ Japanese message (設計書の
// 文言と完全一致させる) if the invoking member lacks Administrator,
// or "" if the check passes. /casino-admin only, in addition to
// DefaultMemberPermissions in Definition().
func requireAdministrator(i *discordgo.InteractionCreate) string {
	if i.Member == nil || i.Member.Permissions&discordgo.PermissionAdministrator == 0 {
		return "❌ このコマンドはサーバー管理者のみ使用できます"
	}
	return ""
}

var sparkChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline renders values (oldest..newest rate integers) as a block-chart
// string (設計書). Each value is bucketed into 8 levels by its
// position within [min(values), max(values)]; a flat series renders the
// lowest bar for every point.
func sparkline(values []int) string {
	if len(values) == 0 {
		return ""
	}
	lo, hi := values[0], values[0]
	for _, v := range values {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	span := hi - lo
	var b strings.Builder
	for _, v := range values {
		idx := 0
		if span > 0 {
			idx = (v - lo) * (len(sparkChars) - 1) / span
		}
		b.WriteRune(sparkChars[idx])
	}
	return b.String()
}

// rateRankCommentary returns a one-line "参謀コメント" (設計書の例:
// 「今日のレートは直近7日で2番目の高値です」) ranking today's rate (the last
// entry of history) among up to the last 7 entries. history must be
// oldest..newest. Disclosed interpretation (設計書 gives an example
// sentence, not an exact algorithm): rank = 1-indexed position when the
// window is sorted descending by Rate.
//
// The empty guard is not decorative: every caller runs inside a discordgo
// handler goroutine, which discordgo starts WITHOUT recover
// (discordgo@v0.29.0/event.go:171), so an index-out-of-range here would
// kill the whole bot process. A doc-comment contract is not a guard.
func rateRankCommentary(history []casino.DailyRate) string {
	if len(history) == 0 {
		return ""
	}
	window := history
	if len(window) > 7 {
		window = window[len(window)-7:]
	}
	today := window[len(window)-1].Rate
	rank := 1
	for _, r := range window {
		if r.Rate > today {
			rank++
		}
	}
	return fmt.Sprintf("今日のレートは直近%d日で%d番目の高値です", len(window), rank)
}

// trendStreak counts how many consecutive trailing entries of history share
// the newest entry's TrendState (>=1 for non-empty history, 0 for empty).
func trendStreak(history []casino.DailyRate) int {
	if len(history) == 0 {
		return 0
	}
	last := history[len(history)-1].Trend
	streak := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Trend != last {
			break
		}
		streak++
	}
	return streak
}

// marketCommentary returns the 相場コメント line required by 設計書 L66
// (掲示embedの3要素目; example: 「コイン強気3日目。天井はどこだ?」).
// history must be oldest..newest; the last entry is today.
//
// It reads the PERSISTED casino.EventKind rather than guessing a surge or
// crash from the day-over-day percentage: the 70/140 clamp can hide a real
// event behind a small visible move (§3.2). An event outranks the trend
// because a surge/crash is the headline of that day. Both "" and
// casino.EventNone mean "no event" ("" can only come from a hand-edited or
// pre-C-1 record).
func marketCommentary(history []casino.DailyRate) string {
	if len(history) == 0 {
		return ""
	}
	today := history[len(history)-1]
	switch today.Event {
	case casino.EventSurge:
		return "🚀 暴騰デー! コインが跳ねた。売り抜けるなら今日だ。"
	case casino.EventCrash:
		return "💥 暴落デー! コインが崩れた。拾いに行くか、様子を見るか。"
	}
	streak := trendStreak(history)
	switch today.Trend {
	case casino.TrendBull:
		return fmt.Sprintf("コイン強気%d日目。天井はどこだ?", streak)
	case casino.TrendBear:
		return fmt.Sprintf("コイン弱気%d日目。底値を狙うなら今か。", streak)
	default:
		return fmt.Sprintf("凪%d日目。動かない相場も相場だ。", streak)
	}
}

// discordTokenInPath matches the secret segment of the two Discord REST
// paths that carry one in the URL itself: /interactions/<id>/<token>/... and
// /webhooks/<id>/<token>/.... The first group keeps the route (and the
// non-secret id) so a redacted line still says WHICH endpoint failed.
var discordTokenInPath = regexp.MustCompile(`(/(?:interactions|webhooks)/[^/\s]+/)[^/\s?]+`)

// redactInteractionError renders err for a log line with the Discord token
// removed. net/http puts the full request URL into *url.Error, so logging a
// failed InteractionRespond verbatim writes the interaction token — and a
// failed webhook call the webhook token — into the log file. The interaction
// token is short-lived and is not the bot token, but it authorises replies to
// that interaction for its lifetime and there is no reason to keep it.
//
// *url.Error is reduced to its Op plus the redacted cause; the URL is
// dropped entirely. Anything else keeps its message with the token segment of
// any embedded path replaced by [redacted], because errors from other layers
// (discordgo's own RESTError, a wrapped fmt.Errorf) can quote a path too.
// Every log site in this package passes its error through here, so no call
// site has to decide whether its error could contain a URL.
func redactInteractionError(err error) string {
	if err == nil {
		return "<nil>"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Op + ": " + redactInteractionError(urlErr.Err)
	}
	return discordTokenInPath.ReplaceAllString(err.Error(), "${1}[redacted]")
}
