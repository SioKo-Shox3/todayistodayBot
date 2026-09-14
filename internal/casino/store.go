package casino

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DefaultPath is the production data file location, relative to the working
// directory the bot is launched from — mirrors internal/store.DefaultPath's
// convention.
const DefaultPath = "data/casino.json"

// Store is a mutex-guarded single-writer JSON file store for the casino
// economy. Every writer of the SAME underlying file must share one *Store
// instance (one sync.Mutex) — see Default() for the production singleton.
type Store struct {
	mu   sync.Mutex
	path string
	// rng is the randomness source for rate generation and slot spins. It is
	// read ONLY while mu is held (i.e. from inside an Update closure):
	// *rand.Rand is not safe for concurrent use.
	rng randSource
	// clock is the wall-clock source for the operations that decide a JST
	// calendar date for themselves (ClaimDaily). nil means time.Now. Like
	// rng it is read ONLY while mu is held, so the date a claim is stamped
	// with is chosen inside the single-writer critical section: two /daily
	// invocations whose instants straddle midnight can no longer be applied
	// in the wrong order. Tests pin it; production never sets it.
	clock func() time.Time
}

// New returns a Store backed by path. Production code should use Default()
// instead, to guarantee every command shares the same mutex; New is for
// tests, where each test's own t.TempDir()-backed file legitimately needs
// its own, independent Store.
func New(path string) *Store {
	return &Store{path: path, rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

var (
	defaultOnce  sync.Once
	defaultStore *Store
)

// Default returns the process-wide singleton Store for DefaultPath. Every
// casino command and the 9am announcement scheduler must call Default() —
// not New(DefaultPath) — so every read/write to data/casino.json goes
// through the same sync.Mutex.
func Default() *Store {
	defaultOnce.Do(func() { defaultStore = New(DefaultPath) })
	return defaultStore
}

// Update runs fn against the current on-disk Data under the store's single
// writer lock, and persists fn's mutations if fn returns nil (a non-nil
// error aborts the write — the file is left untouched). This is the ONLY
// way production code may mutate casino data; every balance change, rate
// generation, or admin mint must go through Update so concurrent commands
// (e.g. two simultaneous /slot bets in the same guild) serialize correctly
// — mirrors internal/store.Store's mutex discipline.
//
// RE-ENTRANCY CONTRACT (危険地帯 — violating this deadlocks the bot):
// s.mu is a plain, NON-reentrant sync.Mutex. fn therefore must NOT call any
// method that takes the lock (Update, Snapshot, Mint, SetAnnounceChannel,
// EnsureTodayRate, EnsureCasinoAccess, ClaimDaily, ExchangeCoinToChip,
// ExchangeChipToCoin, Spin, ViewAccount, TopAssets, RecentRates, and every
// locking method added by later tasks). Use the unexported *Locked helpers instead
// (ensureGuildLocked, ensureAccountLocked, ensureTodayRateLocked,
// topAssetsLocked, creditChipsLocked, creditCoinsLocked) — none of those take
// the lock. fn must also not perform network I/O, Discord API calls, or
// sleeps — the whole guild economy is blocked for its duration.
func (s *Store) Update(fn func(*Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.readLocked()
	if err != nil {
		return err
	}
	if err := fn(&data); err != nil {
		return err
	}
	return s.writeLocked(data)
}

// Snapshot returns the current on-disk Data, read under the store's lock so
// it cannot observe a concurrent Update's half-applied read-modify-write.
// The returned value is freshly unmarshalled on every call and aliased by
// nobody (the Store keeps no in-memory copy), so the caller may read it
// freely after Snapshot returns — but mutating it has NO effect on the file
// (use Update for that).
//
// This deliberately replaces an earlier `Read(fn func(Data) error)` design:
// running an arbitrary callback while holding the mutex reproduced Update's
// re-entrancy hazard (any locking method called from the callback
// deadlocks) and its I/O-under-lock hazard, without Update's justification
// for taking that risk. Returning a value has no such contract to violate.
func (s *Store) Snapshot() (Data, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.readLocked()
}

// readLocked parses the JSON file. Caller must hold mu. It ALWAYS returns a
// non-nil Data on success, via all three degenerate paths — missing file,
// zero-byte file, and the JSON literal `null`. This is not tidiness: Data
// is a map, so handing a nil one to ensureGuildLocked panics with
// "assignment to entry in nil map", discordgo runs handlers in goroutines
// without recover, and the bad file survives the crash — a permanent crash
// loop. A zero-byte file is reachable in practice (power loss mid-write).
func (s *Store) readLocked() (Data, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return Data{}, nil // pre-first-command state, not an error
		}
		return nil, fmt.Errorf("casino: reading %q: %w", s.path, err)
	}
	if len(raw) == 0 {
		return Data{}, nil // truncated/zero-byte file
	}
	var d Data
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("casino: parsing %q: %w", s.path, err)
	}
	if d == nil {
		d = Data{} // JSON literal `null` unmarshals a map to nil
	}
	return d, nil
}

