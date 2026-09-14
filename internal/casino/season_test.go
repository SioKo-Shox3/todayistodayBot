package casino

import "testing"

// seasonUsers builds the map SeasonRanks takes from a userID -> 純利 table,
// so a ranking test reads as the data it is about rather than as struct
// literals. Accounts are otherwise empty: SeasonRanks reads nothing else.
func seasonUsers(nets map[string]int64) map[string]*UserAccount {
	users := make(map[string]*UserAccount, len(nets))
	for userID, net := range nets {
		users[userID] = &UserAccount{SeasonNet: net}
	}
	return users
}

// assertRanks compares a ranking against the exact expected rows, in order.
func assertRanks(t *testing.T, got, want []SeasonRank) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(ranks) = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ranks[%d] = %+v, want %+v (full: %+v)", i, got[i], want[i], got)
		}
	}
}

func TestSeasonRanks_OrdersByNetDescending(t *testing.T) {
	// Deliberately inserted in an order that is neither the answer nor its
	// reverse, so a function that simply returned map iteration order could
	// not pass by luck.
	got := SeasonRanks(seasonUsers(map[string]int64{
		"mid": 500, "top": 1000, "low": 1,
	}), 10)
	assertRanks(t, got, []SeasonRank{
		{UserID: "top", Net: 1000},
		{UserID: "mid", Net: 500},
		{UserID: "low", Net: 1},
	})
}

// A tie must resolve to the SAME podium on every run. Go randomises map
// iteration order, so a sort without the UserID tie-break hands the
// 10,000-chip prize to a different person each time the ranking is computed
// — and the season is ranked once per month with real chips attached.
// Looping makes the flake deterministic evidence rather than an occasional
// red run: 200 iterations of a 3-way tie put the odds of a broken
// implementation passing below (1/6)^200.
func TestSeasonRanks_TiesBreakOnUserIDAscending(t *testing.T) {
	for i := 0; i < 200; i++ {
		got := SeasonRanks(seasonUsers(map[string]int64{
			"charlie": 700, "alice": 700, "bob": 700,
		}), 10)
		assertRanks(t, got, []SeasonRank{
			{UserID: "alice", Net: 700},
			{UserID: "bob", Net: 700},
			{UserID: "charlie", Net: 700},
		})
	}
}

// 純利 exactly 0 is not a last place, it is "did not play" — in a guild
// where /balance has auto-created accounts for people who never bet, ranking
// them would fill the podium with non-players.
func TestSeasonRanks_ExcludesZeroNet(t *testing.T) {
	got := SeasonRanks(seasonUsers(map[string]int64{
		"played": 10, "never-played": 0, "also-never": 0,
	}), 10)
	assertRanks(t, got, []SeasonRank{{UserID: "played", Net: 10}})
}

// Losers stay in. Excluding negatives alongside the zeros would empty the
// podium in a month where everybody lost, and "lost the least" is a real
// result. This also pins the ordering across the sign boundary: -1 ranks
// above -1000, and both rank below any winner.
func TestSeasonRanks_KeepsNegativeNets(t *testing.T) {
	got := SeasonRanks(seasonUsers(map[string]int64{
		"big-loser": -1000, "small-loser": -1, "winner": 5,
	}), 10)
	assertRanks(t, got, []SeasonRank{
		{UserID: "winner", Net: 5},
		{UserID: "small-loser", Net: -1},
		{UserID: "big-loser", Net: -1000},
	})
}

// The limit truncates the TAIL, i.e. it keeps the best rows. A truncation
// applied before the sort (or from the wrong end) would award the podium to
// the three lowest 純利 in the guild.
func TestSeasonRanks_LimitKeepsTheTopRows(t *testing.T) {
	got := SeasonRanks(seasonUsers(map[string]int64{
		"a": 400, "b": 300, "c": 200, "d": 100, "e": -50,
	}), seasonRankLimit)
	assertRanks(t, got, []SeasonRank{
		{UserID: "a", Net: 400},
		{UserID: "b", Net: 300},
		{UserID: "c", Net: 200},
	})
}

func TestSeasonRanks_NonPositiveLimit_ReturnsNothing(t *testing.T) {
	users := seasonUsers(map[string]int64{"a": 400, "b": 300})
	for _, limit := range []int{0, -1} {
		if got := SeasonRanks(users, limit); len(got) != 0 {
			t.Fatalf("SeasonRanks(limit=%d) = %+v, want no rows", limit, got)
		}
	}
}

// A hand-edited or partially-written {"users":{"someone":null}} reaches this
// loop intact — ensureAccountLocked only repairs the ONE user a write path
// touches. Dereferencing it panics in a discordgo handler goroutine (no
// recover), which kills the bot and leaves the same file on disk: a
// permanent crash loop, exactly like topAssetsLocked's null.
func TestSeasonRanks_SkipsNilAccounts(t *testing.T) {
	users := seasonUsers(map[string]int64{"real": 100})
	users["corrupt"] = nil
	assertRanks(t, SeasonRanks(users, 10), []SeasonRank{{UserID: "real", Net: 100}})
}

func TestSeasonRanks_EmptyGuild_ReturnsNothing(t *testing.T) {
	if got := SeasonRanks(map[string]*UserAccount{}, 10); len(got) != 0 {
		t.Fatalf("SeasonRanks on an empty guild = %+v, want no rows", got)
	}
	if got := SeasonRanks(nil, 10); len(got) != 0 {
		t.Fatalf("SeasonRanks(nil) = %+v, want no rows", got)
	}
}

// Bonus is the CALLER's to fill in with what the credit actually landed
// (設計書 C-3b §2). A ranking that pre-filled it with the table value would
// make a capped winner's row claim chips the balance never received.
func TestSeasonRanks_LeavesBonusUnset(t *testing.T) {
	for _, rank := range SeasonRanks(seasonUsers(map[string]int64{"a": 1, "b": 2}), 10) {
		if rank.Bonus != 0 {
			t.Fatalf("SeasonRanks filled in Bonus for %q: %+v", rank.UserID, rank)
		}
	}
}

func TestSeasonBonus_PodiumAmounts(t *testing.T) {
	for _, tc := range []struct {
		rank int
		want int64
	}{
		{1, 10_000},
		{2, 5_000},
		{3, 2_500},
		{4, 0},
		{100, 0},
		// 0 and negatives are what a caller that forgot the 1-based
		// convention passes (a 0-based loop index). They must pay nothing
		// rather than fall into the first place.
		{0, 0},
		{-1, 0},
	} {
		if got := SeasonBonus(tc.rank); got != tc.want {
			t.Fatalf("SeasonBonus(%d) = %d, want %d", tc.rank, got, tc.want)
		}
	}
}

// The podium length and the bonus table must not drift apart: rolloverSeason
// truncates to seasonRankLimit and then asks SeasonBonus for place i+1, so a
// limit raised past the table would silently seat a 4th place with a 0-chip
// prize, and one lowered below it would strand a paying place.
func TestSeasonBonus_MatchesPodiumLength(t *testing.T) {
	if got := SeasonBonus(seasonRankLimit); got <= 0 {
		t.Fatalf("SeasonBonus(seasonRankLimit=%d) = %d, want a paying place", seasonRankLimit, got)
	}
	if got := SeasonBonus(seasonRankLimit + 1); got != 0 {
		t.Fatalf("SeasonBonus(%d) = %d, want 0 — the podium has only %d places", seasonRankLimit+1, got, seasonRankLimit)
	}
}
