package casino

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Every scheduler test in this file pins its own clock. Nothing here may read
// the wall clock: a test whose result depends on WHEN `go test` ran is not
// evidence (§3.6). The announcement path is especially exposed to this,
// because announceWindowOpen is a SECOND suppressor — an unpinned clock lets
// a "callback was not called" assertion pass merely because the run happened
// before 09:00 JST, never testing the condition the test was written for.
var (
	announceDay        = "2026-07-10"
	announceAt0300     = time.Date(2026, 7, 10, 3, 0, 0, 0, jst)             // before the window opens
	announceAt0859599  = time.Date(2026, 7, 10, 8, 59, 59, 900_000_000, jst) // 08:59:59.9 — 100ms before it opens
	announceAt0900     = time.Date(2026, 7, 10, 9, 0, 0, 0, jst)             // exactly the lower bound
	announceAt1000     = time.Date(2026, 7, 10, 10, 0, 0, 0, jst)            // window open
	announceAt2330     = time.Date(2026, 7, 10, 23, 30, 0, 0, jst)           // late-night catch-up (no upper bound)
	announceNextDay900 = time.Date(2026, 7, 11, 9, 0, 0, 0, jst)
)

// seedAnnounceGuild pins guildID's announcement configuration directly,
// WITHOUT going through MarkAnnounced/SetAnnounceChannel: a test must not
// seed its precondition with the code it is testing. It preserves whatever
// rates and users the guild already holds.
func seedAnnounceGuild(t *testing.T, st *Store, guildID, channelID, lastAnnounced string) {
	t.Helper()
	err := st.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		economy.AnnounceChannelID = channelID
		economy.LastAnnounced = lastAnnounced
		return nil
	})
	if err != nil {
		t.Fatalf("seeding announce config for %s: %v", guildID, err)
	}
}

// assertTodayRateExists reads guildID back from disk and returns its rate for
// now's JST calendar date, failing if the rate is missing. This is the "レート
// は絶対に欠損しない" half of every scheduler assertion.
func assertTodayRateExists(t *testing.T, path, guildID string, now time.Time) DailyRate {
	t.Helper()
	economy := readGuild(t, path, guildID)
	today := jstDate(now)
	for _, rate := range economy.Rates {
		if rate.Date == today {
			return rate
		}
	}
	t.Fatalf("guild %q has no rate for %s: %+v", guildID, today, economy.Rates)
	return DailyRate{}
}

// nowScript hands RunAnnounceScheduler a pre-scripted sequence of instants,
// one per loop iteration. Overrunning the script is recorded rather than
// fatal, so the assertion (not a panic in a nested call) reports it.
type nowScript struct {
	instants []time.Time
	calls    int
	overrun  bool
}

func (n *nowScript) next() time.Time {
	if n.calls >= len(n.instants) {
		n.overrun = true
		n.calls++
		return n.instants[len(n.instants)-1]
	}
	v := n.instants[n.calls]
	n.calls++
	return v
}

// sleepScript is the stub that replaces the real timer in EVERY scheduler
// test: it records the requested wake instant and returns a scripted result
// (missing entry => false => the scheduler returns). No test in this package
// ever waits on a real clock (§3.6-3); a package runtime of seconds rather
// than milliseconds means a real sleep leaked in.
type sleepScript struct {
	results []bool
	untils  []time.Time
	calls   int
	hook    func(call int) // runs before returning, for cancel/observation
}

func (s *sleepScript) sleep(_ context.Context, until time.Time) bool {
	call := s.calls
	s.calls++
	s.untils = append(s.untils, until)
	if s.hook != nil {
		s.hook(call)
	}
	if call >= len(s.results) {
		return false
	}
	return s.results[call]
}

// countingCallback records every job it was handed and reports success.
// Tests that need a FAILING send use their own closure instead.
type countingCallback struct {
	jobs []AnnouncementJob
}

func (c *countingCallback) call(job AnnouncementJob) error {
	c.jobs = append(c.jobs, job)
	return nil
}

func TestNextRunAt_BeforeNineAM_SameDayNineAM(t *testing.T) {
	got := nextRunAt(announceAt0300)

	if !got.Equal(announceAt0900) {
		t.Fatalf("nextRunAt(%s) = %s, want %s", announceAt0300, got, announceAt0900)
	}
}

func TestNextRunAt_AfterNineAM_NextDayNineAM(t *testing.T) {
	got := nextRunAt(announceAt1000)

	if !got.Equal(announceNextDay900) {
		t.Fatalf("nextRunAt(%s) = %s, want %s", announceAt1000, got, announceNextDay900)
	}
}

// TestNextRunAt_ExactlyNineAM_NextDayNineAM pins the deliberate boundary
// choice: an input of exactly 09:00:00.000 rolls to the FOLLOWING day, so a
// boot at precisely 09:00 posts once and then sleeps a full day instead of
// re-firing immediately.
func TestNextRunAt_ExactlyNineAM_NextDayNineAM(t *testing.T) {
	got := nextRunAt(announceAt0900)

	if !got.Equal(announceNextDay900) {
		t.Fatalf("nextRunAt(%s) = %s, want %s", announceAt0900, got, announceNextDay900)
	}
}

// TestNextRunAt_UTCInput_ConvertsToJSTCorrectly feeds a non-JST instant:
// 2026-07-10T00:30:00Z is 09:30 JST, i.e. already past today's run, so the
// next run is tomorrow 09:00 JST. Reading the calendar fields before
// converting would see "00:30 on the 10th" and wrongly answer today.
func TestNextRunAt_UTCInput_ConvertsToJSTCorrectly(t *testing.T) {
	input := time.Date(2026, 7, 10, 0, 30, 0, 0, time.UTC)

	got := nextRunAt(input)

	if !got.Equal(announceNextDay900) {
		t.Fatalf("nextRunAt(%s) = %s, want %s", input, got, announceNextDay900)
	}
}

func TestAnnounceWindowOpen_BeforeNineAM_False(t *testing.T) {
	if announceWindowOpen(announceAt0300) {
		t.Fatalf("announceWindowOpen(%s) = true, want false — 09:00 JST has not arrived", announceAt0300)
	}
	if announceWindowOpen(announceAt0859599) {
		t.Fatalf("announceWindowOpen(%s) = true, want false — 100ms before the bound", announceAt0859599)
	}
}

// TestAnnounceWindowOpen_ExactlyNineAM_True pins that the bound is "at or
// after", not "strictly after".
func TestAnnounceWindowOpen_ExactlyNineAM_True(t *testing.T) {
	if !announceWindowOpen(announceAt0900) {
		t.Fatalf("announceWindowOpen(%s) = false, want true — the bound is inclusive", announceAt0900)
	}
}

func TestAnnounceWindowOpen_AfterNineAM_True(t *testing.T) {
	if !announceWindowOpen(announceAt1000) {
		t.Fatalf("announceWindowOpen(%s) = false, want true", announceAt1000)
	}
}

// TestAnnounceWindowOpen_LateNight_True pins the "no upper bound" decision:
// a 23:30 restart still counts as inside the window.
func TestAnnounceWindowOpen_LateNight_True(t *testing.T) {
	if !announceWindowOpen(announceAt2330) {
		t.Fatalf("announceWindowOpen(%s) = false, want true — there is deliberately NO upper bound", announceAt2330)
	}
}

// TestAnnounceWindowOpen_NonJSTInput_ConvertedFirst is the §3.7-3 discipline
// applied to the window: convert to JST FIRST, then compare. 00:30Z is 09:30
// JST (open) and 23:30Z on the previous day is 08:30 JST (closed) — both
// answers invert if the calendar fields are read in the input's own zone.
func TestAnnounceWindowOpen_NonJSTInput_ConvertedFirst(t *testing.T) {
	open := time.Date(2026, 7, 10, 0, 30, 0, 0, time.UTC) // 09:30 JST
	closed := time.Date(2026, 7, 9, 23, 30, 0, 0, time.UTC)

	if !announceWindowOpen(open) {
		t.Fatalf("announceWindowOpen(%s) = false, want true (= 09:30 JST)", open)
	}
	if announceWindowOpen(closed) {
		t.Fatalf("announceWindowOpen(%s) = true, want false (= 08:30 JST)", closed)
	}
}

func TestCollectDailyAnnouncements_GeneratesRateIfMissing(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "a guild's first day is the fixed 基準100, not a draw"}
	seedAnnounceGuild(t, st, "guild1", "chan1", "")

	jobs, err := st.collectDailyAnnouncements(announceAt1000)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}

	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	job := jobs[0]
	if job.GuildID != "guild1" || job.ChannelID != "chan1" {
		t.Fatalf("job = %+v, want GuildID=guild1 ChannelID=chan1", job)
	}
	if job.Today.Date != announceDay || job.Today.Rate != baseRate {
		t.Fatalf("job.Today = %+v, want {Date:%s Rate:%d}", job.Today, announceDay, baseRate)
	}
	if len(job.RecentRates) != 1 || job.RecentRates[0] != job.Today {
		t.Fatalf("job.RecentRates = %+v, want exactly [%+v]", job.RecentRates, job.Today)
	}
	persisted := assertTodayRateExists(t, path, "guild1", announceAt1000)
	if persisted != job.Today {
		t.Fatalf("persisted rate %+v != returned rate %+v", persisted, job.Today)
	}
}

func TestCollectDailyAnnouncements_SkipsNoChannelConfigured(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "a guild's first day is the fixed 基準100, not a draw"}
	seedAnnounceGuild(t, st, "guild1", "", "")

	jobs, err := st.collectDailyAnnouncements(announceAt1000)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}

	if len(jobs) != 0 {
		t.Fatalf("got %d jobs, want 0 (no announce channel configured): %+v", len(jobs), jobs)
	}
	// The rate is generated anyway — 設計書「レートは絶対に欠損しない」.
	assertTodayRateExists(t, path, "guild1", announceAt1000)
}

