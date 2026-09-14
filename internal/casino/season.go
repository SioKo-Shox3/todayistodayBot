package casino

import "sort"

// season.go is the monthly season's pure half (設計書 C-3b §4): who is on
// the podium, and what each place is worth. The switch itself —
// rolloverSeasonLocked — lives in store.go with the rest of the daily
// rollover, for the same reason drawLotteryLocked does: it writes accounts.
//
// Splitting the ranking out is what makes the tie-break and the 純利 0
// exclusion testable without a file on disk, exactly as lottery.go splits
// LotteryPrize and PickLotteryWinner from the draw that applies them.

// seasonRankLimit is how many places the podium has. It is the length of the
// bonus table below, and it is what rolloverSeasonLocked truncates to — the
// two must not drift apart, which is why the bonus function is written as a
// switch over the same three places rather than as an index into a slice
// that could be resized on its own.
const seasonRankLimit = 3

// SeasonBonus is the prize for finishing in `rank` (1-based): 10,000 /
// 5,000 / 2,500 chips for the top three, nothing below that (設計書 C-3b §2).
//
// This is NEWLY MINTED currency, the deliberate exception to the package's
// conservation rule that §2 names alongside the daily bonus — the chips do
// not come out of anyone's balance and no pot is debited to pay it. The
// caller must therefore credit it through creditChipsCappedLocked like any
// other payout, and must NOT feed it back into SeasonNet (§3 excludes the
// season's own prize, so that being handed a prize cannot win the next
// month).
//
// Ranks outside [1, seasonRankLimit] — including 0 and negatives, which a
// caller that forgot the 1-based convention would pass — pay nothing.
func SeasonBonus(rank int) int64 {
	switch rank {
	case 1:
		return 10_000
	case 2:
		return 5_000
	case 3:
		return 2_500
	default:
		return 0
	}
}

// SeasonRanks ranks `users` by 純利, highest first, and returns the top
// `limit` rows with Bonus left at 0 — the caller fills that in with what the
// credit actually landed, because only the caller knows the balances.
//
// 純利 exactly 0 is EXCLUDED rather than ranked last. An account at 0 is
// either someone who has not played since the season opened or someone whose
// wins and losses cancelled exactly, and in a guild where most accounts exist
// only because /balance auto-created them the first kind would otherwise fill
// the podium with people who never placed a bet. It is also what makes
// SeasonResult.Players mean 「遊んだ人数」. Losers are NOT excluded: a negative
// 純利 is a real result and stays in the ranking (it can even reach the
// podium in a month where everybody lost, which is the honest reading —
// somebody did lose the least).
//
// Ties break on UserID ascending, so the podium is a pure function of the
// data rather than of Go's randomised map iteration order — without it a
// two-way tie for first would hand the 10,000-chip prize to a different
// person on every run, and a crash between the credit and the write would
// pay it twice to different people.
//
// nil accounts are skipped rather than repaired, for topAssetsLocked's
// reason: a hand-edited or partially-written {"users":{"someone":null}}
// reaches this loop intact, and dereferencing it would kill the bot, while
// repairing it here would open an account (and mint the welcome bonus) for
// someone who never played.
//
// Net is copied as stored. This function does no arithmetic on it — only
// comparisons — so an out-of-range value from a hand-edited file cannot wrap
// anything here; bounding it is normalizeAccountLocked's job at the write
// path, which is what keeps the number this returns inside [-MaxChips,
// MaxChips] when it is about to be persisted into SeasonResult.
func SeasonRanks(users map[string]*UserAccount, limit int) []SeasonRank {
	if limit <= 0 {
		return nil
	}
	ranks := make([]SeasonRank, 0, len(users))
	for userID, account := range users {
		if account == nil || account.SeasonNet == 0 {
			continue
		}
		ranks = append(ranks, SeasonRank{UserID: userID, Net: account.SeasonNet})
	}
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].Net != ranks[j].Net {
			return ranks[i].Net > ranks[j].Net
		}
		return ranks[i].UserID < ranks[j].UserID
	})
	if len(ranks) > limit {
		ranks = ranks[:limit]
	}
	return ranks
}
