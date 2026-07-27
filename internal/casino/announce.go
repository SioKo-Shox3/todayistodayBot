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
}

// collectDailyAnnouncements ensures every known guild's rate exists for
// now's JST calendar date, then returns an AnnouncementJob for every guild
// with an AnnounceChannelID configured that has not yet been announced
// today (LastAnnounced != today; 設計書). Does NOT mark LastAnnounced (only
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
			rate := ensureTodayRateLocked(economy, today, s.rng)
			if economy.AnnounceChannelID == "" || economy.LastAnnounced == today {
				continue
			}
			recent := economy.Rates
			if len(recent) > 7 {
				recent = recent[len(recent)-7:]
			}
			jobs = append(jobs, AnnouncementJob{
				GuildID: guildID, ChannelID: economy.AnnounceChannelID,
				Today: rate, RecentRates: recent,
				TopAssets: topAssetsLocked(economy, rate.Rate, 3),
			})
		}
		return nil
	})
	return jobs, err
}

// MarkAnnounced records guildID as announced for `date`. Call ONLY after a
// confirmed successful send — leave LastAnnounced untouched on failure so
// tomorrow's 9am wake naturally retries (設計書: 送信失敗はログして継続).
func (s *Store) MarkAnnounced(guildID, date string) error {
	return s.Update(func(d *Data) error {
		ensureGuildLocked(d, guildID).LastAnnounced = date
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
//  1. collectDailyAnnouncements skips any guild with LastAnnounced == today,
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