func TestCollectDailyAnnouncements_SkipsAlreadyAnnouncedToday(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "a guild's first day is the fixed 基準100, not a draw"}
	seedAnnounceGuild(t, st, "guild1", "chan1", announceDay)

	jobs, err := st.collectDailyAnnouncements(announceAt1000)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}

	if len(jobs) != 0 {
		t.Fatalf("got %d jobs, want 0 (already announced today): %+v", len(jobs), jobs)
	}
	assertTodayRateExists(t, path, "guild1", announceAt1000)
}

func TestCollectDailyAnnouncements_IncludesUpTo7RatesAndTop3(t *testing.T) {
	st, _ := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	// Ten consecutive days ending today, so no draw is needed at all.
	for i := 0; i < 10; i++ {
		seedRate(t, st, "guild1", announceAt1000.AddDate(0, 0, i-9), 101+i)
	}
	st.rng = forbiddenRand{t: t, reason: "today's rate already exists, so no new draw may happen"}
	const todayRate = 110
	seedAccounts(t, st, "guild1", map[string]UserAccount{
		"userA": {Chips: 1000},           // 1000
		"userB": {Coins: 100},            // 11000
		"userC": {Chips: 500, Coins: 10}, // 1600
		"userD": {Chips: 50000},          // 50000
		"userE": {Coins: 1},              // 110
	})

	jobs, err := st.collectDailyAnnouncements(announceAt1000)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}

	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	job := jobs[0]
	if job.Today.Rate != todayRate || job.Today.Date != announceDay {
		t.Fatalf("job.Today = %+v, want {Date:%s Rate:%d}", job.Today, announceDay, todayRate)
	}
	if len(job.RecentRates) != 7 {
		t.Fatalf("len(job.RecentRates) = %d, want 7: %+v", len(job.RecentRates), job.RecentRates)
	}
	if job.RecentRates[0].Rate != 104 {
		t.Fatalf("job.RecentRates[0] = %+v, want the 7th-most-recent day (rate 104)", job.RecentRates[0])
	}
	if job.RecentRates[6] != job.Today {
		t.Fatalf("job.RecentRates[6] = %+v, want today's rate %+v as the last element", job.RecentRates[6], job.Today)
	}
	want := []RankEntry{
		{UserID: "userD", TotalAssets: 50000},
		{UserID: "userB", TotalAssets: 100 * todayRate},
		{UserID: "userC", TotalAssets: 500 + 10*todayRate},
	}
	if len(job.TopAssets) != len(want) {
		t.Fatalf("len(job.TopAssets) = %d, want 3: %+v", len(job.TopAssets), job.TopAssets)
	}
	for i, entry := range want {
		if job.TopAssets[i] != entry {
			t.Fatalf("job.TopAssets[%d] = %+v, want %+v (full: %+v)", i, job.TopAssets[i], entry, job.TopAssets)
		}
	}
}

// TestCollectDailyAnnouncements_NilGuildValue_DoesNotPanic covers the §3.3 nil
// pointer path: a hand-edited or partially-written file can hold
// {"guild1": null}, and a nil *GuildEconomy would panic on the first field
// access — which, in a discordgo handler goroutine, kills the whole process.
func TestCollectDailyAnnouncements_NilGuildValue_DoesNotPanic(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "a guild's first day is the fixed 基準100, not a draw"}
	if err := os.WriteFile(path, []byte(`{"guild1": null}`), 0o644); err != nil {
		t.Fatalf("writing hand-edited data file: %v", err)
	}

	jobs, err := st.collectDailyAnnouncements(announceAt1000)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}

	if len(jobs) != 0 {
		t.Fatalf("got %d jobs, want 0 (the repaired guild has no announce channel): %+v", len(jobs), jobs)
	}
	assertTodayRateExists(t, path, "guild1", announceAt1000)
}

// TestCollectDailyAnnouncements_NilAccountDoesNotPanic is the announcement
// half of the nil-account regression. The nil-guild guard above does not cover
// it: ensureGuildLocked repairs the guild and its Users map, but a null value
// INSIDE that map survives into topAssetsLocked, so the 9am top-3 build would
// panic — in the scheduler goroutine, with no recover anywhere above it.
func TestCollectDailyAnnouncements_NilAccountDoesNotPanic(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "today's rate is already in the file, so no draw may happen"}
	writeHandEditedFile(t, path, fmt.Sprintf(
		`{"guild1":{"announce_channel_id":"chan1","last_announced":"",`+
			`"rates":[{"date":%q,"rate":100,"trend":"flat","event":"none"}],`+
			`"users":{"ghost":null,"user-a":{"chips":100,"coins":1}}}}`,
		announceDay))

	jobs, err := st.collectDailyAnnouncements(announceAt1000)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}

	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	want := RankEntry{UserID: "user-a", TotalAssets: 200} // 100 + 1*100
	if len(jobs[0].TopAssets) != 1 || jobs[0].TopAssets[0] != want {
		t.Fatalf("job.TopAssets = %+v, want exactly [%+v] (the nil account must be skipped, not ranked)", jobs[0].TopAssets, want)
	}
	economy := readGuild(t, path, "guild1")
	if account := economy.Users["ghost"]; account != nil {
		t.Fatalf("building the announcement opened an account for the nil user: %+v", account)
	}
}

func TestMarkAnnounced_PersistsLastAnnouncedDate(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")

	if err := st.MarkAnnounced("guild1", announceDay); err != nil {
		t.Fatalf("MarkAnnounced returned error: %v", err)
	}

	economy := readGuild(t, path, "guild1")
	if economy.LastAnnounced != announceDay {
		t.Fatalf("LastAnnounced = %q, want %q", economy.LastAnnounced, announceDay)
	}
	if economy.AnnounceChannelID != "chan1" {
		t.Fatalf("MarkAnnounced clobbered AnnounceChannelID: %+v", economy)
	}
}

// TestRunAnnounceScheduler_StopsWhenContextCancelled runs the startup pass at
// a FIXED 10:00 JST and has the sleep stub cancel the context and report
// "stop", so the loop must return immediately. No real clock wait occurs.
func TestRunAnnounceScheduler_StopsWhenContextCancelled(t *testing.T) {
	st, _ := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callback := &countingCallback{}
	now := &nowScript{instants: []time.Time{announceAt1000}}
	sleep := &sleepScript{hook: func(int) { cancel() }}

	RunAnnounceScheduler(ctx, st, callback.call, now.next, sleep.sleep)

	if now.calls != 1 {
		t.Fatalf("now() was called %d times, want exactly 1 (one reading per loop iteration)", now.calls)
	}
	if sleep.calls != 1 {
		t.Fatalf("sleep was called %d times, want exactly 1", sleep.calls)
	}
	if len(callback.jobs) != 1 {
		t.Fatalf("callback was called %d times, want 1 (the startup pass runs before the first sleep)", len(callback.jobs))
	}
}

// TestRunAnnounceScheduler_MarksOnlySuccessfullyCallbackedGuilds pins that a
// failed send leaves LastAnnounced untouched, so a later pass retries it.
func TestRunAnnounceScheduler_MarksOnlySuccessfullyCallbackedGuilds(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	seedAnnounceGuild(t, st, "guild2", "chan2", "")
	sendErr := errors.New("discord: 403 missing access")
	var attempted []string
	callback := func(job AnnouncementJob) error {
		attempted = append(attempted, job.GuildID)
		if job.GuildID == "guild2" {
			return sendErr
		}
		return nil
	}
	now := &nowScript{instants: []time.Time{announceAt1000}}
	sleep := &sleepScript{}

	RunAnnounceScheduler(context.Background(), st, callback, now.next, sleep.sleep)

	if len(attempted) != 2 {
		t.Fatalf("callback was called for %v, want both guilds", attempted)
	}
	if got := readGuild(t, path, "guild1").LastAnnounced; got != announceDay {
		t.Fatalf("guild1 LastAnnounced = %q, want %q (its send succeeded)", got, announceDay)
	}
	if got := readGuild(t, path, "guild2").LastAnnounced; got != "" {
		t.Fatalf("guild2 LastAnnounced = %q, want it left empty — a failed send must stay retryable", got)
	}
}

// TestRunAnnounceScheduler_StartupCatchUp_AfterNineAM_Announces is D1: a bot
// that was down at 09:00 and comes back at 10:00 still posts that day.
func TestRunAnnounceScheduler_StartupCatchUp_AfterNineAM_Announces(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", jstYesterday(announceAt1000))
	callback := &countingCallback{}
	now := &nowScript{instants: []time.Time{announceAt1000}}
	sleep := &sleepScript{}

	RunAnnounceScheduler(context.Background(), st, callback.call, now.next, sleep.sleep)

	if len(callback.jobs) != 1 {
		t.Fatalf("callback was called %d times, want exactly 1", len(callback.jobs))
	}
	if got := readGuild(t, path, "guild1").LastAnnounced; got != announceDay {
		t.Fatalf("LastAnnounced = %q, want %q", got, announceDay)
	}
}

// TestRunAnnounceScheduler_StartupCatchUp_BeforeNineAM_GeneratesRateOnly is
// the regression test for the 09:00 LOWER BOUND, and it is not optional. If a
// 03:00 restart posted, LastAnnounced would be set, the 09:00 wake six hours
// later would find nothing pending, and that day's announcement would have
// been permanently relocated to the middle of the night — a direct violation
// of 設計書「毎朝9時(JST)に掲示する」.
func TestRunAnnounceScheduler_StartupCatchUp_BeforeNineAM_GeneratesRateOnly(t *testing.T) {
	st, path := newTempStore(t)
	yesterday := jstYesterday(announceAt0300)
	seedAnnounceGuild(t, st, "guild1", "chan1", yesterday)
	callback := &countingCallback{}
	now := &nowScript{instants: []time.Time{announceAt0300}}
	sleep := &sleepScript{}

	RunAnnounceScheduler(context.Background(), st, callback.call, now.next, sleep.sleep)

	if len(callback.jobs) != 0 {
		t.Fatalf("callback was called %d times before 09:00 JST, want 0: %+v", len(callback.jobs), callback.jobs)
	}
	economy := readGuild(t, path, "guild1")
	if economy.LastAnnounced != yesterday {
		t.Fatalf("LastAnnounced = %q, want it left at %q — a pre-09:00 pass must not consume the day's slot", economy.LastAnnounced, yesterday)
	}
	// ...but the rate is still generated unconditionally.
	assertTodayRateExists(t, path, "guild1", announceAt0300)
}