// writeLocked serializes data to a temp file in the same directory and
// renames it over the destination path, so a crash mid-write cannot leave a
// truncated/corrupt data/casino.json. On Linux (same directory) the rename
// is atomic; on non-Unix platforms os.Rename is explicitly NOT an atomic
// operation (go doc os.Rename), so there it is best-effort — readLocked's
// missing/zero-byte/null guards are what actually keep a torn file from
// crashing the bot. tmp.Sync() runs before the rename so the temp file's
// bytes have reached the disk, not just the page cache, when it takes the
// destination's place (a deliberate difference from
// internal/store/events.go, which predates this). The temp-file glob is
// ".casino-*.json.tmp" — same convention as events.go's
// ".chosei-events-*.json.tmp", different prefix so the two stores' temp
// files stay visually distinguishable in a shared data/ directory during
// debugging. Caller must hold mu.
func (s *Store) writeLocked(data Data) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("casino: creating directory %q: %w", dir, err)
	}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("casino: marshaling data: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".casino-*.json.tmp")
	if err != nil {
		return fmt.Errorf("casino: creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("casino: writing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("casino: syncing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("casino: closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("casino: renaming temp file to %q: %w", s.path, err)
	}
	return nil
}

// jst is the fixed +09:00 zone every calendar-day boundary in this package
// is computed in. JST has no daylight saving, so a fixed offset is exact,
// and unlike time.LoadLocation("Asia/Tokyo") it needs no tzdata file — that
// lookup can fail on a minimal Linux container.
var jst = time.FixedZone("JST", 9*3600)

// jstDate formats t's JST calendar date as "2006-01-02". Never depend on
// time.Local here: the bot's host may run in any zone.
func jstDate(t time.Time) string { return t.In(jst).Format("2006-01-02") }

// nowLocked returns the current instant from the store's clock (time.Now
// unless a test pinned it). Read s.clock only from inside an Update closure,
// i.e. with s.mu held — same discipline as s.rng.
func (s *Store) nowLocked() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// jstYesterday returns the JST calendar date one day before t's JST date.
// It converts to JST FIRST and only then subtracts a day: doing
// jstDate(t.AddDate(0,0,-1)) instead would subtract one calendar day in t's
// OWN location, which is 23h or 25h long on a DST transition day in
// America/*, Europe/* etc. On a Linux host with such a TZ that silently
// yields 一昨日 or 今日 in JST terms, breaking /daily streaks.
func jstYesterday(t time.Time) string { return t.In(jst).AddDate(0, 0, -1).Format("2006-01-02") }

// ensureGuildLocked returns d's entry for guildID, creating an empty one if
// absent. Also repairs a nil Users map and a nil *GuildEconomy value (which
// a hand-edited or partially-written file can contain as `{"g": null}`).
// Caller must already hold the Store's lock (call only from inside an
// Update closure). EVERY read of (*d)[guildID] must go through this — see
// the nil-map hazard on readLocked.
func ensureGuildLocked(d *Data, guildID string) *GuildEconomy {
	if (*d)[guildID] == nil {
		(*d)[guildID] = &GuildEconomy{Users: map[string]*UserAccount{}}
	}
	if (*d)[guildID].Users == nil {
		(*d)[guildID].Users = map[string]*UserAccount{}
	}
	return (*d)[guildID]
}

// welcomeBonusChips is the one-time grant a brand-new account opens with
// (設計書). It is an initial value, not a credit, and 1,000 is trivially
// below MaxChips — that is why it is the only chip increase in this package
// that does not go through creditChipsLocked.
const welcomeBonusChips = 1000

// ensureAccountLocked returns economy's account for userID, auto-creating
// it with the 1,000-chip welcome bonus if this is userID's first casino
// interaction in this guild. Caller must already hold the Store's lock.
func ensureAccountLocked(economy *GuildEconomy, userID string) *UserAccount {
	if economy.Users[userID] == nil {
		economy.Users[userID] = &UserAccount{Chips: welcomeBonusChips}
	}
	return economy.Users[userID]
}

// creditChipsLocked adds delta to account.Chips, refusing to push the
// account's HELD chips — Chips + Escrow — past MaxChips. Caller must already
// hold the Store's lock. Every chip credit in this package goes through here
// — do NOT write `account.Chips += x` anywhere else (moveToEscrowLocked and
// moveFromEscrowLocked are the two exceptions, and they move chips between
// the account's own two fields rather than creating any).
//
// Escrow counts against the cap because it is chips the account still owns:
// leaving it out let a credit fill Chips to MaxChips while a stake was in
// flight, and the refund that followed then had nowhere to put the stake.
// Every comparison is written in "headroom" form (subtract from MaxChips)
// rather than as a sum, because the sums can wrap on a hand-edited file.
func creditChipsLocked(account *UserAccount, delta int64) error {
	if delta < 0 || account.Chips < 0 || account.Escrow < 0 {
		return ErrChipCapExceeded
	}
	if account.Chips > MaxChips-account.Escrow {
		return ErrChipCapExceeded // already over the cap (hand-edited file)
	}
	if delta > MaxChips-account.Escrow-account.Chips {
		return ErrChipCapExceeded
	}
	account.Chips += delta
	return nil
}

// creditCoinsLocked mirrors creditChipsLocked for MaxCoins.
func creditCoinsLocked(account *UserAccount, delta int64) error {
	if delta < 0 || account.Coins > MaxCoins-delta {
		return ErrCoinCapExceeded
	}
	account.Coins += delta
	return nil
}

// Mint credits amount coins to guildID/userID (auto-creating the account if
// needed). Caller (internal/commands/casino_admin.go) owns the permission
// check — Mint has no notion of Discord permissions. Refuses amounts
// outside [1, MaxCoins] and any credit that would push the balance past
// MaxCoins — Discord's MaxValue on the option is a UX nicety, not a
// guarantee: discordgo's IntValue() is int64(o.Value.(float64)), so a
// direct API call can send ~2^53. A refusal persists nothing at all.
func (s *Store) Mint(guildID, userID string, amount int64) error {
	if amount < 1 || amount > MaxCoins {
		return ErrInvalidAmount
	}
	return s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		account := ensureAccountLocked(economy, userID)
		return creditCoinsLocked(account, amount)
	})
}

