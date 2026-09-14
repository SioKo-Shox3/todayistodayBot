package casino

import (
	"context"
	"log/slog"
	"time"
)

// AnnouncementJob is a neutral (discordgo-free) description of one guild's
// pending 9am rate announcement — internal/commands/casino_announce.go
// turns this into an actual discordgo.MessageEmbed and posts it.
type AnnouncementJob struct {
	GuildID     string
	ChannelID   string
	Today       DailyRate
	RecentRates []DailyRate // oldest..newest, up to 7 entries (includes Today as the last element)
	TopAssets   []RankEntry // top 3 (設計書)
	// JackpotPool is the guild's slot jackpot pool at collection time, AFTER
	// the daily rollover has seeded it (ensureTodayRateLocked calls
	// seedJackpotLocked), so a guild that has never spun is shown the real
	// JackpotSeed rather than a pool of 0.
	JackpotPool int64
	// LotteryDraws is every settled draw still waiting to be announced,
	// oldest first — Lottery.Unannounced as it stood at collection time. It
	// is empty on a morning nobody entered, and holds more than one entry
	// when an outage left an older draw unposted while the next morning's
	// draw settled on top of it.
	//
	// The queue is the whole point: a single slot would let the second draw
	// overwrite the first, and that winner would never be announced or
	// celebrated at all. The dates are therefore not necessarily yesterday's,
	// which is why the embed prints each draw's own Date instead of saying
	// 「昨日」. Entries are retired by MarkAnnounced alone, which the
	// scheduler calls only after a confirmed send.
	LotteryDraws []LotteryDraw
	// LotteryPrize / LotteryTickets describe the draw that is now OPEN (the
	// one the readers can still buy into), read after the rollover cleared
	// the settled pot — so they are today's fresh numbers, which for a guild
	// with no carryover are 0.
	LotteryPrize   int64
	LotteryTickets int
	// SeasonMonth is the JST month ("2006-01") of the season that is RUNNING
	// at collection time, and SeasonTop its podium so far — read after the
	// rollover, so on the 1st of a month they describe the month that has
	// just opened rather than the one that was paid out a microsecond ago.
	// SeasonTop is empty in a month nobody has won or lost anything in yet,
	// which is a normal morning, not a missing value (SeasonRanks excludes
	// 純利 0).
	SeasonMonth string
	SeasonTop   []SeasonRank
	// SeasonClosed is the most recently closed season when its public result
	// has NOT been posted yet, and nil otherwise — including on every
	// ordinary morning of a month, since a season closes only once.
	//
	// Kept until a send succeeds, the discipline the lottery queue follows:
	// MarkSeasonAnnounced is called only after a confirmed post, so an
	// outage across the 1st leaves the result here and the next pass that
	// reaches this guild posts it. The month it names is the month that
	// CLOSED, not today's.
	SeasonClosed *SeasonResult
}