// TestRunAnnounceScheduler_StartupCatchUp_LateNight_StillAnnounces pins the
// "no UPPER bound" half of the same decision: that day's 09:00 has already
// passed and was missed, so a late post beats losing the day entirely. The
// user chose this knowing a late-night restart produces a late-night post.
func TestRunAnnounceScheduler_StartupCatchUp_LateNight_StillAnnounces(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", jstYesterday(announceAt2330))
	callback := &countingCallback{}
	now := &nowScript{instants: []time.Time{announceAt2330}}
	sleep := &sleepScript{}

	RunAnnounceScheduler(context.Background(), st, callback.call, now.next, sleep.sleep)

	if len(callback.jobs) != 1 {
		t.Fatalf("callback was called %d times at 23:30 JST, want exactly 1 (no upper bound)", len(callback.jobs))
	}
	if got := readGuild(t, path, "guild1").LastAnnounced; got != announceDay {
		t.Fatalf("LastAnnounced = %q, want %q", got, announceDay)
	}
}

// TestRunAnnounceScheduler_StartupCatchUp_ExactlyNineAM_Announces pins that
// the lower bound is "at or after 09:00", not "strictly after".
func TestRunAnnounceScheduler_StartupCatchUp_ExactlyNineAM_Announces(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", jstYesterday(announceAt0900))
	callback := &countingCallback{}
	now := &nowScript{instants: []time.Time{announceAt0900}}
	sleep := &sleepScript{}

	RunAnnounceScheduler(context.Background(), st, callback.call, now.next, sleep.sleep)

	if len(callback.jobs) != 1 {
		t.Fatalf("callback was called %d times at exactly 09:00:00.000 JST, want 1", len(callback.jobs))
	}
	if got := readGuild(t, path, "guild1").LastAnnounced; got != announceDay {
		t.Fatalf("LastAnnounced = %q, want %q", got, announceDay)
	}
	// The boot-at-exactly-09:00 case must then sleep a FULL day, not re-fire.
	if len(sleep.untils) != 1 || !sleep.untils[0].Equal(announceNextDay900) {
		t.Fatalf("sleep untils = %v, want exactly [%s]", sleep.untils, announceNextDay900)
	}
}

// TestRunAnnounceScheduler_StartupCatchUp_AlreadyAnnouncedToday_Skips is the
// first of the three independent no-double-fire guarantees.
func TestRunAnnounceScheduler_StartupCatchUp_AlreadyAnnouncedToday_Skips(t *testing.T) {
	st, _ := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", announceDay)
	callback := &countingCallback{}
	now := &nowScript{instants: []time.Time{announceAt1000}}
	sleep := &sleepScript{}

	RunAnnounceScheduler(context.Background(), st, callback.call, now.next, sleep.sleep)

	if len(callback.jobs) != 0 {
		t.Fatalf("callback was called %d times for an already-announced guild, want 0: %+v", len(callback.jobs), callback.jobs)
	}
}

// TestRunAnnounceScheduler_StartupCatchUp_NoAnnounceChannel_GeneratesRateWithoutAnnouncing
// verifies that an unconfigured guild still gets its rate but no post.
//
// PINNING THE CLOCK IS MANDATORY HERE. With the real clock this test would
// pass between 00:00 and 08:59 JST merely because the window was closed —
// it would never have exercised "skipped because no channel is configured",
// i.e. a test that cannot fail for the reason it was written for. The
// explicit announceWindowOpen assertion below makes that non-vacuousness
// mechanically checked rather than assumed.
func TestRunAnnounceScheduler_StartupCatchUp_NoAnnounceChannel_GeneratesRateWithoutAnnouncing(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "", "")
	callback := &countingCallback{}
	now := &nowScript{instants: []time.Time{announceAt1000}}
	sleep := &sleepScript{}

	if !announceWindowOpen(announceAt1000) {
		t.Fatalf("test precondition broken: the window must be OPEN at %s, otherwise this test proves nothing", announceAt1000)
	}

	RunAnnounceScheduler(context.Background(), st, callback.call, now.next, sleep.sleep)

	if len(callback.jobs) != 0 {
		t.Fatalf("callback was called %d times for a guild with no announce channel, want 0: %+v", len(callback.jobs), callback.jobs)
	}
	assertTodayRateExists(t, path, "guild1", announceAt1000)
}

// TestRunAnnounceScheduler_BeforeNineAM_SleepsUntilSameDayNineAM guarantees
// that a day whose post was deferred at startup is still picked up THAT SAME
// DAY. It is also the regression test for reading now() exactly once per
// iteration: computing nextRunAt from a second, later reading could roll past
// 09:00 and sleep until tomorrow, silently losing the day.
func TestRunAnnounceScheduler_BeforeNineAM_SleepsUntilSameDayNineAM(t *testing.T) {
	st, _ := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	callback := &countingCallback{}
	now := &nowScript{instants: []time.Time{announceAt0300}}
	sleep := &sleepScript{}

	RunAnnounceScheduler(context.Background(), st, callback.call, now.next, sleep.sleep)

	if len(sleep.untils) != 1 {
		t.Fatalf("sleep was called %d times, want exactly 1: %v", len(sleep.untils), sleep.untils)
	}
	if !sleep.untils[0].Equal(announceAt0900) {
		t.Fatalf("slept until %s, want TODAY's 09:00 JST (%s)", sleep.untils[0].In(jst), announceAt0900)
	}
}

// TestRunAnnounceScheduler_WakesAtNineAM_AnnouncesOnSecondPass is the
// liveness test for the scheduled (non-catch-up) announcement, and it is
// mandatory: every other test here stops the loop after the startup pass, so
// an implementation that returned instead of looping would still pass them
// all while every morning's announcement silently disappeared.
//
// It boots at 08:59:59.9 (window closed), asserts the loop sleeps to TODAY's
// 09:00, then wakes at 09:00:00.0 and posts on the second pass.
func TestRunAnnounceScheduler_WakesAtNineAM_AnnouncesOnSecondPass(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", jstYesterday(announceAt0900))
	callback := &countingCallback{}
	now := &nowScript{instants: []time.Time{announceAt0859599, announceAt0900}}
	callsAtFirstSleep := -1
	sleep := &sleepScript{results: []bool{true, false}}
	sleep.hook = func(call int) {
		if call == 0 {
			callsAtFirstSleep = len(callback.jobs)
		}
	}

	RunAnnounceScheduler(context.Background(), st, callback.call, now.next, sleep.sleep)

	if now.overrun {
		t.Fatalf("now() was called %d times, want exactly 2 (one per loop iteration)", now.calls)
	}
	// (1) the first pass, 100ms before the bound, must not post.
	if callsAtFirstSleep != 0 {
		t.Fatalf("callback was called %d times during the 08:59:59.9 pass, want 0", callsAtFirstSleep)
	}
	// (2) it must sleep to TODAY's 09:00, not tomorrow's.
	if len(sleep.untils) == 0 || !sleep.untils[0].Equal(announceAt0900) {
		t.Fatalf("first sleep until = %v, want TODAY's 09:00 JST (%s)", sleep.untils, announceAt0900)
	}
	// (3) the second pass, at exactly 09:00:00.0, posts exactly once.
	if len(callback.jobs) != 1 {
		t.Fatalf("callback was called %d times in total, want exactly 1 (on the second pass): %+v", len(callback.jobs), callback.jobs)
	}
	// (4) and the post is recorded.
	if got := readGuild(t, path, "guild1").LastAnnounced; got != announceDay {
		t.Fatalf("LastAnnounced = %q, want %q", got, announceDay)
	}
}

// TestRunAnnounceScheduler_CancelDuringCallback_ReturnsAfterCallbackCompletes
// (SF-3) pins that cancelling the context does not abandon an in-flight send:
// the caller can join the scheduler goroutine and know no callback is still
// running.
//
// The clock is pinned to 10:00 JST because a closed window means no callback
// at all, and this test would deadlock-then-timeout rather than verify
// anything (with the real clock, that is every run between 00:00 and 08:59
// JST).
func TestRunAnnounceScheduler_CancelDuringCallback_ReturnsAfterCallbackCompletes(t *testing.T) {
	// callbackWaitLimit only ever elapses when the implementation is broken;
	// on the passing path both channels are already closed when read, so no
	// real time is spent here.
	const callbackWaitLimit = 30 * time.Second

	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	entered := make(chan struct{})
	release := make(chan struct{})
	callback := func(job AnnouncementJob) error {
		close(entered)
		<-release
		return nil
	}
	now := &nowScript{instants: []time.Time{announceAt1000}}
	sleep := &sleepScript{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		RunAnnounceScheduler(ctx, st, callback, now.next, sleep.sleep)
	}()

	select {
	case <-entered:
	case <-time.After(callbackWaitLimit):
		t.Fatal("callback was never invoked — the scheduler never reached the send")
	}

	cancel()

	select {
	case <-done:
		t.Fatal("RunAnnounceScheduler returned while its callback was still running — the caller cannot join it safely")
	default:
	}

	close(release)

	select {
	case <-done:
	case <-time.After(callbackWaitLimit):
		t.Fatal("RunAnnounceScheduler did not return after the callback completed")
	}

	if got := readGuild(t, path, "guild1").LastAnnounced; got != announceDay {
		t.Fatalf("LastAnnounced = %q, want %q — the send completed successfully before the cancel took effect", got, announceDay)
	}
}