// SetAnnounceChannel sets guildID's 9am announcement channel.
func (s *Store) SetAnnounceChannel(guildID, channelID string) error {
	return s.Update(func(d *Data) error {
		ensureGuildLocked(d, guildID).AnnounceChannelID = channelID
		return nil
	})
}

// EnsureTodayRate makes sure guildID has a DailyRate for today's JST
// calendar date, generating one via a random-walk step if missing. Public
// entry point — takes the lock itself. Do NOT call this from inside
// another Update closure (deadlock) — internal callers that need "today's
// rate" as part of their own transaction must call ensureTodayRateLocked
// directly instead (see EnsureCasinoAccess/TopAssets/ClaimDaily's
// neighbours/Exchange*/ViewAccount/RecentRates below).
func (s *Store) EnsureTodayRate(guildID string, now time.Time) (DailyRate, error) {
	today := jstDate(now)
	var result DailyRate
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		result = ensureTodayRateLocked(economy, today, s.rng)
		return nil
	})
	return result, err
}

// ensureTodayRateLocked returns economy's DailyRate for `today`, appending
// a freshly-generated one (via nextRate) if missing or unusable, and trimming
// history to the most recent 30 entries (設計書). A `today` the history has
// already reached is answered from the history instead (settledRateLocked):
// the history only ever moves forward. Caller must already hold the
// Store's lock, which is also what makes reading s.rng here safe — *rand.Rand
// is not safe for concurrent use.
//
// A stored rate outside the [minRate, maxRate] clamp counts as MISSING, not as
// today's rate. nextRate clamps everything it returns, so such a record can
// only come from a hand edit or a partial write — and a record that simply
// lacks the "rate" field unmarshals as Rate 0. Returning that 0 would feed a
// zero divisor to exchangeCoinToChip/exchangeChipToCoin, and an integer
// divide-by-zero panic in a discordgo handler goroutine (no recover) kills the
// bot while the bad file stays on disk: every restart re-crashes. Repairing it
// HERE, at the single point every consumer reads today's rate through, protects
// /exchange, /rank, /rate and the 9am announcement in one place.
func ensureTodayRateLocked(economy *GuildEconomy, today string, rng randSource) DailyRate {
	rate, _ := ensureTodayRateIndexLocked(economy, today, rng)
	return rate
}

