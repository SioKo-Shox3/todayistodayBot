package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
// 設計書 C-3a §2 adds a fifth: the slot jackpot pool, so the 9am posting is
// where the guild watches the pool grow. It comes from the job (collected
// under the store's lock, after the rollover seeded it), never from a fresh
// read here — this function stays pure.
//
// 設計書 C-3a §3 adds a sixth: the lottery — this morning's settled draw and
// the pot now open for buying (lotteryAnnounceLines). Same rule: every
// number comes from the job, which collected them inside the Update that
// performed the draw, so the embed cannot show a pot that was emptied a
// microsecond after it was read.
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
			{Name: "🎰 ジャックポット", Value: fmt.Sprintf("%d チップ", job.JackpotPool), Inline: false},
			{Name: "🎟️ 宝くじ", Value: lotteryAnnounceLines(job), Inline: false},
		},
	}
}

// lotteryAnnounceLines renders the 🎟️ 宝くじ field's body: this morning's
// settled draw on the first line, and on the second the pot that is open for
// buying right now (設計書 C-3a §3 掲示).
//
// The winner is mentioned with <@id> exactly as /lottery status does
// (lottery.go's lotteryLastDrawLine) — two renderings of one draw must not
// disagree.
//
// A nil LotteryDraw is not a missing value, it is what a day nobody entered
// looks like: drawLotteryLocked leaves LastDraw untouched then and rolls the
// entire prize forward, so 「繰り越し」 is literally what happened and the
// second line's amount IS that carried-over prize. Tickets read 0 枚 at 09:00
// on every normal morning — the draw just emptied the pot — and only climb
// above 0 here when the announcement is a late catch-up posted after people
// have already started buying into the new pot.
func lotteryAnnounceLines(job casino.AnnouncementJob) string {
	result := "🏆 昨日の当選: 該当者なし — 賞金は繰り越し"
	if d := job.LotteryDraw; d != nil && d.WinnerID != "" {
		result = fmt.Sprintf("🏆 昨日の当選: <@%s> が %dチップ 獲得(%d枚 / %d人)", d.WinnerID, d.Prize, d.TicketsSold, d.Buyers)
	}
	return fmt.Sprintf("%s\n💰 本日の賞金: %dチップ / 🎫 売れた枚数: %d枚", result, job.LotteryPrize, job.LotteryTickets)
}

// lotteryAnnounceCelebration renders the SEPARATE public message a won draw
// posts, in the same shape as slot.go's jackpot celebration
// (slotCelebrationMessage): an embed field is easy to scroll past, and the
// whole point of the mention is that the winner gets pinged.
//
// "" means「何も投稿しない」: no draw, no winner named, or a prize of 0. The
// last case is not hypothetical — drawLotteryLocked pays through
// creditChipsCappedLocked, so a winner already at MaxChips is recorded with
// Prize 0 and the whole pot rolls forward instead. 「0 チップ 獲得!!」 would
// be a celebration of nothing; the embed field still reports that draw
// exactly as it was recorded.
func lotteryAnnounceCelebration(draw *casino.LotteryDraw) string {
	if draw == nil || draw.WinnerID == "" || draw.Prize <= 0 {
		return ""
	}
	return fmt.Sprintf("🎉🎉🎉 <@%s> が 🎟️ 宝くじに当選!! %d チップ 獲得!! 🎉🎉🎉", draw.WinnerID, draw.Prize)
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
//
// sendText is the second, plain-text sender the lottery celebration needs.
// It is a separate parameter rather than a second call on one sender
// because the two have opposite failure semantics, encoded below.
func startAnnounceScheduler(ctx context.Context, st *casino.Store, send func(channelID string, embed *discordgo.MessageEmbed) error, sendText func(channelID, content string) error, now func() time.Time, sleep casino.SleepFunc) (wait func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		casino.RunAnnounceScheduler(ctx, st, func(job casino.AnnouncementJob) error {
			// The embed decides the job: its error propagates, so
			// runAnnouncePass leaves LastAnnounced untouched and a later
			// pass retries the whole posting.
			if err := send(job.ChannelID, buildAnnouncementEmbed(job)); err != nil {
				return err
			}
			// The celebration is posted AFTER, and only on success, for
			// exactly that reason: posting it first would re-post it on
			// every retry of a failing embed, @mentioning the winner again
			// each time.
			celebration := lotteryAnnounceCelebration(job.LotteryDraw)
			if celebration == "" {
				return nil
			}
			if err := sendText(job.ChannelID, celebration); err != nil {
				// Log only, and return nil. The draw is already committed
				// to the store and the announcement is already up; failing
				// the job here would re-post the embed tomorrow morning and
				// still could not un-lose this message. The result stays
				// visible via /lottery status (設計書 C-3a §3).
				slog.Error("discord: ChannelMessageSend failed for a lottery celebration", "guild_id", job.GuildID, "error", err)
			}
			return nil
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
	}, func(channelID, content string) error {
		_, err := s.ChannelMessageSend(channelID, content)
		if err != nil {
			return errors.New(redactInteractionError(err)) // same redaction, same reason
		}
		return nil
	}, time.Now, casino.RealSleepUntil)
}