// collectDailyAnnouncements ensures every known guild's rate exists for
// now's JST calendar date, then returns an AnnouncementJob for every guild
// with an AnnounceChannelID configured that has not yet been announced
// today (today > LastAnnounced, a high-water mark; 設計書). Does NOT mark LastAnnounced (only
// MarkAnnounced does, after a confirmed send) and does NOT send anything.
func (s *Store) collectDailyAnnouncements(now time.Time) ([]AnnouncementJob, error) {
	today := jstDate(now)
	var jobs []AnnouncementJob
	err := s.Update(func(d *Data) error {
		for guildID := range *d {
			// Deliberately re-fetch via ensureGuildLocked rather than using
			// the value from `range *d`: a hand-edited or partially-written
			// file can hold `{"guild1": null}`, and a nil *GuildEconomy
			// would panic on the first field access — which, in a discordgo
			// handler goroutine, kills the whole process (§3.3).
			economy := ensureGuildLocked(d, guildID)
			rate := ensureTodayRateLocked(economy, now, s.rng)
			// `>=`, not `!=`: MarkAnnounced only ever moves LastAnnounced
			// FORWARD, so a clock that rolls back would otherwise make every
			// pass of the earlier day look unannounced and re-post it (the
			// boundary can never catch up again). Comparing as a high-water
			// mark keeps the two sides of the invariant in step.
			if economy.AnnounceChannelID == "" || today <= economy.LastAnnounced {
				continue
			}
			recent := economy.Rates
			if len(recent) > 7 {
				recent = recent[len(recent)-7:]
			}
			// Every lottery field is read after ensureTodayRateLocked, which
			// is what performed this morning's draw — so the job carries the
			// settled result and the freshly opened pot, not yesterday's.
			sold, _, prize := lotterySummaryLocked(&economy.Lottery)
			top := topAssetsLocked(economy, rate.Rate, 3)
			// AFTER topAssetsLocked on purpose: SeasonRanks compares the
			// stored 純利 without bounding it, and that loop is what
			// normalises every account on the way past (SeasonStatus does the
			// same normalisation for the same reason). seasonRankLimit, not a
			// literal 3, so the podium the embed shows cannot drift away from
			// the podium the rollover pays.
			seasonTop := SeasonRanks(economy.Users, seasonRankLimit)
			job := AnnouncementJob{
				GuildID: guildID, ChannelID: economy.AnnounceChannelID,
				Today: rate, RecentRates: recent,
				TopAssets:      top,
				JackpotPool:    economy.Jackpot, // read after ensureTodayRateLocked seeded it
				LotteryPrize:   prize,
				LotteryTickets: sold,
				SeasonMonth:    economy.SeasonMonth,
				SeasonTop:      seasonTop,
			}
			// A closed season is pending until its own mark catches up with
			// it — `>` and not `!=`, the high-water rule LastAnnounced
			// follows: a clock that steps back must not re-open a month that
			// has already been celebrated and @mention its winners twice.
			// Copied, slice included, for the reason LotteryDraws is: the
			// value behind LastSeason belongs to the Data this Update is
			// about to drop, and the job outlives it.
			if last := economy.LastSeason; last != nil && last.Month > economy.LastSeasonAnnounced {
				closed := *last
				closed.Ranks = append([]SeasonRank(nil), last.Ranks...)
				job.SeasonClosed = &closed
			}
			// Handed over whole, with no date filter of its own: the queue
			// already IS the list of draws that have not been announced, and
			// re-filtering it here by LastAnnounced would re-introduce the
			// very assumption (one pending draw, dated after the last
			// posting) the queue exists to break. Copied because the slice
			// behind it belongs to the Data this Update is about to drop.
			if pending := economy.Lottery.Unannounced; len(pending) > 0 {
				job.LotteryDraws = append([]LotteryDraw(nil), pending...)
			}
			jobs = append(jobs, job)
		}
		return nil
	})
	return jobs, err
}

// MarkAnnounced records guildID as announced for `date` and retires every
// lottery draw that posting covered. Call ONLY after a confirmed successful
// send — leave both untouched on failure so tomorrow's 9am wake naturally
// retries (設計書: 送信失敗はログして継続).
//
// The queue is pruned by `Date <= date` rather than emptied outright: a draw
// that settled WHILE the message was in flight carries a later date and must
// survive to be posted next time. Both sides are "YYYY-MM-DD", so the
// lexicographic order is the calendar one.
//
// LastAnnounced only ever moves FORWARD. It is a high-water mark — "every
// day up to here has had its posting" — and both readers treat it as one:
// collectDailyAnnouncements skips a guild whose mark already reads today,
// and migrateUnannouncedLocked calls a draw unposted when it is dated after
// the mark. A clock that steps back (NTP correction, a host with the wrong
// date) posts for the earlier day, and letting that posting pull the mark
// back would re-open both tests on days that are already done: the later
// day would be announced a second time and its winner re-queued and
// re-celebrated. Pruning is NOT gated the same way — those draws really
// were in the message that just went out, whatever today's date says.
func (s *Store) MarkAnnounced(guildID, date string) error {
	return s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		if date > economy.LastAnnounced {
			economy.LastAnnounced = date
		}
		lottery := &economy.Lottery
		kept := lottery.Unannounced[:0]
		for _, draw := range lottery.Unannounced {
			if draw.Date > date {
				kept = append(kept, draw)
			}
		}
		if len(kept) == 0 {
			lottery.Unannounced = nil // keep omitempty's promise: no empty array on disk
		} else {
			lottery.Unannounced = kept
		}
		return nil
	})
}