// ensureTodayRateIndexLocked is ensureTodayRateLocked plus the position the
// returned rate occupies in economy.Rates, for the one caller (RecentRates)
// that has to show the history AROUND that entry. The index is -1 when no
// stored entry backs the rate — the synthesised 基準100 fallback below.
func ensureTodayRateIndexLocked(economy *GuildEconomy, today string, rng randSource) (DailyRate, int) {
	// Drop unusable today records rather than appending after them, so the
	// history never ends up holding two entries for the same date, and so the
	// regeneration below starts from the last VALID day instead of from a
	// corrupt one. The loop (rather than a single check) matters only for a
	// file hand-edited into holding SEVERAL today records: stripping just the
	// last one would leave the previous corrupt one both as a duplicate date
	// and as nextRate's prevRate.
	for len(economy.Rates) > 0 {
		last := len(economy.Rates) - 1
		if economy.Rates[last].Date != today {
			break
		}
		if validRate(economy.Rates[last].Rate) {
			return economy.Rates[last], last
		}
		economy.Rates = economy.Rates[:last]
	}
	// A date the history has already reached must never be drawn a second
	// time. The tail check above only asks "is the LAST entry today?", so a
	// clock that moves backwards between two calls (an NTP correction, a hand
	// -set system time, a host whose zone data is fixed at boot) turns
	// D→D+1→D into an append: the history becomes D,D+1,D,D+1 and day D gets
	// a fresh, different rate after /exchange has already paid out at the old
	// one. Dates are "2006-01-02", so string order is calendar order.
	// The tail is not enough to decide that: a history written BEFORE this
	// rule existed can hold the days out of order (D, D+1, D), and then a
	// second D+1 would append on top of the D+1 already stored. Search the
	// whole history for the date first; only when no entry carries it does
	// the tail comparison decide between "a past day the history skipped
	// over" (answer from the history) and "a genuinely new day" (draw).
	if len(economy.Rates) > 0 {
		settled, idx := settledRateIndexLocked(economy, today)
		if idx >= 0 && settled.Date == today {
			return settled, idx
		}
		if today <= economy.Rates[len(economy.Rates)-1].Date {
			return settled, idx
		}
	}
	var prevRate int
	var prevTrend TrendState
	hasPrev := len(economy.Rates) > 0
	if hasPrev {
		last := economy.Rates[len(economy.Rates)-1]
		prevRate, prevTrend = last.Rate, last.Trend
	}
	rate, trend, event := nextRate(prevRate, prevTrend, hasPrev, rng)
	result := DailyRate{Date: today, Rate: rate, Trend: trend, Event: event}
	economy.Rates = append(economy.Rates, result)
	if len(economy.Rates) > 30 {
		economy.Rates = economy.Rates[len(economy.Rates)-30:]
	}
	return result, len(economy.Rates) - 1
}

// settledRateIndexLocked answers "what rate does `date` report?" WITHOUT
// touching the history, and where in the history that answer lives: the entry
// stored for `date` itself when there is one, otherwise the newest entry —
// the rate the guild has actually settled on. Caller must already hold the
// Store's lock.
//
// Only in-clamp entries qualify, for the crash-loop reason spelled out on
// ensureTodayRateLocked: a hand-edited record must never be handed back as a
// divisor. A history in which no entry at all is usable falls back to the
// 基準100 starting point rather than to a zero.
func settledRateIndexLocked(economy *GuildEconomy, date string) (DailyRate, int) {
	var newest DailyRate
	newestIdx := -1
	for i := len(economy.Rates) - 1; i >= 0; i-- {
		entry := economy.Rates[i]
		if !validRate(entry.Rate) {
			continue
		}
		if entry.Date == date {
			return entry, i
		}
		if newestIdx < 0 {
			newest, newestIdx = entry, i
		}
	}
	if newestIdx >= 0 {
		return newest, newestIdx
	}
	return DailyRate{Date: date, Rate: baseRate, Trend: TrendFlat, Event: EventNone}, -1
}

// EnsureCasinoAccess commits, as its OWN transaction, the two invariants
// every casino command depends on: today's DailyRate exists for guildID,
// and guildID/userID has an account (welcome bonus granted on first ever
// access). Callers run this BEFORE their own Update so a failing main
// operation cannot roll the new account and its welcome bonus back — and so
// that "which command you happened to run first" cannot change the outcome.
// Idempotent: repeat calls neither re-grant the bonus nor regenerate the
// rate. All seven commands call it, /rate and /rank included.
func (s *Store) EnsureCasinoAccess(guildID, userID string, now time.Time) error {
	today := jstDate(now)
	return s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		ensureTodayRateLocked(economy, today, s.rng)
		ensureAccountLocked(economy, userID)
		return nil
	})
}

// RankEntry is one row of the total-assets ranking.
type RankEntry struct {
	UserID      string
	TotalAssets int64 // Chips + Escrow + Coins*rate
}