// 掲示は「育つプール」を見せる場所なので、job はロールオーバーが種を入れた
// 後のプールを運ぶ(設計書 C-3a §2)。一度も回していないギルドで 0 を返すと
// 実残高(種 1,000)と食い違う。
func TestCollectDailyAnnouncements_CarriesSeededJackpotPool(t *testing.T) {
	st, _ := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "a guild's first day is the fixed 基準100, not a draw"}
	seedAnnounceGuild(t, st, "guild1", "chan1", "")

	jobs, err := st.collectDailyAnnouncements(announceAt1000)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	if jobs[0].JackpotPool != JackpotSeed {
		t.Fatalf("job.JackpotPool = %d, want the seed %d", jobs[0].JackpotPool, JackpotSeed)
	}
}

func TestCollectDailyAnnouncements_CarriesGrownJackpotPool(t *testing.T) {
	st, _ := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "a guild's first day is the fixed 基準100, not a draw"}
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	if err := st.Update(func(d *Data) error {
		ensureGuildLocked(d, "guild1").Jackpot = 45678
		return nil
	}); err != nil {
		t.Fatalf("growing the pool: %v", err)
	}

	jobs, err := st.collectDailyAnnouncements(announceAt1000)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	if jobs[0].JackpotPool != 45678 {
		t.Fatalf("job.JackpotPool = %d, want 45678 (the pool must not be reseeded or reset)", jobs[0].JackpotPool)
	}
}

// TestCollectDailyAnnouncements_CarriesTheMorningLotteryDraw pins the 9am
// embed's lottery half (設計書 C-3a §3): the sweep itself performs the draw
// — the rollover is inside the same ensureTodayRateLocked it already calls —
// and the job carries that result plus the freshly reopened pot, not the one
// that was just settled.
func TestCollectDailyAnnouncements_CarriesTheMorningLotteryDraw(t *testing.T) {
	st, _ := newTempStore(t)
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0}}
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	yesterday := announceAt1000.AddDate(0, 0, -1)
	if _, err := st.BuyLotteryTickets("guild1", "u1", 2, yesterday); err != nil {
		t.Fatalf("BuyLotteryTickets: %v", err)
	}

	jobs, err := st.collectDailyAnnouncements(announceAt1000)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}

	job := jobs[0]
	want := LotteryDraw{Date: announceDay, WinnerID: "u1", Prize: 90, TicketsSold: 2, Buyers: 1}
	if len(job.LotteryDraws) != 1 || job.LotteryDraws[0] != want {
		t.Fatalf("job.LotteryDraws = %+v, want exactly [%+v] — the sweep must settle and report the morning's draw", job.LotteryDraws, want)
	}
	if job.LotteryPrize != 0 || job.LotteryTickets != 0 {
		t.Fatalf("job.LotteryPrize/LotteryTickets = %d/%d, want 0/0 — these describe the pot that is now OPEN, which the draw just emptied",
			job.LotteryPrize, job.LotteryTickets)
	}
	// The house's 10% reached the jackpot pool the same morning.
	if want := JackpotSeed + 10; job.JackpotPool != want {
		t.Fatalf("job.JackpotPool = %d, want %d (seed + the lottery's house cut)", job.JackpotPool, want)
	}
}

// TestCollectDailyAnnouncements_OmitsAnAlreadyAnnouncedDrawAndCarriesTheOpenPot
// is the other half: a LastDraw that a previous morning already posted must
// NOT be repeated every day until someone buys again — while the open pot
// (carryover included) is still reported, because that is what readers can buy
// into today. LastAnnounced is the marker that retires a draw, so a draw dated
// on or before it is spent.
func TestCollectDailyAnnouncements_OmitsAnAlreadyAnnouncedDrawAndCarriesTheOpenPot(t *testing.T) {
	st, _ := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "a guild's first day is the fixed 基準100, and today's draw already ran"}
	seedAnnounceGuild(t, st, "guild1", "chan1", "2026-07-02") // the 7/1 draw was posted on 7/2
	stale := LotteryDraw{Date: "2026-07-01", WinnerID: "u9", Prize: 1234, TicketsSold: 7, Buyers: 2}
	if err := st.Update(func(d *Data) error {
		lottery := &ensureGuildLocked(d, "guild1").Lottery
		lottery.DrawDate = announceDay // today's draw has already happened
		lottery.LastDraw = &stale
		lottery.Carryover = 500
		lottery.Sales = 100
		lottery.Tickets = map[string]int{"u1": 2}
		return nil
	}); err != nil {
		t.Fatalf("seeding the lottery state: %v", err)
	}

	jobs, err := st.collectDailyAnnouncements(announceAt1000)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}

	job := jobs[0]
	if len(job.LotteryDraws) != 0 {
		t.Fatalf("job.LotteryDraws = %+v, want empty — the draw from %s was already announced and is no longer queued", job.LotteryDraws, stale.Date)
	}
	if job.LotteryPrize != 590 || job.LotteryTickets != 2 {
		t.Fatalf("job.LotteryPrize/LotteryTickets = %d/%d, want 590/2 (floor(100*90/100) + 500 carried over, 2 tickets)",
			job.LotteryPrize, job.LotteryTickets)
	}
}

// TestCollectDailyAnnouncements_CarriesADrawTheOutageNeverAnnounced is 所見 2
// of the C-3a 区切りレビュー. The bot is down at 09:00 on 7/10, comes back up
// on 7/11, and the startup pass settles 7/10's draw. A job filtered to "the
// draw settled TODAY" would drop that winner on the floor — never announced,
// never celebrated, by any later pass either. The filter is "not announced
// yet", so 7/10's winner rides out on 7/11's posting, and only a confirmed
// send (MarkAnnounced) retires them.
func TestCollectDailyAnnouncements_CarriesADrawTheOutageNeverAnnounced(t *testing.T) {
	st, _ := newTempStore(t)
	// Float64 feeds the daily rate. Intn is scripted EMPTY on purpose: the
	// pot is empty on both mornings, so any actual draw here is a bug in the
	// setup and must fail loudly rather than quietly overwrite LastDraw.
	st.rng = &lotteryRand{t: t, float: 0.5}
	seedAnnounceGuild(t, st, "guild1", "chan1", "2026-07-09") // last posting: 7/9
	missed := LotteryDraw{Date: announceDay, WinnerID: "u9", Prize: 1234, TicketsSold: 7, Buyers: 2}
	if err := st.Update(func(d *Data) error {
		lottery := &ensureGuildLocked(d, "guild1").Lottery
		lottery.DrawDate = announceDay // 7/10's draw ran; its announcement never did
		lottery.LastDraw = &missed
		lottery.Unannounced = []LotteryDraw{missed}
		return nil
	}); err != nil {
		t.Fatalf("seeding the lottery state: %v", err)
	}

	jobs, err := st.collectDailyAnnouncements(announceNextDay900)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	if len(jobs[0].LotteryDraws) != 1 || jobs[0].LotteryDraws[0] != missed {
		t.Fatalf("job.LotteryDraws = %+v, want exactly [%+v] — the draw the outage swallowed must still be announced",
			jobs[0].LotteryDraws, missed)
	}

	// The send succeeds, so 7/11 becomes the last announced day. The morning
	// after must NOT report the same winner a second time.
	if err := st.MarkAnnounced("guild1", jstDate(announceNextDay900)); err != nil {
		t.Fatalf("MarkAnnounced: %v", err)
	}
	dayAfter := time.Date(2026, 7, 12, 9, 0, 0, 0, jst)
	jobs, err = st.collectDailyAnnouncements(dayAfter)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs on the following morning, want 1: %+v", len(jobs), jobs)
	}
	if len(jobs[0].LotteryDraws) != 0 {
		t.Fatalf("job.LotteryDraws = %+v, want empty — an announced draw must not be announced twice", jobs[0].LotteryDraws)
	}
}

// TestCollectDailyAnnouncements_QueuesADrawThatSettlesOnTopOfAnUnpostedOne is
// the regression for 反復 2 の指摘: a single LastDraw slot let the NEXT
// morning's draw overwrite one that had never been posted, and that winner was
// then lost for good. The sequence is the evaluator's: 7/10's draw settles
// without an announcement, somebody buys into the new pot, and 7/11's sweep
// settles a SECOND draw. Both must ride out on that morning's posting.
func TestCollectDailyAnnouncements_QueuesADrawThatSettlesOnTopOfAnUnpostedOne(t *testing.T) {
	st, _ := newTempStore(t)
	// Two draws, one buyer each: PickLotteryWinner walks a one-entry
	// cumulative sum, so roll 0 names that buyer both times.
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0, 0}}
	seedAnnounceGuild(t, st, "guild1", "chan1", "2026-07-09")

	// u1 buys on 7/9, so the 7/10 09:00 draw is theirs.
	if _, err := st.BuyLotteryTickets("guild1", "u1", 2, announceAt1000.AddDate(0, 0, -1)); err != nil {
		t.Fatalf("BuyLotteryTickets(u1): %v", err)
	}
	// u2 buys at 7/10 10:00. The rollover inside the purchase settles 7/10's
	// draw (u1 wins) — and nothing announces it, exactly as an outage at
	// 09:00 leaves things. u2's tickets join the 7/11 pot.
	if _, err := st.BuyLotteryTickets("guild1", "u2", 2, announceAt1000); err != nil {
		t.Fatalf("BuyLotteryTickets(u2): %v", err)
	}

	jobs, err := st.collectDailyAnnouncements(announceNextDay900)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}

	want := []LotteryDraw{
		{Date: announceDay, WinnerID: "u1", Prize: 90, TicketsSold: 2, Buyers: 1},
		{Date: jstDate(announceNextDay900), WinnerID: "u2", Prize: 90, TicketsSold: 2, Buyers: 1},
	}
	got := jobs[0].LotteryDraws
	if len(got) != len(want) {
		t.Fatalf("job.LotteryDraws = %+v, want %+v — the unposted 7/10 winner must not be overwritten by 7/11's draw", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("job.LotteryDraws[%d] = %+v, want %+v (whole queue: %+v)", i, got[i], want[i], got)
		}
	}
}

