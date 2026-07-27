package casino

import (
	"context"
	"errors"
	"fmt"
	"os"
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