// totalAssetsLocked values one account at `rate`. Escrow counts as the
// user's own money (設計書 §3): it is chips that left Chips only for the
// duration of a hand, so leaving it out would drop a player's bet out of
// the ranking and off /balance while they are mid-game, then have it
// reappear at settlement. Caller must already hold the Store's lock.
//
// Still cannot overflow int64: every write path keeps Chips + Escrow <=
// MaxChips (staking moves chips between the two fields and settlement
// credits through creditChipsLocked), so the bound is unchanged from
// Chips-only — MaxChips + MaxCoins*maxRate = 1.41e14, about 65,413x below
// math.MaxInt64.
func totalAssetsLocked(account *UserAccount, rate int) int64 {
	return account.Chips + account.Escrow + account.Coins*int64(rate)
}

// topAssetsLocked ranks economy's users by total assets at `rate`, ties
// broken by ascending UserID for deterministic output. Nil accounts are
// skipped (see below). Caller must already hold the Store's lock.
//
// Chips + Coins*rate cannot overflow int64: the store invariants are
// Chips <= MaxChips (1e12), Coins <= MaxCoins (1e12) and rate <= maxRate
// (140, the clamp ceiling), so the sum is bounded by
// 1e12 + 1e12*140 = 1.41e14, about 65,413x below math.MaxInt64
// (9.223e18). Every credit path enforces those caps.
func topAssetsLocked(economy *GuildEconomy, rate int, limit int) []RankEntry {
	entries := make([]RankEntry, 0, len(economy.Users))
	for userID, account := range economy.Users {
		if account == nil {
			// A hand-edited or partially-written file can hold
			// {"users":{"someone":null}}. ensureGuildLocked repairs the guild
			// and its Users map, and ensureAccountLocked repairs only the ONE
			// user a write path touches — so ANOTHER user's null reaches this
			// loop intact, where account.Chips dereferences nil. discordgo runs
			// handlers in goroutines without recover, so that kills the bot and
			// leaves the same bad file behind: a permanent crash loop, exactly
			// like readLocked's nil-map hazard.
			//
			// SKIPPED, NOT REPAIRED: this is a display-only read path, and
			// opening an account grants the 1,000-chip welcome bonus, which
			// would mint currency for someone who never ran a casino command
			// merely because a third party read the ranking. Account creation
			// belongs to the write path (ensureAccountLocked), which repairs
			// the entry the moment that user actually plays.
			continue
		}
		entries = append(entries, RankEntry{UserID: userID, TotalAssets: totalAssetsLocked(account, rate)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].TotalAssets != entries[j].TotalAssets {
			return entries[i].TotalAssets > entries[j].TotalAssets
		}
		return entries[i].UserID < entries[j].UserID
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

// TopAssets returns guildID's top `limit` accounts by total assets, using
// today's rate (generated first if missing, same transaction).
func (s *Store) TopAssets(guildID string, now time.Time, limit int) ([]RankEntry, error) {
	today := jstDate(now)
	var result []RankEntry
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		rate := ensureTodayRateLocked(economy, today, s.rng)
		result = topAssetsLocked(economy, rate.Rate, limit)
		return nil
	})
	return result, err
}

// DailyClaimResult is what a successful daily claim paid out.
type DailyClaimResult struct {
	Amount       int64 // credited chips
	NewStreak    int
	JackpotBonus bool // true iff the +500 "大入り袋" applied (NewStreak%7==0)
}

// ClaimDaily pays out today's daily bonus to guildID/userID (JST calendar
// date of the store's clock, read under the lock), auto-creating the account
// (welcome bonus) on first interaction. Returns ErrAlreadyClaimedToday (no
// mutation) if today is not strictly AFTER the last claimed date — that
// covers both a second claim on the same day and a claim whose date has gone
// backwards — or ErrChipCapExceeded (also no mutation) if the payout would
// break MaxChips.
func (s *Store) ClaimDaily(guildID, userID string) (DailyClaimResult, error) {
	var result DailyClaimResult
	err := s.Update(func(d *Data) error {
		// The claim's instant is read HERE, under the lock — not by the
		// caller before it. A pre-lock read lets two /daily invocations that
		// straddle midnight reach the store in the reverse order of the
		// instants they carry.
		now := s.nowLocked()
		today := jstDate(now)
		yesterday := jstYesterday(now) // NOT jstDate(now.AddDate(0,0,-1)) — see jstYesterday's doc
		economy := ensureGuildLocked(d, guildID)
		account := ensureAccountLocked(economy, userID)
		// LastDailyDate is a "2006-01-02" string, so a lexical comparison IS
		// the calendar comparison: the claim date is monotonically
		// non-decreasing. Rejecting an EARLIER date as well as an equal one
		// is what stops the D→D+1→D→D+1 sequence from paying four times for
		// two days.
		if account.LastDailyDate != "" && today <= account.LastDailyDate {
			return ErrAlreadyClaimedToday
		}
		streak := 1
		if account.LastDailyDate == yesterday {
			streak = account.StreakDays + 1
		}
		amount := dailyBonusAmount(streak)
		if err := creditChipsLocked(account, amount); err != nil {
			return err // aborts the transaction: streak/date/balance all stay untouched
		}
		account.StreakDays = streak
		account.LastDailyDate = today
		result = DailyClaimResult{Amount: amount, NewStreak: streak, JackpotBonus: streak%7 == 0}
		return nil
	})
	if err != nil {
		return DailyClaimResult{}, err
	}
	return result, nil
}

// ExchangeResult is what a successful exchange moved.
type ExchangeResult struct {
	Spent    int64 // debited from source currency
	Received int64 // credited to destination currency
	RateUsed int
}

// ExchangeCoinToChip converts coins to chips for guildID/userID at today's
// rate (ensured in the same transaction, so no concurrent command can change
// the rate between reading it and moving the balances). Minimum: coins >= 1.
func (s *Store) ExchangeCoinToChip(guildID, userID string, coins int64, now time.Time) (ExchangeResult, error) {
	if coins < 1 {
		return ExchangeResult{}, ErrInvalidAmount
	}
	today := jstDate(now)
	var result ExchangeResult
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		rate := ensureTodayRateLocked(economy, today, s.rng)
		account := ensureAccountLocked(economy, userID)
		if account.Coins < coins {
			return &ErrInsufficientCoins{Balance: account.Coins}
		}
		chips, err := exchangeCoinToChip(coins, rate.Rate)
		if err != nil {
			return err
		}
		if err := creditChipsLocked(account, chips); err != nil {
			return err // ErrChipCapExceeded — aborts before the debit is persisted
		}
		account.Coins -= coins
		result = ExchangeResult{Spent: coins, Received: chips, RateUsed: rate.Rate}
		return nil
	})
	return result, err
}