// TestCollectDailyAnnouncements_KeepsTheQueueUntilASendSucceeds pins the
// retry: the queue is emptied by MarkAnnounced alone, so a posting that failed
// (the scheduler skips MarkAnnounced then) leaves every draw in place for the
// next pass to carry.
func TestCollectDailyAnnouncements_KeepsTheQueueUntilASendSucceeds(t *testing.T) {
	st, _ := newTempStore(t)
	st.rng = &lotteryRand{t: t, float: 0.5}
	seedAnnounceGuild(t, st, "guild1", "chan1", "2026-07-09")
	missed := LotteryDraw{Date: announceDay, WinnerID: "u9", Prize: 1234, TicketsSold: 7, Buyers: 2}
	if err := st.Update(func(d *Data) error {
		lottery := &ensureGuildLocked(d, "guild1").Lottery
		lottery.DrawDate = announceDay
		lottery.LastDraw = &missed
		lottery.Unannounced = []LotteryDraw{missed}
		return nil
	}); err != nil {
		t.Fatalf("seeding the lottery state: %v", err)
	}

	// Pass 1: the job carries the draw, but the send fails — no MarkAnnounced.
	jobs, err := st.collectDailyAnnouncements(announceNextDay900)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 || len(jobs[0].LotteryDraws) != 1 {
		t.Fatalf("first pass: %+v, want one job carrying one draw", jobs)
	}

	// Pass 2, the following morning: the same draw is still owed.
	jobs, err = st.collectDailyAnnouncements(time.Date(2026, 7, 12, 9, 0, 0, 0, jst))
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs on the following morning, want 1: %+v", len(jobs), jobs)
	}
	if len(jobs[0].LotteryDraws) != 1 || jobs[0].LotteryDraws[0] != missed {
		t.Fatalf("job.LotteryDraws = %+v, want [%+v] — a failed send must not retire a draw", jobs[0].LotteryDraws, missed)
	}
}

// TestMarkAnnounced_RetiresThePostedDrawsAndKeepsLaterOnes covers both halves
// of the prune. Draws dated on or before the posted date were in the message
// that just went out; a draw dated AFTER it settled while that message was in
// flight and has never been shown to anybody.
func TestMarkAnnounced_RetiresThePostedDrawsAndKeepsLaterOnes(t *testing.T) {
	st, path := newTempStore(t)
	queued := []LotteryDraw{
		{Date: "2026-07-10", WinnerID: "u1", Prize: 90, TicketsSold: 2, Buyers: 1},
		{Date: "2026-07-11", WinnerID: "u2", Prize: 180, TicketsSold: 4, Buyers: 2},
		{Date: "2026-07-12", WinnerID: "u3", Prize: 270, TicketsSold: 6, Buyers: 3},
	}
	if err := st.Update(func(d *Data) error {
		ensureGuildLocked(d, "guild1").Lottery.Unannounced = append([]LotteryDraw(nil), queued...)
		return nil
	}); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}

	if err := st.MarkAnnounced("guild1", "2026-07-11"); err != nil {
		t.Fatalf("MarkAnnounced: %v", err)
	}

	economy := readGuild(t, path, "guild1")
	got := economy.Lottery.Unannounced
	if len(got) != 1 || got[0] != queued[2] {
		t.Fatalf("Unannounced = %+v, want [%+v] — only the draws that posting covered are retired", got, queued[2])
	}
	if economy.LastAnnounced != "2026-07-11" {
		t.Fatalf("LastAnnounced = %q, want 2026-07-11", economy.LastAnnounced)
	}
}

// The migration tests below all work from the SHAPE a pre-C3-12 build left on
// disk: an unposted winner in "last_draw" and no "unannounced" key at all.
// Only this branch has ever written that file (the bot has never run in
// production), but an operator can still carry one over from an older binary,
// and collectDailyAnnouncements reads the queue and nothing else — so without
// the migration that winner is posted by nobody, ever.
var migrationDay = time.Date(2026, 9, 14, 10, 0, 0, 0, jst) // after 09:00: the 9/14 draw has settled

// oldFormatStore writes `raw` — a casino.json as an older build wrote it —
// and returns a Store over it. Written as literal JSON on purpose:
// marshalling today's structs would emit today's keys, which is the one thing
// the file under test does not have.
func oldFormatStore(t *testing.T, raw string) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "casino.json")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("writing the pre-queue casino.json: %v", err)
	}
	return New(path), path
}

// TestCollectDailyAnnouncements_MigratesAPreQueueFilesUnpostedWinner is the
// C3-18 repro: 9/13 was the last posting, 9/14's draw named a winner, and the
// file predates the queue. The 9/14 posting must still carry that winner —
// and the migration must not queue them a second time on the next pass.
func TestCollectDailyAnnouncements_MigratesAPreQueueFilesUnpostedWinner(t *testing.T) {
	st, _ := oldFormatStore(t, `{"guild1":{"announce_channel_id":"chan1","last_announced":"2026-09-13",`+
		`"lottery":{"draw_date":"2026-09-14","sales":0,"carryover":0,`+
		`"last_draw":{"date":"2026-09-14","winner_id":"u9","prize":1234,"tickets_sold":7,"buyers":2}}}}`)
	// Float64 feeds the daily rate. Intn is scripted EMPTY: 9/14's draw is
	// already stamped, so any actual draw here is a bug in the setup.
	st.rng = &lotteryRand{t: t, float: 0.5}
	want := LotteryDraw{Date: "2026-09-14", WinnerID: "u9", Prize: 1234, TicketsSold: 7, Buyers: 2}

	jobs, err := st.collectDailyAnnouncements(migrationDay)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	if len(jobs[0].LotteryDraws) != 1 || jobs[0].LotteryDraws[0] != want {
		t.Fatalf("job.LotteryDraws = %+v, want exactly [%+v] — a winner the old format left in last_draw must still be posted",
			jobs[0].LotteryDraws, want)
	}

	// Nothing was marked (the send has not happened), so the state the second
	// pass reads is the one the first pass left. It must find the winner
	// already queued rather than derive them from last_draw a second time.
	jobs, err = st.collectDailyAnnouncements(migrationDay)
	if err != nil {
		t.Fatalf("second collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs on the second pass, want 1: %+v", len(jobs), jobs)
	}
	if len(jobs[0].LotteryDraws) != 1 || jobs[0].LotteryDraws[0] != want {
		t.Fatalf("job.LotteryDraws = %+v on the second pass, want exactly [%+v] — the migration must not queue the same draw twice",
			jobs[0].LotteryDraws, want)
	}
}

// TestCollectDailyAnnouncements_DoesNotMigrateAnAlreadyPostedResult pins the
// boundary of the "not posted yet" test. last_draw outlives its announcement
// on purpose (/lottery status keeps showing the last real result), so a
// migration that ignored LastAnnounced would repost that winner every single
// morning. Date == LastAnnounced is the exact edge: the 9/14 draw was posted
// on 9/14.
func TestCollectDailyAnnouncements_DoesNotMigrateAnAlreadyPostedResult(t *testing.T) {
	st, path := oldFormatStore(t, `{"guild1":{"announce_channel_id":"chan1","last_announced":"2026-09-14",`+
		`"lottery":{"draw_date":"2026-09-14","sales":0,"carryover":0,`+
		`"last_draw":{"date":"2026-09-14","winner_id":"u9","prize":1234,"tickets_sold":7,"buyers":2}}}}`)
	// The pot is empty, so 9/15's draw names nobody and takes no Intn roll.
	st.rng = &lotteryRand{t: t, float: 0.5}

	jobs, err := st.collectDailyAnnouncements(time.Date(2026, 9, 15, 10, 0, 0, 0, jst))
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	if len(jobs[0].LotteryDraws) != 0 {
		t.Fatalf("job.LotteryDraws = %+v, want empty — the 9/14 draw was posted on 9/14", jobs[0].LotteryDraws)
	}
	if queue := readGuild(t, path, "guild1").Lottery.Unannounced; len(queue) != 0 {
		t.Fatalf("persisted Unannounced = %+v, want empty — a posted draw must not be put back in the queue", queue)
	}
}

// TestCollectDailyAnnouncements_DoesNotMigrateADrawNobodyWon covers the other
// guard. drawLotteryLocked never writes a winner-less last_draw (a quiet day
// leaves the previous result standing), so this state can only come from a
// hand edit or a partial write — and a draw with no winner has no prize to
// report and nobody to celebrate.
func TestCollectDailyAnnouncements_DoesNotMigrateADrawNobodyWon(t *testing.T) {
	st, path := oldFormatStore(t, `{"guild1":{"announce_channel_id":"chan1","last_announced":"2026-09-13",`+
		`"lottery":{"draw_date":"2026-09-14","sales":0,"carryover":0,`+
		`"last_draw":{"date":"2026-09-14","winner_id":"","prize":0,"tickets_sold":0,"buyers":0}}}}`)
	st.rng = &lotteryRand{t: t, float: 0.5}

	jobs, err := st.collectDailyAnnouncements(migrationDay)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	if len(jobs[0].LotteryDraws) != 0 {
		t.Fatalf("job.LotteryDraws = %+v, want empty — a draw that named nobody is not a result", jobs[0].LotteryDraws)
	}
	if queue := readGuild(t, path, "guild1").Lottery.Unannounced; len(queue) != 0 {
		t.Fatalf("persisted Unannounced = %+v, want empty — a winner-less draw must not enter the queue", queue)
	}
}

