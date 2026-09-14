package commands

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
)

// jstForTest is the zone every date in this file is written in. The season's
// boundaries are JST calendar boundaries (設計書 C-3b §4), so a test written
// in the host's zone would pass or fail depending on where it runs.
var jstForTest = time.FixedZone("JST", 9*3600)

func TestSeasonCommand_Definition(t *testing.T) {
	def := (&SeasonCommand{}).Definition()
	if def.Name != "season" {
		t.Fatalf("unexpected command name: %q", def.Name)
	}
	assertGuildOnly(t, def)
}

// --- 残り日数 (設計書 C-3b §5) ---------------------------------------------

// The count INCLUDES today, so the last day of a month reads 残り1日 rather
// than 0: the season closes at the next midnight, so today is still playable.
// February and the leap year are in the table because a month-length constant
// would get them wrong and nothing else would notice.
func TestSeasonDaysLeft_CountsTodayAndEndsAtTheMonthBoundary(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want int
	}{
		{"月初(31日の月)", time.Date(2026, 7, 1, 0, 0, 0, 0, jstForTest), 31},
		{"月末(31日の月)", time.Date(2026, 7, 31, 23, 59, 59, 0, jstForTest), 1},
		{"月の途中", time.Date(2026, 7, 10, 12, 0, 0, 0, jstForTest), 22},
		{"30日の月の月初", time.Date(2026, 9, 1, 12, 0, 0, 0, jstForTest), 30},
		{"平年の2月末", time.Date(2026, 2, 28, 12, 0, 0, 0, jstForTest), 1},
		{"閏年の2月末", time.Date(2028, 2, 28, 12, 0, 0, 0, jstForTest), 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := casino.SeasonDaysLeft(tc.now); got != tc.want {
				t.Fatalf("SeasonDaysLeft(%s) = %d, want %d", tc.now.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

// The date is the JST one, not the host's. 2026-07-31 15:00 UTC is already
// 2026-08-01 in JST, so the July season it belongs to is the NEXT one — a
// count taken in UTC would report 1 day left of a month that has ended.
func TestSeasonDaysLeft_ReadsTheJSTCalendarDay(t *testing.T) {
	got := casino.SeasonDaysLeft(time.Date(2026, 7, 31, 15, 0, 0, 0, time.UTC))
	if got != 31 {
		t.Fatalf("SeasonDaysLeft = %d, want 31 — 07-31 15:00 UTC is 08-01 JST", got)
	}
}

// --- 順位表の整形 (設計書 C-3b §5) -----------------------------------------

// The top three get /rank's medals and everyone below gets a number, the same
// shape the asset ranking uses. 純利 carries its sign in every row: a table
// of losses must not read like a table of wins.
func TestFormatSeasonMessage_RanksWithMedalsAndSignedNets(t *testing.T) {
	view := casino.SeasonView{
		Month:    "2026-09",
		DaysLeft: 16,
		Players:  4,
		Ranks: []casino.SeasonRank{
			{UserID: "u1", Net: 1_200},
			{UserID: "u2", Net: 500},
			{UserID: "u3", Net: 100},
			{UserID: "u4", Net: -800},
		},
		Self:     casino.SeasonRank{UserID: "u3", Net: 100},
		SelfRank: 3,
	}

	got := formatSeasonMessage(view)

	want := "🏆 **シーズン 2026-09**（今日を含めて残り16日）\n" +
		"🥇 <@u1> — 純利 +1200\n🥈 <@u2> — 純利 +500\n🥉 <@u3> — 純利 +100\n4. <@u4> — 純利 -800\n" +
		"参加者: 4人\nあなた: 3位 — 純利 +100\n前シーズン: まだありません。"
	if got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
}

// 11th place is off the table but not out of the season: the caller's own
// line names the place they actually hold, which is the only way /season can
// tell them how far they are from the board.
func TestFormatSeasonMessage_CallerOutsideTheTableKeepsTheirPlace(t *testing.T) {
	view := casino.SeasonView{
		Month:    "2026-09",
		DaysLeft: 5,
		Players:  11,
		Ranks:    []casino.SeasonRank{{UserID: "u1", Net: 900}},
		Self:     casino.SeasonRank{UserID: "me", Net: -40},
		SelfRank: 11,
	}

	got := formatSeasonMessage(view)

	if !strings.Contains(got, "あなた: 11位 — 純利 -40\n") {
		t.Errorf("the caller's own place is missing:\n%s", got)
	}
	if strings.Contains(got, "<@me>") {
		t.Errorf("an 11th-placed caller must not appear in a TOP10 table:\n%s", got)
	}
}

// 純利 0 is unranked, and the message says why. Without the reason, a player
// who has claimed a daily bonus but never bet reads "圏外" as a bug.
func TestFormatSeasonMessage_CallerWithNoResultIsToldTheyAreUnranked(t *testing.T) {
	view := casino.SeasonView{
		Month:    "2026-09",
		DaysLeft: 5,
		Players:  1,
		Ranks:    []casino.SeasonRank{{UserID: "u1", Net: 900}},
		Self:     casino.SeasonRank{UserID: "me"},
	}

	got := formatSeasonMessage(view)

	if !strings.Contains(got, "あなた: 圏外（純利 0 — 勝敗がつくと順位に載ります）\n") {
		t.Errorf("the unranked caller is not told why they are missing:\n%s", got)
	}
}

// A season nobody has played still has a header, a day count and the
// caller's own line: an empty table is a state of the season, not an error.
func TestFormatSeasonMessage_NobodyHasPlayed(t *testing.T) {
	view := casino.SeasonView{Month: "2026-09", DaysLeft: 30, Self: casino.SeasonRank{UserID: "me"}}

	got := formatSeasonMessage(view)

	want := "🏆 **シーズン 2026-09**（今日を含めて残り30日）\nまだ誰も勝敗を記録していません。\n" +
		"あなた: 圏外（純利 0 — 勝敗がつくと順位に載ります）\n前シーズン: まだありません。"
	if got != want {
		t.Fatalf("unexpected message:\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(got, "参加者:") {
		t.Errorf("an empty season must not claim a participant count:\n%s", got)
	}
}

// --- 前シーズン (設計書 C-3b §4: 直近 1 件だけ) -----------------------------

// The bonus shown is the CREDITED amount carried in the record, not what
// SeasonBonus offers: a winner already at the chip cap took less, and the
// line must not promise chips that never arrived.
func TestFormatLastSeasonLine_NamesThePodiumAndWhatItWasPaid(t *testing.T) {
	last := &casino.SeasonResult{
		Month:   "2026-08",
		Players: 7,
		Ranks: []casino.SeasonRank{
			{UserID: "u1", Net: 4_000, Bonus: 10_000},
			{UserID: "u2", Net: 2_000, Bonus: 5_000},
			{UserID: "u3", Net: -100, Bonus: 0}, // at the chip cap: nothing fitted
		},
	}

	got := formatLastSeasonLine(last)

	want := "前シーズン（2026-08・7人）: 🥇 <@u1>（純利 +4000 / 賞与 10000枚）" +
		" 🥈 <@u2>（純利 +2000 / 賞与 5000枚） 🥉 <@u3>（純利 -100 / 賞与 0枚）"
	if got != want {
		t.Fatalf("unexpected line:\n got: %q\nwant: %q", got, want)
	}
}

func TestFormatLastSeasonLine_NoSeasonHasClosedYet(t *testing.T) {
	if got := formatLastSeasonLine(nil); got != "前シーズン: まだありません。" {
		t.Fatalf("unexpected line for a first season: %q", got)
	}
}

// A month in which nobody played closes with an empty podium — the record
// exists, so the line must name the month rather than claim there was no
// previous season at all.
func TestFormatLastSeasonLine_ClosedWithoutAPodium(t *testing.T) {
	got := formatLastSeasonLine(&casino.SeasonResult{Month: "2026-08"})
	if got != "前シーズン（2026-08）: 受賞者なし" {
		t.Fatalf("unexpected line for an unplayed season: %q", got)
	}
}

// The table /season shows is the one the store builds, end to end: the store
// truncates to SeasonTopLimit and the renderer numbers what it is given, so
// the two halves have to agree on where the board ends.
func TestSeasonCommand_RendersTheStoresOwnView(t *testing.T) {
	store := newTestCasinoStore(t)
	// The REAL clock, not testNow(): a settlement books its 純利 into the
	// month of the store's own clock (ensureSeasonMonthLocked reads it under
	// the lock, and this package cannot inject it), so a pinned `now` here
	// would ask the store to report a season the game never played in.
	now := time.Now()
	if err := store.EnsureCasinoAccess("g1", "u1", now); err != nil {
		t.Fatalf("EnsureCasinoAccess: %v", err)
	}
	// One real result, booked through a game: /season must show 純利 that the
	// economy actually recorded, not a number this test wrote into the file.
	if err := store.OpenGame("g1", "u1", string(casino.GameHighLow), testEscrowSession, 200, now); err != nil {
		t.Fatalf("OpenGame: %v", err)
	}
	if _, err := store.SettleGame("g1", "u1", testEscrowSession, 500); err != nil {
		t.Fatalf("SettleGame: %v", err)
	}

	view, err := store.SeasonStatus("g1", "u1", now)
	if err != nil {
		t.Fatalf("SeasonStatus: %v", err)
	}

	got := formatSeasonMessage(view)

	if !strings.Contains(got, "🥇 <@u1> — 純利 +300\n") {
		t.Errorf("the winner's row is missing or wrong:\n%s", got)
	}
	if !strings.Contains(got, "あなた: 1位 — 純利 +300\n") {
		t.Errorf("the caller's own line is missing or wrong:\n%s", got)
	}
	wantHeader := fmt.Sprintf("🏆 **シーズン %s**（今日を含めて残り%d日）\n",
		now.In(jstForTest).Format("2006-01"), casino.SeasonDaysLeft(now))
	if !strings.Contains(got, wantHeader) {
		t.Errorf("the header does not name the running season and its remaining days:\n got: %s\nwant it to contain: %s", got, wantHeader)
	}
}