// ExchangeChipToCoin converts chips to coins for guildID/userID at today's
// rate. Minimum is on the RESULT (設計書: "結果が1コイン以上になる量"),
// checked after computing the exchange.
func (s *Store) ExchangeChipToCoin(guildID, userID string, chips int64, now time.Time) (ExchangeResult, error) {
	if chips < 1 {
		return ExchangeResult{}, ErrInvalidAmount
	}
	today := jstDate(now)
	var result ExchangeResult
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		rate := ensureTodayRateLocked(economy, today, s.rng)
		account := ensureAccountLocked(economy, userID)
		if account.Chips < chips {
			return &ErrInsufficientChips{Balance: account.Chips}
		}
		coins, err := exchangeChipToCoin(chips, rate.Rate)
		if err != nil {
			return err
		}
		if coins < 1 {
			return ErrBelowMinimumExchange
		}
		if err := creditCoinsLocked(account, coins); err != nil {
			return err // ErrCoinCapExceeded — aborts before the debit is persisted
		}
		account.Chips -= chips
		result = ExchangeResult{Spent: chips, Received: coins, RateUsed: rate.Rate}
		return nil
	})
	return result, err
}

// AccountView is one account plus the totals derived from today's rate.
// Account carries the escrow trio verbatim, so /balance can show what is
// staked on an in-flight game; TotalAssets already includes it.
type AccountView struct {
	Account     UserAccount
	RateUsed    int
	TotalAssets int64
}

// ViewAccount ensures guildID/userID's account exists (welcome bonus on
// first use) and today's rate exists, returning both plus derived total
// assets.
func (s *Store) ViewAccount(guildID, userID string, now time.Time) (AccountView, error) {
	today := jstDate(now)
	var result AccountView
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		rate := ensureTodayRateLocked(economy, today, s.rng)
		account := *ensureAccountLocked(economy, userID)
		result = AccountView{Account: account, RateUsed: rate.Rate, TotalAssets: totalAssetsLocked(&account, rate.Rate)}
		return nil
	})
	return result, err
}

// Spin atomically deducts bet chips, resolves the spin (via the pure `spin`,
// using s.rng), credits any payout, and persists — all in one Update
// ("控除→抽選→払い戻しの永続化", 設計書). Reading s.rng is safe only because
// the draw happens inside the closure, i.e. under the store's lock:
// *rand.Rand is not safe for concurrent use. Does NOT perform the reveal
// animation (internal/commands/slot.go's job). Returns ErrBetOutOfRange
// (defense in depth — the command layer's option bounds are a UX nicety, not
// a guarantee), *ErrInsufficientChips, or ErrChipCapExceeded — in every error
// case nothing at all is persisted, so the bet is never silently eaten.
func (s *Store) Spin(guildID, userID string, bet int64) (SpinResult, error) {
	if bet < 10 || bet > 1000 {
		return SpinResult{}, ErrBetOutOfRange
	}
	var result SpinResult
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		account := ensureAccountLocked(economy, userID)
		if account.Chips < bet {
			return &ErrInsufficientChips{Balance: account.Chips}
		}
		account.Chips -= bet
		result = spin(bet, s.rng)
		if err := creditChipsLocked(account, result.Payout); err != nil {
			return err // aborts: the deduction above is discarded with the transaction
		}
		return nil
	})
	if err != nil {
		return SpinResult{}, err
	}
	return result, nil
}