// TestCollectDailyAnnouncements_LeavesAPostQueueFileAlone is the other side of
// the guard: once a file HAS a queue, the queue is the authority and last_draw
// contributes nothing. The state below is what a confirmed 9/14 posting leaves
// when a later draw is already waiting — retiring the older entry empties
// nothing, and deriving a second entry from last_draw would post 9/15 twice.
func TestCollectDailyAnnouncements_LeavesAPostQueueFileAlone(t *testing.T) {
	st, _ := newTempStore(t)
	st.rng = &lotteryRand{t: t, float: 0.5}
	seedAnnounceGuild(t, st, "guild1", "chan1", "2026-09-13")
	queued := LotteryDraw{Date: "2026-09-14", WinnerID: "u9", Prize: 1234, TicketsSold: 7, Buyers: 2}
	newer := LotteryDraw{Date: "2026-09-15", WinnerID: "u8", Prize: 90, TicketsSold: 1, Buyers: 1}
	if err := st.Update(func(d *Data) error {
		lottery := &ensureGuildLocked(d, "guild1").Lottery
		lottery.DrawDate = "2026-09-15" // 9/15's draw has run
		lottery.LastDraw = &newer
		lottery.Unannounced = []LotteryDraw{queued}
		return nil
	}); err != nil {
		t.Fatalf("seeding the lottery state: %v", err)
	}

	jobs, err := st.collectDailyAnnouncements(time.Date(2026, 9, 15, 10, 0, 0, 0, jst))
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	if got := jobs[0].LotteryDraws; len(got) != 1 || got[0] != queued {
		t.Fatalf("job.LotteryDraws = %+v, want exactly [%+v] — a file that already has a queue must be read from the queue alone",
			got, queued)
	}
}

// TestCollectDailyAnnouncements_MigratesBeforeTodaysDrawOverwritesLastDraw is
// why the migration sits in normalizeLotteryLocked rather than at the
// announcement: the rollover overwrites last_draw with TODAY's winner, and the
// normalisation point is the one place that runs BEFORE it. The morning after
// the upgrade therefore posts both winners, oldest first.
func TestCollectDailyAnnouncements_MigratesBeforeTodaysDrawOverwritesLastDraw(t *testing.T) {
	st, _ := oldFormatStore(t, `{"guild1":{"announce_channel_id":"chan1","last_announced":"2026-09-12",`+
		`"lottery":{"draw_date":"2026-09-13","sales":200,"carryover":0,"tickets":{"u1":2},`+
		`"last_draw":{"date":"2026-09-13","winner_id":"u9","prize":1234,"tickets_sold":7,"buyers":2}}}}`)
	// One scripted roll: u1 holds every ticket, so 9/14's draw names them.
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0}}
	migrated := LotteryDraw{Date: "2026-09-13", WinnerID: "u9", Prize: 1234, TicketsSold: 7, Buyers: 2}

	jobs, err := st.collectDailyAnnouncements(migrationDay)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	got := jobs[0].LotteryDraws
	if len(got) != 2 {
		t.Fatalf("job.LotteryDraws = %+v, want 2 draws — 9/13's migrated winner and 9/14's fresh one", got)
	}
	if got[0] != migrated {
		t.Fatalf("job.LotteryDraws[0] = %+v, want %+v — the older result must be posted first and must survive today's draw",
			got[0], migrated)
	}
	if got[1].Date != "2026-09-14" || got[1].WinnerID != "u1" {
		t.Fatalf("job.LotteryDraws[1] = %+v, want the 2026-09-14 draw won by u1", got[1])
	}
}

// TestMigrateUnannounced_KeepsTheWinnerOfAGuildWithNoChannelYet is the C3-19
// [P1] repro. A guild with no AnnounceChannelID is exactly the guild that
// needs the migration most: nothing posts for it, so the only record of
// 9/13's winner is last_draw — and 9/14's draw overwrites last_draw within
// hours. If the migration waits for a channel, the winner is gone before the
// channel can be configured. Note the tickets: they are what makes 9/14
// draw at all, which is what made the old gate lossy rather than merely late.
func TestMigrateUnannounced_KeepsTheWinnerOfAGuildWithNoChannelYet(t *testing.T) {
	st, path := oldFormatStore(t, `{"guild1":{"announce_channel_id":"","last_announced":"2026-09-12",`+
		`"lottery":{"draw_date":"2026-09-13","sales":100,"carryover":0,"tickets":{"u1":2},`+
		`"last_draw":{"date":"2026-09-13","winner_id":"u9","prize":1234,"tickets_sold":7,"buyers":2}}}}`)
	// One scripted roll: u1 holds every ticket, so 9/14's draw names them and
	// overwrites last_draw. That overwrite is the whole point of the repro.
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0}}
	want := LotteryDraw{Date: "2026-09-13", WinnerID: "u9", Prize: 1234, TicketsSold: 7, Buyers: 2}

	if _, err := st.EnsureTodayRate("guild1", migrationDay); err != nil {
		t.Fatalf("EnsureTodayRate without an announcement channel: %v", err)
	}

	lottery := readGuild(t, path, "guild1").Lottery
	if lottery.LastDraw == nil || lottery.LastDraw.Date != "2026-09-14" {
		t.Fatalf("LastDraw = %+v, want 9/14's draw — the setup must actually overwrite it", lottery.LastDraw)
	}
	if len(lottery.Unannounced) == 0 {
		t.Fatalf("Unannounced is empty — 9/13's winner %+v was lost to 9/14's draw because the guild had no channel yet", want)
	}
	if lottery.Unannounced[0] != want {
		t.Fatalf("Unannounced[0] = %+v, want %+v — the migrated winner must survive today's draw", lottery.Unannounced[0], want)
	}

	// Configuring the channel afterwards posts it: the point of keeping it.
	seedAnnounceGuild(t, st, "guild1", "chan1", "2026-09-13")
	jobs, err := st.collectDailyAnnouncements(migrationDay)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements after the channel was configured: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	if got := jobs[0].LotteryDraws; len(got) != 2 || got[0] != want {
		t.Fatalf("job.LotteryDraws = %+v, want 9/13's migrated winner first, then 9/14's", got)
	}
}

// TestMarkAnnounced_DoesNotLetAClockRollbackRepostADraw is the C3-19 [P2]
// repro. 9/14 is posted and marked. The clock then steps back to 9/13, which
// posts for that earlier day — and if that posting pulled LastAnnounced back
// with it, the 9/14 pass that follows would see 9/14 as unposted, re-queue
// its winner off LastDraw and celebrate them twice.
func TestMarkAnnounced_DoesNotLetAClockRollbackRepostADraw(t *testing.T) {
	var (
		day13 = time.Date(2026, 9, 13, 10, 0, 0, 0, jst)
		day14 = time.Date(2026, 9, 14, 10, 0, 0, 0, jst)
		drawn = LotteryDraw{Date: "2026-09-14", WinnerID: "u9", Prize: 1234, TicketsSold: 7, Buyers: 2}
	)
	// New format: "unannounced" is present and empty because MarkAnnounced
	// already retired 9/14's entry. Rates for both days exist, so neither
	// pass rolls a draw — 9/14's pot is stamped and sold nothing.
	st, _ := oldFormatStore(t, `{"guild1":{"announce_channel_id":"chan1","last_announced":"2026-09-14",`+
		`"rates":[{"date":"2026-09-13","rate":100},{"date":"2026-09-14","rate":100}],`+
		`"lottery":{"draw_date":"2026-09-14","sales":0,"carryover":0,"unannounced":[],`+
		`"last_draw":{"date":"2026-09-14","winner_id":"u9","prize":1234,"tickets_sold":7,"buyers":2}}}}`)
	st.rng = &lotteryRand{t: t, float: 0.5}

	var posted []AnnouncementJob
	callback := func(job AnnouncementJob) error {
		posted = append(posted, job)
		return nil
	}
	runAnnouncePass(context.Background(), st, callback, day13) // the clock steps back
	runAnnouncePass(context.Background(), st, callback, day14) // and forward again

	for _, job := range posted {
		for _, draw := range job.LotteryDraws {
			if draw == drawn {
				t.Fatalf("posted %+v again (jobs: %+v) — MarkAnnounced let a rolled-back clock reopen an announced day", drawn, posted)
			}
		}
	}
}

// TestMarkAnnounced_AdvancesTheMarkOnAForwardClock is the other half of the
// high-water mark: refusing to go backwards must not stop it going forwards.
func TestMarkAnnounced_AdvancesTheMarkOnAForwardClock(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "2026-09-13")

	if err := st.MarkAnnounced("guild1", "2026-09-14"); err != nil {
		t.Fatalf("MarkAnnounced: %v", err)
	}
	if got := readGuild(t, path, "guild1").LastAnnounced; got != "2026-09-14" {
		t.Fatalf("LastAnnounced = %q, want %q — a normal forward posting must still move the mark", got, "2026-09-14")
	}
}

