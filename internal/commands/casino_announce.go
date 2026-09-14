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
			{Name: "🏆 シーズン(今月)", Value: seasonAnnounceLines(job), Inline: false},
		},
	}
}

// lotteryAnnounceLines renders the 🎟️ 宝くじ field's body: one line per
// settled draw still waiting to be announced, then the pot that is open for
// buying right now (設計書 C-3a §3 掲示).
//
// The winner is mentioned with <@id> exactly as /lottery status does
// (lottery.go's lotteryLastDrawLine) — two renderings of one draw must not
// disagree.
//
// An EMPTY LotteryDraws is not a missing value, it is what a day nobody
// entered looks like: drawLotteryLocked queues nothing then and rolls the
// entire prize forward, so 「繰り越し」 is literally what happened and the
// last line's amount IS that carried-over prize. Tickets read 0 枚 at 09:00
// on every normal morning — the draw just emptied the pot — and only climb
// above 0 here when the announcement is a late catch-up posted after people
// have already started buying into the new pot.
//
// Every line carries its draw's OWN date, not 「昨日」, and there can be more
// than one: an outage that swallowed a posting leaves the older draw queued
// while the next morning settles on top of it, and the channel is owed both.
// Calling either of them 「昨日」 would be a lie about which draw the numbers
// belong to.
func lotteryAnnounceLines(job casino.AnnouncementJob) string {
	lines := make([]string, 0, len(job.LotteryDraws)+1)
	for _, d := range job.LotteryDraws {
		if d.WinnerID == "" {
			continue // a hand-edited queue entry naming nobody has nothing to report
		}
		lines = append(lines, fmt.Sprintf("🏆 %s の当選: <@%s> が %dチップ 獲得(%d枚 / %d人)",
			lotteryDrawDateLabel(d.Date), d.WinnerID, d.Prize, d.TicketsSold, d.Buyers))
	}
	if len(lines) == 0 {
		lines = append(lines, "🏆 前回の当選: 該当者なし — 賞金は繰り越し")
	}
	lines = append(lines, fmt.Sprintf("💰 本日の賞金: %dチップ / 🎫 売れた枚数: %d枚", job.LotteryPrize, job.LotteryTickets))
	return strings.Join(lines, "\n")
}

// seasonAnnounceLines renders the 🏆 シーズン(今月) field's body: the running
// season's month and its podium so far (設計書 C-3b §4 掲示).
//
// 純利 carries its sign here exactly as /season prints it (season.go's
// formatSeasonMessage), and the empty-month sentence is the same one: two
// renderings of one table must not disagree about what "nobody has played
// yet" looks like. An empty SeasonTop is that case and nothing else —
// SeasonRanks excludes 純利 0, so a guild whose accounts exist only because
// /balance opened them shows this line rather than a podium of people who
// never placed a bet.
//
// The month is printed even though the field is headed 今月: the posting can
// be a late catch-up, and the heading alone would then name the wrong month.
func seasonAnnounceLines(job casino.AnnouncementJob) string {
	if len(job.SeasonTop) == 0 {
		return fmt.Sprintf("%s — まだ誰も勝敗を記録していません。", job.SeasonMonth)
	}
	lines := make([]string, 0, len(job.SeasonTop)+1)
	lines = append(lines, fmt.Sprintf("%s の純利ランキング", job.SeasonMonth))
	for i, rank := range job.SeasonTop {
		marker := fmt.Sprintf("%d.", i+1)
		if i < len(announceMedals) {
			marker = announceMedals[i]
		}
		lines = append(lines, fmt.Sprintf("%s <@%s> — 純利 %+d", marker, rank.UserID, rank.Net))
	}
	return strings.Join(lines, "\n")
}