// RecentRates ensures today's rate exists (same transaction) then returns
// up to the most recent `limit` DailyRate entries (oldest..newest, today
// included as the last element) for guildID.
func (s *Store) RecentRates(guildID string, now time.Time, limit int) ([]DailyRate, error) {
	today := jstDate(now)
	var result []DailyRate
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		rate, idx := ensureTodayRateIndexLocked(economy, today, s.rng)
		// /rate reads the LAST element as "today's rate", so the history has
		// to end at the very entry /exchange charges from — not merely at the
		// last entry dated `rate.Date`. Cutting by index rather than by date
		// also covers a history whose days are stored out of order (see
		// ensureTodayRateIndexLocked), where a later element can carry an
		// EARLIER date. idx < 0 means no stored entry backs the rate at all.
		recent := economy.Rates
		if idx < 0 {
			recent = []DailyRate{rate}
		} else {
			recent = recent[:idx+1]
		}
		if len(recent) > limit {
			recent = recent[len(recent)-limit:]
		}
		result = recent
		return nil
	})
	return result, err
}

// --- escrow: chips staked on an in-flight button game (設計書 §3) ---------
//
// A button game's BOARD lives in memory (the session manager); only the
// STAKE is persisted, as account.Escrow. That split is what makes the money
// safe across a crash: chips leave Chips the moment the game opens, so no
// concurrent command can spend them twice, and a restart — which destroys
// every board — finds the orphaned stakes and hands them back
// (RefundStaleEscrows).
//
// The conservation law these four methods maintain, and that store_test.go's
// escrow tests pin down: across OpenGame, AddToEscrow, SettleGame and
// RefundStaleEscrows, an account's Chips + Escrow moves ONLY by the payout
// SettleGame is handed. Staking is a move between two fields of the same
// account, so it leaves the sum exactly where it was; a refund is the same
// move backwards. Nothing here touches Coins.

// moveToEscrowLocked stakes amount chips: Chips -= amount, Escrow += amount.
// The sum Chips + Escrow is unchanged, which is why this does NOT go through
// creditChipsLocked — no currency is created, so no cap is at risk (Escrow
// can never exceed what Chips already held). Caller must already hold the
// Store's lock.
func moveToEscrowLocked(account *UserAccount, amount int64) error {
	if amount < 1 {
		return ErrInvalidAmount
	}
	if account.Chips < amount {
		return &ErrInsufficientChips{Balance: account.Chips}
	}
	account.Chips -= amount
	account.Escrow += amount
	return nil
}

// moveFromEscrowLocked is the exact inverse of moveToEscrowLocked: the whole
// escrow goes back to Chips and the escrow trio is cleared. It returns the
// stake it moved. Like the forward move it does NOT go through
// creditChipsLocked and cannot fail — the chips were already the account's,
// counted against MaxChips the whole time they sat in escrow, so handing
// them back creates no currency and can break no cap. Capping (or refusing)
// a refund would DESTROY chips, which is exactly what the conservation law
// above forbids. Caller must already hold the Store's lock.
func moveFromEscrowLocked(account *UserAccount) int64 {
	stake := account.Escrow
	account.Chips += stake
	clearEscrowLocked(account)
	return stake
}

// clearEscrowLocked zeroes the whole escrow trio. The two string fields are
// cleared alongside the amount so `Escrow == 0` and "no game in progress"
// can never disagree — a leftover EscrowGame would make a settled account
// look in-flight to anything that reads the game name.
func clearEscrowLocked(account *UserAccount) {
	account.Escrow = 0
	account.EscrowGame = ""
	account.EscrowOpenedAt = ""
}