// MarkSeasonAnnounced records that guildID's closed season for `month`
// ("2006-01") has been posted. Call ONLY after a confirmed successful send,
// for MarkAnnounced's reason — leaving the mark behind is what makes the
// next pass carry the result again (設計書 C-3b §4 掲示).
//
// It is a SEPARATE call from MarkAnnounced, not a second field on it,
// because the two messages fail independently: the daily embed goes out
// first and the season's result follows it, so a result that could not be
// delivered must be retried WITHOUT re-posting the embed that already
// landed (and without re-pinging the lottery winner alongside it).
//
// FORWARD ONLY, exactly like LastAnnounced: a host whose clock or zone data
// steps back would otherwise let an already-celebrated month look pending
// again, and its podium would be @mentioned a second time.
//
// An empty month is a no-op ("" > "" is false), so a caller that reaches
// here with no closed season to mark cannot blank the boundary.
func (s *Store) MarkSeasonAnnounced(guildID, month string) error {
	return s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		if month > economy.LastSeasonAnnounced {
			economy.LastSeasonAnnounced = month
		}
		return nil
	})
}

// nextRunAt returns the next 9:00 JST instant strictly after now (an input
// exactly at 9:00:00.000 JST rolls to the FOLLOWING day — a deliberate
// simplification avoiding a double-fire race right at the boundary).
func nextRunAt(now time.Time) time.Time {
	j := now.In(jst)
	next := time.Date(j.Year(), j.Month(), j.Day(), 9, 0, 0, 0, jst)
	if !next.After(j) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// AnnounceCallback is invoked once per pending job by RunAnnounceScheduler.
// Returning nil means "sent successfully" (triggers MarkAnnounced); a
// non-nil error is logged here and left for tomorrow's retry.
type AnnounceCallback func(job AnnouncementJob) error

// SleepFunc blocks until `until` or ctx cancellation, returning false if
// ctx was cancelled (caller should stop) or true if it woke at `until`.
// Exported because RunAnnounceScheduler (exported) takes one as a
// parameter — an exported function must not have an unexported type in its
// signature, or callers outside this package cannot supply their own.
type SleepFunc func(ctx context.Context, until time.Time) bool

// RealSleepUntil is the production SleepFunc — a real, cancellable timer
// wait. Exported so internal/commands/casino_announce.go can pass it.
func RealSleepUntil(ctx context.Context, until time.Time) bool {
	timer := time.NewTimer(time.Until(until))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// announceWindowOpen reports whether now is at or after 09:00 JST on now's
// OWN JST calendar date — i.e. whether today's scheduled announcement time
// has already arrived.
//
// This is the LOWER BOUND of the catch-up rule, and it is not optional: it
// is what "毎朝9時(JST)に掲示する" means. There is deliberately NO upper
// bound (see runAnnouncePass).
func announceWindowOpen(now time.Time) bool {
	j := now.In(jst)
	return !j.Before(time.Date(j.Year(), j.Month(), j.Day(), 9, 0, 0, 0, jst))
}

// runAnnouncePass performs exactly one sweep:
//
//  1. ALWAYS ensure every known guild has today's rate — regardless of the
//     time of day. This is 設計書 L170-174「レートは絶対に欠損しない」.
//  2. ONLY IF announceWindowOpen(now): post + mark every guild that is
//     configured for announcements and has not been announced today.
//
// Errors are logged and never abort the sweep (設計書: 送信失敗はログして
// 継続し、レート生成には影響させない).
//
// WHY THE 09:00 LOWER BOUND (ユーザー決定の確定解釈): a 03:00 restart must
// NOT post. That day's 09:00 has not happened yet, so posting now would set
// LastAnnounced, the 09:00 wake six hours later would find nothing pending,
// and the day's announcement would have been permanently relocated to the
// middle of the night — directly contradicting 設計書「毎朝9時(JST)に
// 掲示する」.
//
// WHY NO UPPER BOUND: a 23:00 restart DOES post, late. That day's 09:00 has
// already passed and was missed, so a late post is better than losing the
// day entirely. The user chose this behaviour knowing that a late-night
// restart produces a late-night announcement. "時間窓を切らない" means "no
// upper bound", not "no lower bound".
func runAnnouncePass(ctx context.Context, st *Store, callback AnnounceCallback, now time.Time) {
	jobs, err := st.collectDailyAnnouncements(now) // step 1: rates, unconditionally
	if err != nil {
		slog.Error("casino: collectDailyAnnouncements failed", "error", err)
		return
	}
	if !announceWindowOpen(now) {
		// Before 09:00 JST: today's rate is now guaranteed to exist, but the
		// scheduled posting time has not arrived. The loop below sleeps to
		// this very same 09:00 and posts then.
		return
	}
	for _, job := range jobs { // step 2: post, no upper bound
		if ctx.Err() != nil {
			return // shutting down: stop starting new sends
		}
		if err := callback(job); err != nil {
			slog.Error("casino: announcement callback failed", "guild_id", job.GuildID, "error", err)
			continue // leave LastAnnounced untouched so a later pass retries
		}
		if err := st.MarkAnnounced(job.GuildID, job.Today.Date); err != nil {
			slog.Error("casino: MarkAnnounced failed", "guild_id", job.GuildID, "error", err)
		}
	}
}

// RunAnnounceScheduler blocks until ctx is cancelled. It runs ONE pass
// immediately at startup (the catch-up pass), then sleeps to the next
// 09:00 JST and runs another after every wake. now/sleep are injected so
// tests never block on a real clock.
//
// STARTUP CATCH-UP (ユーザー決定): without this first pass, LastAnnounced
// could never become "today" through any code path — the persisted field
// the design doc mandates would be dead code — and a bot that was down at
// 09:00 would skip that day's announcement forever. The pass always
// backfills today's rate; whether it also POSTS is decided by
// runAnnouncePass's 09:00 lower bound (no upper bound).
//
// NOTE the single `t := now()` per iteration: the pass and the sleep target
// are computed from the SAME instant on purpose. Reading the clock twice
// would open a narrow hole — booting at 08:59:59.9, skipping the post
// (window not open), then computing nextRunAt from a clock that has since
// ticked past 09:00 and sleeping until TOMORROW, silently losing today's
// announcement. With one reading, nextRunAt(08:59:59.9) is today's 09:00,
// so the very next wake posts.
//
// NO DOUBLE FIRE, three independent reasons:
//  1. collectDailyAnnouncements skips any guild with LastAnnounced >= today,
//     and MarkAnnounced sets exactly that after every successful send.
//  2. The 09:00 lower bound means a pre-09:00 startup cannot consume the
//     day's slot early.
//  3. nextRunAt rolls an input of exactly 09:00:00.000 to the FOLLOWING
//     day, so a boot at precisely 09:00 posts once and then sleeps a full
//     day instead of re-firing immediately.
//
// (If a pass ever ran so long that nextRunAt(t) is already in the past, the
// timer fires at once and the extra pass is a harmless no-op: every guild
// is already marked for today.)
func RunAnnounceScheduler(ctx context.Context, st *Store, callback AnnounceCallback, now func() time.Time, sleep SleepFunc) {
	for {
		if ctx.Err() != nil {
			return
		}
		t := now()
		runAnnouncePass(ctx, st, callback, t)
		if !sleep(ctx, nextRunAt(t)) {
			return
		}
	}
}