// seasonAnnounceCelebration renders the SEPARATE public message a closed
// season posts, for lotteryAnnounceCelebration's reason: the winners are
// @mentioned, and a mention buried in an embed field is easy to scroll past.
//
// Bonus is printed as CREDITED, never as SeasonBonus offers it — a winner
// already at the chip cap took only what fit (types.go), and the message
// beside their balance must not promise chips that never arrived.
//
// "" means 「何も投稿しない」 and has one structural cause: a month that
// closed with nobody on the podium (an empty guild, or one where every
// account ended on 純利 0). There is no one to ping and no prize to report.
// The caller still marks that season announced — there is nothing left to
// retry, and leaving it pending would re-ask the question every morning.
//
// The podium is capped at the medals it has, which is the same three places
// rolloverSeasonLocked pays: a hand-edited last_season holding fifty rows
// must not turn the celebration into fifty mentions.
func seasonAnnounceCelebration(result *casino.SeasonResult) string {
	if result == nil || len(result.Ranks) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🎉🎉🎉 %s のシーズンが終了!! 🎉🎉🎉\n", result.Month)
	for i, rank := range result.Ranks {
		if i >= len(announceMedals) {
			break
		}
		fmt.Fprintf(&b, "%s <@%s> — 純利 %+d / 賞与 %dチップ\n", announceMedals[i], rank.UserID, rank.Net, rank.Bonus)
	}
	fmt.Fprintf(&b, "参加者: %d人 — 新しいシーズンが始まりました!", result.Players)
	return b.String()
}

// lotteryDrawDateLabel renders a stored draw date ("2006-01-02") as the M/D
// the announcement heading shows. A date that fails to parse is printed as
// stored rather than dropped or replaced with a guess: a hand-edited file must
// not be able to make the embed claim a day the draw did not happen on.
func lotteryDrawDateLabel(date string) string {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return fmt.Sprintf("%d/%d", int(d.Month()), d.Day())
}

// lotteryAnnounceCelebration renders the SEPARATE public message a won draw
// posts, in the same shape as slot.go's jackpot celebration
// (slotCelebrationMessage): an embed field is easy to scroll past, and the
// whole point of the mention is that the winner gets pinged.
//
// "" means「何も投稿しない」, and the ONLY reasons are structural: there was
// no draw, or the draw named nobody. A named winner is always celebrated,
// including one recorded with Prize 0 — drawLotteryLocked pays through
// creditChipsCappedLocked, so a winner already at MaxChips keeps the win but
// receives nothing — the unpaid remainder follows the house's cut into the
// jackpot pool rather than forward to the next draw, which would hand it to
// somebody else. Suppressing that message would
// drop the ping for a real winner; the amount is printed as recorded, so the
// message stays honest about what actually landed in the account.
func lotteryAnnounceCelebration(draw *casino.LotteryDraw) string {
	if draw == nil || draw.WinnerID == "" {
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
			// every retry of a failing embed, @mentioning the winners
			// again each time. One message per queued draw: each named a
			// different winner and each is owed their ping.
			for i := range job.LotteryDraws {
				celebration := lotteryAnnounceCelebration(&job.LotteryDraws[i])
				if celebration == "" {
					continue
				}
				if err := sendText(job.ChannelID, celebration); err != nil {
					// Log only, and keep going. The draws are already committed
					// to the store and the announcement is already up; failing
					// the job here would re-post the embed tomorrow morning and
					// still could not un-lose this message. The results stay
					// visible via /lottery status (設計書 C-3a §3).
					slog.Error("discord: ChannelMessageSend failed for a lottery celebration", "guild_id", job.GuildID, "error", err)
				}
			}
			// 設計書 C-3b §4 掲示: a season that has closed since the last
			// posting gets its own public message. Only a closed one — on
			// every other morning SeasonClosed is nil and nothing is sent.
			//
			// The mark is taken HERE, right after the confirmed send, and
			// not by runAnnouncePass with the rest of the job: a failure
			// must leave the result pending WITHOUT failing the job, because
			// failing it would re-post the embed (and re-ping the lottery
			// winner) on the next pass of the same day. Left unmarked, the
			// result is carried by the next pass that reaches this guild,
			// and it stays readable through /season in the meantime.
			if job.SeasonClosed != nil {
				if celebration := seasonAnnounceCelebration(job.SeasonClosed); celebration != "" {
					if err := sendText(job.ChannelID, celebration); err != nil {
						slog.Error("discord: ChannelMessageSend failed for a season result", "guild_id", job.GuildID, "error", err)
						return nil // the embed is up; leave the season for the next pass
					}
				}
				if err := st.MarkSeasonAnnounced(job.GuildID, job.SeasonClosed.Month); err != nil {
					slog.Error("casino: MarkSeasonAnnounced failed", "guild_id", job.GuildID, "error", err)
				}
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