// OpenGame stakes bet chips on a new game of `game` for guildID/userID,
// auto-creating the account (welcome bonus) on first ever interaction.
// Returns ErrGameInProgress if the account already has chips in escrow,
// *ErrInsufficientChips if the balance will not cover the bet, and
// ErrInvalidAmount for a bet below 1 (defense in depth: the command layer's
// 10..1,000 option bounds are a UX nicety, not a guarantee). In every error
// case nothing is persisted, so a refused open cannot eat the bet.
//
// The one-game-at-a-time rule is enforced HERE, under the store's lock,
// rather than only in the in-memory session manager: two commands that race
// arrive on two discordgo goroutines, and the session map alone would let
// the loser overwrite the winner's escrow — chips that no settlement would
// ever return.
//
// now is the instant recorded as EscrowOpenedAt (RFC3339 in JST, the
// package-wide convention); no decision here reads it.
func (s *Store) OpenGame(guildID, userID, game string, bet int64, now time.Time) error {
	return s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		account := ensureAccountLocked(economy, userID)
		if account.Escrow > 0 {
			return ErrGameInProgress
		}
		if err := moveToEscrowLocked(account, bet); err != nil {
			return err
		}
		account.EscrowGame = game
		account.EscrowOpenedAt = now.In(jst).Format(time.RFC3339)
		return nil
	})
}

// AddToEscrow stakes amount MORE chips on the game already in flight —
// blackjack's ダブル (設計書 §7). Returns ErrNoGameInProgress when the
// account has no escrow (chips added to a game that does not exist would
// have no settlement to return them), *ErrInsufficientChips when the balance
// will not cover the raise, and ErrInvalidAmount below 1. Nothing is
// persisted on any of those, so the command layer's "persist first, then
// apply to the board" order leaves the board untouched when a raise is
// refused.
func (s *Store) AddToEscrow(guildID, userID string, amount int64) error {
	return s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		account := ensureAccountLocked(economy, userID)
		if account.Escrow == 0 {
			return ErrNoGameInProgress
		}
		return moveToEscrowLocked(account, amount)
	})
}

// SettleResult is what a settlement left behind.
type SettleResult struct {
	Chips  int64 // the account's chip balance after the payout landed
	Payout int64 // what was paid, stake included (0 on a loss)
}

// SettleGame closes the in-flight game for guildID/userID: the escrow is
// released and payout chips are credited. payout is the TOTAL the player
// receives, the returned stake included — a push pays the stake back, a loss
// pays 0, and the caller never adds the stake on top.
//
// Returns ErrNoGameInProgress when the account has no escrow. That is the
// store-level half of the double-settlement guard (the session manager
// dropping a settled session is the other half): a button pressed twice in
// the same instant resolves the same board on two goroutines, and without
// this check the second one would pay the whole payout again out of nothing.
// ErrInvalidAmount rejects a negative payout and ErrChipCapExceeded a payout
// that would break MaxChips — on any error nothing at all is persisted, so
// the escrow survives for a retry rather than vanishing.
func (s *Store) SettleGame(guildID, userID string, payout int64) (SettleResult, error) {
	var result SettleResult
	err := s.Update(func(d *Data) error {
		if payout < 0 {
			return ErrInvalidAmount
		}
		economy := ensureGuildLocked(d, guildID)
		account := ensureAccountLocked(economy, userID)
		if account.Escrow == 0 {
			return ErrNoGameInProgress
		}
		clearEscrowLocked(account)
		if err := creditChipsLocked(account, payout); err != nil {
			return err // aborts: the escrow release above is discarded with the transaction
		}
		result = SettleResult{Chips: account.Chips, Payout: payout}
		return nil
	})
	if err != nil {
		return SettleResult{}, err
	}
	return result, nil
}

// RefundStaleEscrows returns every chip still held in escrow, in every
// guild, to the account that staked it, and reports how many accounts were
// refunded. cmd/bot calls it ONCE at startup (設計書 §3).
//
// The refund is unconditional rather than age-based, and that is the whole
// point: boards live only in memory, so the process that could have settled
// these stakes no longer exists. An escrow observed at startup is by
// definition a game that can never finish. now is accepted for contract
// symmetry with OpenGame and to pin the call to the same startup instant the
// rest of the boot sequence uses; no decision here reads it.
//
// The refund moves the stake back rather than crediting it, so it never
// fails and never truncates: an account whose Chips + Escrow already exceeds
// MaxChips (a hand-edited file) keeps that whole sum, now all in Chips. Both
// alternatives are worse — capping would destroy the difference, and
// aborting would leave every account in every guild staked forever, unable
// to open another game.
func (s *Store) RefundStaleEscrows(now time.Time) (int, error) {
	refunded := 0
	err := s.Update(func(d *Data) error {
		refunded = 0 // never double-count if the closure is ever run more than once
		for _, economy := range *d {
			if economy == nil {
				continue // hand-edited {"g": null}; see ensureGuildLocked
			}
			for _, account := range economy.Users {
				if account == nil || account.Escrow <= 0 {
					continue // hand-edited {"users":{"someone":null}}; see topAssetsLocked
				}
				moveFromEscrowLocked(account)
				refunded++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return refunded, nil
}
