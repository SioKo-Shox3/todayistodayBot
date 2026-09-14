package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

var announceMedals = []string{"🥇", "🥈", "🥉"}

// buildAnnouncementEmbed renders one AnnouncementJob into the 9am posting.
// 設計書 L62-68 requires FOUR elements and all four are present here:
//  1. today's rate + day-over-day change (rateChangeLine, shared with /rate
//     so the two renderings cannot disagree — they used to differ on
//     diff == 0)
//  2. the 7-day sparkline
//  3. the 相場コメント (marketCommentary)
//  4. the top-3 total-asset ranking
//
// The 🚀/💥 tag comes from the PERSISTED casino.EventKind via
// rateChangeLine, not from a day-over-day threshold. A threshold cannot
// work: the 70/140 clamp turns a genuine +15% surge from 130 into a
// +7.7% move, so a 10% threshold would silently drop 🚀 on exactly the
// days that most deserve it (§3.2).
func buildAnnouncementEmbed(job casino.AnnouncementJob) *discordgo.MessageEmbed {
	values := make([]int, len(job.RecentRates))
	for i, r := range job.RecentRates {
		values[i] = r.Rate
	}

	changeLine := rateChangeLine(job.RecentRates)   // rate.go (Task 11)
	commentary := marketCommentary(job.RecentRates) // casino_shared.go (Task 8)

	rankLines := make([]string, 0, len(job.TopAssets))
	for i, e := range job.TopAssets {
		marker := fmt.Sprintf("%d.", i+1)
		if i < len(announceMedals) {
			marker = announceMedals[i]
		}
		rankLines = append(rankLines, fmt.Sprintf("%s <@%s> — %d", marker, e.UserID, e.TotalAssets))
	}
	rankValue := "まだ誰も口座を持っていません。"
	if len(rankLines) > 0 {
		rankValue = strings.Join(rankLines, "\n")
	}

	return &discordgo.MessageEmbed{
		Title:       "💱 本日のコインレート",
		Description: fmt.Sprintf("1コイン = %dチップ\n%s\n直近%d日: %s\n%s", job.Today.Rate, changeLine, len(job.RecentRates), sparkline(values), commentary),
		Color:       0xf1c40f,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "総資産ランキング TOP3", Value: rankValue, Inline: false},
		},
	}
}

// startAnnounceScheduler is StartCasinoAnnounceScheduler's testable core:
// it takes the store, a send function, a clock and a sleeper instead of a
// *discordgo.Session, so tests can drive it with casino.New(t.TempDir()),
// a fake sender and a FIXED clock.
//
// now and sleep MUST be parameters, not time.Now/casino.RealSleepUntil
// hardcoded here. casino.runAnnouncePass only posts when
// casino.announceWindowOpen(now) is true, i.e. from 09:00 JST onward. With a
// hardcoded real clock, any test that asserts "send was called" would fail
// (or hang until go test's 10-minute timeout) whenever the suite happens to
// run between 00:00 and 08:59 JST, and any test written to avoid that hang
// would assert nothing at all — a test that cannot fail is not evidence
// (Docs/agent-guide/build-and-verify.md). casino.SleepFunc is already
// exported for exactly this reason (see its doc comment, Task 6).
func startAnnounceScheduler(ctx context.Context, st *casino.Store, send func(channelID string, embed *discordgo.MessageEmbed) error, now func() time.Time, sleep casino.SleepFunc) (wait func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		casino.RunAnnounceScheduler(ctx, st, func(job casino.AnnouncementJob) error {
			return send(job.ChannelID, buildAnnouncementEmbed(job))
		}, now, sleep)
	}()
	return func() { <-done }
}

// StartCasinoAnnounceScheduler starts the 9am-JST casino rate announcement
// scheduler as a background goroutine and returns immediately, together
// with a wait function that blocks until that goroutine has fully exited.
// This is NOT a slash command — it has no Definition()/Handle() and is not
// Register()'d — it is Discord-facing infrastructure wiring for
// internal/casino.RunAnnounceScheduler (itself discordgo-free).
// cmd/bot/main.go calls this exactly once (Task 17).
//
// The caller MUST invoke wait() after cancelling ctx and BEFORE closing the
// session: the scheduler can be inside s.ChannelMessageSendEmbed when the
// shutdown signal arrives, and tearing the session down underneath it is a
// use-after-close race. Returning nothing (as an earlier draft did) makes
// that impossible to get right from the outside.
//
// This is the ONLY place that supplies the real clock and the real sleeper.
func StartCasinoAnnounceScheduler(ctx context.Context, s *discordgo.Session) (wait func()) {
	return startAnnounceScheduler(ctx, casino.Default(), func(channelID string, embed *discordgo.MessageEmbed) error {
		_, err := s.ChannelMessageSendEmbed(channelID, embed)
		if err != nil {
			// The scheduler logs whatever this returns, so redact here:
			// this is the only place the real Discord error (a *url.Error
			// carrying the request URL) enters the announcement path.
			return errors.New(redactInteractionError(err))
		}
		return nil
	}, time.Now, casino.RealSleepUntil)
}