// TestCollectDailyAnnouncements_StaysQuietWhileTheClockIsBehindTheMark is the
// collector's half of the high-water mark. MarkAnnounced only moves the mark
// FORWARD, so a collector that asked "is today != the mark?" would answer yes
// on every pass of a rolled-back day and repost it forever — the mark can
// never come back down to match. Asking "is today AFTER the mark?" keeps the
// two sides in step: the rolled-back day is already covered, so it is silent.
func TestCollectDailyAnnouncements_StaysQuietWhileTheClockIsBehindTheMark(t *testing.T) {
	var (
		day13 = time.Date(2026, 9, 13, 10, 0, 0, 0, jst)
		day15 = time.Date(2026, 9, 15, 10, 0, 0, 0, jst)
	)
	// 9/14 has been announced. Rates for 9/13 and 9/14 exist so the rolled-back
	// pass rolls nothing over; the lottery is idle with nothing pending.
	st, _ := oldFormatStore(t, `{"guild1":{"announce_channel_id":"chan1","last_announced":"2026-09-14",`+
		`"rates":[{"date":"2026-09-13","rate":100},{"date":"2026-09-14","rate":100}],`+
		`"lottery":{"draw_date":"2026-09-14","sales":0,"carryover":0,"unannounced":[]}}}`)
	st.rng = &lotteryRand{t: t, float: 0.5}

	behind, err := st.collectDailyAnnouncements(day13)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements(day13): %v", err)
	}
	if len(behind) != 0 {
		t.Fatalf("collected %d job(s) for a day the mark already covers (%+v) — a rolled-back clock would repost it on every pass", len(behind), behind)
	}

	// The mark must not jam the scheduler once the clock passes it again.
	ahead, err := st.collectDailyAnnouncements(day15)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements(day15): %v", err)
	}
	if len(ahead) != 1 {
		t.Fatalf("collected %d job(s) for a day past the mark, want 1 — the high-water mark must not silence the future", len(ahead))
	}
}

// --- シーズン欄と結果の祝い (設計書 C-3b §4 掲示) ---------------------------

// seedAnnounceSeason pins a guild's season state directly, WITHOUT going
// through the rollover: a test must not seed its precondition with the code
// it is testing (seedAnnounceGuild's rule). A month of "" means the guild has
// never opened a season, which is what a pre-C-3b file reads back as.
func seedAnnounceSeason(t *testing.T, st *Store, guildID, month string, last *SeasonResult, nets map[string]int64) {
	t.Helper()
	err := st.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		economy.SeasonMonth = month
		economy.LastSeason = last
		for userID, net := range nets {
			economy.Users[userID] = &UserAccount{Chips: 1_000, SeasonNet: net}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding the season of %s: %v", guildID, err)
	}
}

// oneJob collects and returns the single job the guild under test is owed,
// failing the test if the sweep produced any other number.
func oneJob(t *testing.T, st *Store, now time.Time) AnnouncementJob {
	t.Helper()
	jobs, err := st.collectDailyAnnouncements(now)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(jobs), jobs)
	}
	return jobs[0]
}

// The 9am posting carries the RUNNING season's podium — the same three
// places the rollover pays — so the channel watches the month being fought
// over, not only the one that ended.
func TestCollectDailyAnnouncements_CarriesTheRunningSeasonPodium(t *testing.T) {
	st, _ := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	seedAnnounceSeason(t, st, "guild1", "2026-07", nil, map[string]int64{
		"leader": 1_200, "second": 800, "third": 300, "fourth": 100, "idle": 0,
	})

	job := oneJob(t, st, announceAt1000)

	if job.SeasonMonth != "2026-07" {
		t.Errorf("job.SeasonMonth = %q, want %q", job.SeasonMonth, "2026-07")
	}
	want := []SeasonRank{{UserID: "leader", Net: 1_200}, {UserID: "second", Net: 800}, {UserID: "third", Net: 300}}
	if len(job.SeasonTop) != len(want) {
		t.Fatalf("job.SeasonTop = %+v, want the top %d", job.SeasonTop, len(want))
	}
	for i, rank := range want {
		if job.SeasonTop[i] != rank {
			t.Errorf("job.SeasonTop[%d] = %+v, want %+v", i, job.SeasonTop[i], rank)
		}
	}
	if len(job.ClosedSeasons) != 0 {
		t.Errorf("job.ClosedSeasons = %+v, want empty — no season has closed", job.ClosedSeasons)
	}
}

// oneClosedSeason returns the single season result the job is owed, failing
// the test if the queue holds any other number.
func oneClosedSeason(t *testing.T, job AnnouncementJob) SeasonResult {
	t.Helper()
	if len(job.ClosedSeasons) != 1 {
		t.Fatalf("job.ClosedSeasons = %+v, want exactly 1 result", job.ClosedSeasons)
	}
	return job.ClosedSeasons[0]
}

// A month nobody has won or lost anything in is a normal morning, not a
// missing value: SeasonRanks excludes 純利 0, so a guild whose accounts exist
// only because /balance opened them carries an EMPTY podium rather than a
// table of people who never placed a bet.
func TestCollectDailyAnnouncements_QuietMonthCarriesNoPodium(t *testing.T) {
	st, _ := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	seedAnnounceSeason(t, st, "guild1", "2026-07", nil, map[string]int64{"idle1": 0, "idle2": 0})

	job := oneJob(t, st, announceAt1000)

	if len(job.SeasonTop) != 0 {
		t.Fatalf("job.SeasonTop = %+v, want empty", job.SeasonTop)
	}
	if job.SeasonMonth != "2026-07" {
		t.Errorf("job.SeasonMonth = %q, want %q even with nobody on the board", job.SeasonMonth, "2026-07")
	}
}

// The sweep itself closes an elapsed month — the rollover is inside the
// ensureTodayRateLocked it already calls — and the job carries that result
// plus the freshly zeroed board, exactly as it does for the lottery draw.
func TestCollectDailyAnnouncements_ClosesTheMonthAndCarriesItsResult(t *testing.T) {
	st, _ := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	seedAnnounceSeason(t, st, "guild1", "2026-06", nil, map[string]int64{
		"leader": 1_200, "second": 800, "third": 300, "fourth": 100,
	})

	job := oneJob(t, st, announceAt1000)

	closed := oneClosedSeason(t, job)
	if closed.Month != "2026-06" {
		t.Errorf("closed.Month = %q, want %q (the month that CLOSED, not today's)", closed.Month, "2026-06")
	}
	if closed.Players != 4 {
		t.Errorf("closed.Players = %d, want 4", closed.Players)
	}
	want := []SeasonRank{
		{UserID: "leader", Net: 1_200, Bonus: SeasonBonus(1)},
		{UserID: "second", Net: 800, Bonus: SeasonBonus(2)},
		{UserID: "third", Net: 300, Bonus: SeasonBonus(3)},
	}
	if len(closed.Ranks) != len(want) {
		t.Fatalf("closed.Ranks = %+v, want %+v", closed.Ranks, want)
	}
	for i, rank := range want {
		if closed.Ranks[i] != rank {
			t.Errorf("Ranks[%d] = %+v, want %+v", i, closed.Ranks[i], rank)
		}
	}
	if job.SeasonMonth != "2026-07" || len(job.SeasonTop) != 0 {
		t.Errorf("running season = %q %+v, want the freshly opened 2026-07 with an empty board", job.SeasonMonth, job.SeasonTop)
	}
}

// 設計書 C-3a の掲示待ち行列と同じ規律: the result is kept until a send is
// confirmed. Only MarkSeasonAnnounced retires it, so a pass whose posting
// failed (i.e. never marked) carries the very same result again.
func TestCollectDailyAnnouncements_KeepsTheClosedSeasonUntilItIsAnnounced(t *testing.T) {
	st, _ := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	seedAnnounceSeason(t, st, "guild1", "2026-06", nil, map[string]int64{"leader": 1_200})

	if first := oneClosedSeason(t, oneJob(t, st, announceAt1000)); first.Month != "2026-06" {
		t.Fatalf("first pass: closed season = %+v, want June", first)
	}
	// The send failed: nothing was marked.
	if retry := oneClosedSeason(t, oneJob(t, st, announceAt1000)); retry.Month != "2026-06" {
		t.Fatalf("after a failed send: closed season = %+v, want the June season again", retry)
	}

	if err := st.MarkSeasonAnnounced("guild1", "2026-06"); err != nil {
		t.Fatalf("MarkSeasonAnnounced returned error: %v", err)
	}
	after := oneJob(t, st, announceAt1000)
	if len(after.ClosedSeasons) != 0 {
		t.Fatalf("after a confirmed send: ClosedSeasons = %+v, want empty — the podium must not be pinged twice", after.ClosedSeasons)
	}
}

// The mark is a high-water mark, LastAnnounced's rule: a clock that steps
// back must not re-open a month that has already been celebrated.
func TestMarkSeasonAnnounced_DoesNotLetAClockRollbackRepostASeason(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	// A podium, not a bare result: an empty one is never queued at all
	// (queueClosedSeasonLocked), so the assertion below would hold for the
	// wrong reason and the mark would not be under test.
	seedAnnounceSeason(t, st, "guild1", "2026-07", &SeasonResult{
		Month: "2026-06", Players: 1, Ranks: []SeasonRank{{UserID: "leader", Net: 1_200, Bonus: SeasonBonus(1)}},
	}, nil)

	if err := st.MarkSeasonAnnounced("guild1", "2026-06"); err != nil {
		t.Fatalf("MarkSeasonAnnounced returned error: %v", err)
	}
	if err := st.MarkSeasonAnnounced("guild1", "2026-05"); err != nil {
		t.Fatalf("MarkSeasonAnnounced (rolled back) returned error: %v", err)
	}
	if got := readGuild(t, path, "guild1").LastSeasonAnnounced; got != "2026-06" {
		t.Fatalf("LastSeasonAnnounced = %q, want %q — the mark only moves forward", got, "2026-06")
	}
	// An empty month is a no-op, not a blanked boundary.
	if err := st.MarkSeasonAnnounced("guild1", ""); err != nil {
		t.Fatalf("MarkSeasonAnnounced (empty) returned error: %v", err)
	}
	if got := readGuild(t, path, "guild1").LastSeasonAnnounced; got != "2026-06" {
		t.Fatalf("LastSeasonAnnounced = %q after an empty mark, want %q", got, "2026-06")
	}
	if job := oneJob(t, st, announceAt1000); len(job.ClosedSeasons) != 0 {
		t.Fatalf("ClosedSeasons = %+v, want empty — 2026-06 is already announced", job.ClosedSeasons)
	}
}

// 掲示チャンネル未設定なら送らない: no channel, no job — and crucially the
// result is NOT marked, so it stays both readable through /season and
// pending for the day somebody configures a channel.
func TestCollectDailyAnnouncements_NoChannelKeepsTheClosedSeasonPending(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceSeason(t, st, "guild1", "2026-06", nil, map[string]int64{"leader": 1_200})

	jobs, err := st.collectDailyAnnouncements(announceAt1000)
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("got %d jobs, want 0 — the guild has no announcement channel", len(jobs))
	}
	economy := readGuild(t, path, "guild1")
	if economy.LastSeason == nil || economy.LastSeason.Month != "2026-06" {
		t.Fatalf("LastSeason = %+v, want the June result kept for /season", economy.LastSeason)
	}
	if economy.LastSeasonAnnounced != "" {
		t.Fatalf("LastSeasonAnnounced = %q, want empty — nothing was posted", economy.LastSeasonAnnounced)
	}

	if queue := economy.UnannouncedSeasons; len(queue) != 1 || queue[0].Month != "2026-06" {
		t.Fatalf("persisted UnannouncedSeasons = %+v, want the June result waiting for a channel", queue)
	}

	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	if closed := oneClosedSeason(t, oneJob(t, st, announceAt1000)); closed.Month != "2026-06" {
		t.Fatalf("after configuring a channel: closed season = %+v, want the June result", closed)
	}
}

// C3B-08 の回帰: the month boundary must not eat a result the channel never
// received. 7/31 closes nothing, but 8/1 closes July right on top of June —
// and with LastSeason as the only slot, June's podium was overwritten and
// never announced by any later pass.
func TestCollectDailyAnnouncements_AMonthRolloverDoesNotDropAnUnannouncedSeason(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	seedAnnounceSeason(t, st, "guild1", "2026-06", nil, map[string]int64{"leader": 1_200, "second": 800})

	// 7/31: June closes here and the result is owed. The send failed, so
	// nothing is marked.
	july31 := time.Date(2026, 7, 31, 10, 0, 0, 0, jst)
	if closed := oneClosedSeason(t, oneJob(t, st, july31)); closed.Month != "2026-06" {
		t.Fatalf("7/31: closed season = %+v, want June", closed)
	}

	// 8/1: the pass closes July — nobody played it, so it has no podium of
	// its own — and June must still be owed.
	august1 := time.Date(2026, 8, 1, 10, 0, 0, 0, jst)
	closed := oneClosedSeason(t, oneJob(t, st, august1))
	if closed.Month != "2026-06" {
		t.Fatalf("8/1: closed season = %+v, want June again — the July rollover overwrote it", closed)
	}
	if len(closed.Ranks) != 2 || closed.Ranks[0].UserID != "leader" {
		t.Fatalf("8/1: June's podium = %+v, want the two players it closed with", closed.Ranks)
	}
	// LastSeason really was overwritten: the queue is what survived it.
	economy := readGuild(t, path, "guild1")
	if economy.LastSeason == nil || economy.LastSeason.Month != "2026-07" {
		t.Fatalf("LastSeason = %+v, want the July result the rollover wrote", economy.LastSeason)
	}

	if err := st.MarkSeasonAnnounced("guild1", "2026-06"); err != nil {
		t.Fatalf("MarkSeasonAnnounced returned error: %v", err)
	}
	if job := oneJob(t, st, august1); len(job.ClosedSeasons) != 0 {
		t.Fatalf("after the confirmed send: ClosedSeasons = %+v, want empty", job.ClosedSeasons)
	}
}

// 掲示チャンネル未設定のまま月をまたいでも同じ: the queue is filled by the
// rollover, which every pass reaches whether or not there is anywhere to
// post — so the day a channel is configured, the old podium is still there.
func TestCollectDailyAnnouncements_NoChannelKeepsASeasonAcrossTheMonthBoundary(t *testing.T) {
	st, _ := newTempStore(t)
	seedAnnounceSeason(t, st, "guild1", "2026-06", nil, map[string]int64{"leader": 1_200})

	for _, at := range []time.Time{
		time.Date(2026, 7, 31, 10, 0, 0, 0, jst),
		time.Date(2026, 8, 1, 10, 0, 0, 0, jst),
	} {
		jobs, err := st.collectDailyAnnouncements(at)
		if err != nil {
			t.Fatalf("collectDailyAnnouncements(%v) returned error: %v", at, err)
		}
		if len(jobs) != 0 {
			t.Fatalf("got %d jobs, want 0 — the guild has no announcement channel", len(jobs))
		}
	}

	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	job := oneJob(t, st, time.Date(2026, 8, 2, 10, 0, 0, 0, jst))
	if closed := oneClosedSeason(t, job); closed.Month != "2026-06" {
		t.Fatalf("after configuring a channel: closed season = %+v, want the June result", closed)
	}
}

// Several months can be owed at once, and posting one of them retires only
// that one: the queue is drained a message at a time, so a failure partway
// through leaves the rest exactly where they were.
func TestMarkSeasonAnnounced_RetiresOnlyTheMonthThatWasPosted(t *testing.T) {
	st, path := newTempStore(t)
	seedAnnounceGuild(t, st, "guild1", "chan1", "")
	seedAnnounceSeason(t, st, "guild1", "2026-06", nil, map[string]int64{"leader": 1_200})
	// June closes on the 7/31 pass, July on the 8/1 one — with a player in
	// each month, so both have a podium to post.
	oneJob(t, st, time.Date(2026, 7, 31, 10, 0, 0, 0, jst))
	seedAnnounceSeason(t, st, "guild1", "2026-07", nil, map[string]int64{"second": 500})
	august1 := time.Date(2026, 8, 1, 10, 0, 0, 0, jst)
	if job := oneJob(t, st, august1); len(job.ClosedSeasons) != 2 ||
		job.ClosedSeasons[0].Month != "2026-06" || job.ClosedSeasons[1].Month != "2026-07" {
		t.Fatalf("ClosedSeasons = %+v, want June then July, oldest first", job.ClosedSeasons)
	}

	// June got through, July did not.
	if err := st.MarkSeasonAnnounced("guild1", "2026-06"); err != nil {
		t.Fatalf("MarkSeasonAnnounced returned error: %v", err)
	}
	if closed := oneClosedSeason(t, oneJob(t, st, august1)); closed.Month != "2026-07" {
		t.Fatalf("closed season = %+v, want July still owed", closed)
	}
	if got := readGuild(t, path, "guild1").LastSeasonAnnounced; got != "2026-06" {
		t.Fatalf("LastSeasonAnnounced = %q, want %q", got, "2026-06")
	}
}

// TestCollectDailyAnnouncements_CarriesAnOwedSeasonAfterTodayIsMarked is the
// regression for 反復 2 の所見 2: the embed landed at 10:00 and the season
// result that follows it did not, so LastAnnounced reads today while the
// queue still owes a month. The 11:00 pass must still reach this guild —
// with DailyOwed false, so the caller posts the result ALONE.
//
// Before C3B-09 the LastAnnounced gate dropped the whole guild here and the
// owed result could not move again until the next calendar day.
func TestCollectDailyAnnouncements_CarriesAnOwedSeasonAfterTodayIsMarked(t *testing.T) {
	st, _ := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "today's rate was drawn by the 10:00 pass, not by this one"}
	seedAnnounceGuild(t, st, "guild1", "chan1", announceDay) // the embed already went out today
	// A draw queued alongside it: it rode with the embed, so this pass must
	// not carry it again and re-ping the winner.
	posted := LotteryDraw{Date: announceDay, WinnerID: "u9", Prize: 1234, TicketsSold: 7, Buyers: 2}
	err := st.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, "guild1")
		economy.Rates = []DailyRate{{Date: announceDay, Rate: 100}}
		economy.SeasonMonth = "2026-07"
		economy.Lottery.DrawDate = announceDay
		economy.Lottery.Unannounced = []LotteryDraw{posted}
		economy.UnannouncedSeasons = []SeasonResult{{
			Month: "2026-06", Players: 1,
			Ranks: []SeasonRank{{UserID: "u1", Net: 5_000, Bonus: 10_000}},
		}}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding the owed result: %v", err)
	}

	job := oneJob(t, st, time.Date(2026, 7, 10, 11, 0, 0, 0, jst))

	if job.DailyOwed {
		t.Fatalf("job.DailyOwed = true, want false — today's embed already landed and must not be posted twice")
	}
	if closed := oneClosedSeason(t, job); closed.Month != "2026-06" {
		t.Fatalf("closed season = %+v, want the June result still owed", closed)
	}
	if len(job.LotteryDraws) != 0 {
		t.Fatalf("job.LotteryDraws = %+v, want empty — the celebrations rode with the embed", job.LotteryDraws)
	}
}

// The separation cuts one way only: with nothing owed, a guild whose embed
// already landed today is still skipped outright. Otherwise every pass of an
// ordinary afternoon would hand the caller a job with nothing in it.
func TestCollectDailyAnnouncements_AnnouncedTodayWithNothingOwedStaysSkipped(t *testing.T) {
	st, _ := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "today's rate was drawn by the 10:00 pass, not by this one"}
	seedAnnounceGuild(t, st, "guild1", "chan1", announceDay)
	if err := st.Update(func(d *Data) error {
		ensureGuildLocked(d, "guild1").Rates = []DailyRate{{Date: announceDay, Rate: 100}}
		return nil
	}); err != nil {
		t.Fatalf("seeding today's rate: %v", err)
	}

	jobs, err := st.collectDailyAnnouncements(time.Date(2026, 7, 10, 11, 0, 0, 0, jst))
	if err != nil {
		t.Fatalf("collectDailyAnnouncements returned error: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("got %d jobs, want 0 (announced today, nothing owed): %+v", len(jobs), jobs)
	}
}
