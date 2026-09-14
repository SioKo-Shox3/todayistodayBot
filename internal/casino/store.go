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

// jstMonth formats t's JST calendar month as "2006-01", the key the monthly
// season is identified by. The layout is chosen so that string order is
// calendar order — the same property jstDate relies on — which is what lets
// rolloverSeasonLocked decide "has the month moved FORWARD" with a plain <
// rather than by parsing two months back into time.Time.
func jstMonth(t time.Time) string { return t.In(jst).Format("2006-01") }

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
// interaction in this guild, and normalising the balances it hands back.
// Caller must already hold the Store's lock.
func ensureAccountLocked(economy *GuildEconomy, userID string) *UserAccount {
	if economy.Users[userID] == nil {
		economy.Users[userID] = &UserAccount{Chips: welcomeBonusChips}
	}
	account := economy.Users[userID]
	normalizeAccountLocked(account)
	return account
}

// normalizeAccountLocked is the account's normalisation point, the third of
// the same kind in this package: seedJackpotLocked does it for the pool and
// normalizeLotteryLocked for the lottery pot. After it returns, Chips and
// Escrow are in [0, MaxChips] and Coins is in [0, MaxCoins], so every sum
// and difference the balance arithmetic computes is inside the range
// types.go sized it for — whatever was on disk.
//
// It buys the headroom subtractions in particular. creditChipsCappedLocked
// computes MaxChips - Escrow - Chips, which on a hand-edited
// Chips = Escrow = math.MaxInt64 file wraps to a large POSITIVE number: the
// credit is then let through and lands the account on a NEGATIVE balance.
// The fix belongs here rather than in the subtraction, because bolting a
// guard onto each individual sum fixes the one expression it names and
// leaves the next one (totalAssetsLocked, the escrow moves) still wrong —
// the same argument normalizeLotteryLocked's comment makes at length.
//
// Clamping the MAGNITUDE means a hand-edited balance above the cap loses
// what does not fit, at the first write path that reads it. That is the
// deliberate trade: a number outside the cap is outside every invariant
// this package maintains, and reading it as-is corrupts accounts that were
// never edited (the pool, the ranking) rather than just the edited one.
func normalizeAccountLocked(account *UserAccount) {
	if account.Chips < 0 {
		account.Chips = 0
	} else if account.Chips > MaxChips {
		account.Chips = MaxChips
	}
	if account.Escrow < 0 {
		account.Escrow = 0
	} else if account.Escrow > MaxChips {
		account.Escrow = MaxChips
	}
	if account.Coins < 0 {
		account.Coins = 0
	} else if account.Coins > MaxCoins {
		account.Coins = MaxCoins
	}
	// SeasonNet is clamped at BOTH ends rather than floored at 0: it is a
	// signed 純利 (types.go), so a losing player's negative value is the
	// legitimate reading and flooring it would promote every loser to even.
	// The magnitude bound is what addSeasonNetLocked's arithmetic is sized
	// for, and putting it here — not only inside that helper — means the
	// season ranking and any future reader see a bounded number too, without
	// having to add a credit first.
	if account.SeasonNet > MaxChips {
		account.SeasonNet = MaxChips
	} else if account.SeasonNet < -MaxChips {
		account.SeasonNet = -MaxChips
	}
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
	var result DailyRate
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		result = ensureTodayRateLocked(economy, now, s.rng)
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
func ensureTodayRateLocked(economy *GuildEconomy, now time.Time, rng randSource) DailyRate {
	rate, _ := ensureTodayRateIndexLocked(economy, now, rng)
	return rate
}

// ensureTodayRateIndexLocked is ensureTodayRateLocked plus the position the
// returned rate occupies in economy.Rates, for the one caller (RecentRates)
// that has to show the history AROUND that entry. The index is -1 when no
// stored entry backs the rate — the synthesised 基準100 fallback below.
func ensureTodayRateIndexLocked(economy *GuildEconomy, now time.Time, rng randSource) (DailyRate, int) {
	// The rate rolls over at MIDNIGHT JST and the lottery is drawn at 09:00
	// JST, so the two need different dates off the same instant — which is
	// why this takes `now` rather than a pre-formatted "today".
	today := jstDate(now)
	// Seed the jackpot pool here as well as in Spin (設計書 C-3a §2) — on
	// behalf of both this and ensureTodayRateLocked. This is the read path
	// every display goes through (/balance, /rank, /rate, the 9am
	// announcement), so a guild whose members have not spun since the upgrade
	// would otherwise be shown a pool of 0 chips that is really 1,000.
	// Normalise the persisted lottery numbers BEFORE either the seeding or
	// the draw reads them (設計書 C-3a §8). Both of those compute with Sales
	// and Carryover, so a clamp applied afterwards would be a clamp applied
	// to a value that has already wrapped. Every command and the
	// announcement sweep reach the lottery through this function, so this
	// one call is the whole guard — there is no second read path to cover.
	normalizeLotteryLocked(economy)
	seedJackpotLocked(economy)
	// Roll the daily lottery over at the very same point, for the very same
	// reason the rate is generated here (設計書 C-3a §3 の取りこぼし防止): this
	// is the ONE place every command and the announcement sweep pass through,
	// so a draw hooked here happens whether or not the bot was alive at 09:00
	// JST and whether or not the guild has an announcement channel configured.
	// Hanging it off the scheduler instead would silently skip the draw for a
	// guild that never set a channel, and lose a day entirely after a restart.
	// Close the month BEFORE the lottery draw, not after. The switch zeroes
	// every account's SeasonNet, and drawLotteryLocked CREDITS SeasonNet to
	// the winner it names — so the other order would silently erase the
	// 純利 of the one draw that shares a call with a month boundary, on the
	// 1st of every month. Running first instead books that payout into the
	// month that has just opened, which is a defensible reading (the chips
	// land in the new month) and, unlike the erasure, is not a loss.
	rolloverSeasonLocked(economy, jstMonth(now))
	drawLotteryLocked(economy, lotteryDrawDate(now), rng)
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
	return s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		ensureTodayRateLocked(economy, now, s.rng)
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
// (9.223e18). Every credit path enforces those caps — but only for accounts
// a credit has touched, and this loop reads the map directly, so the bound
// holds on a hand-edited file only because each account goes through the
// normalisation point first. normalizeAccountLocked rather than
// ensureAccountLocked: clamping an entry that is already there is not the
// same as CREATING one, and creating one here would mint the 1,000-chip
// welcome bonus for someone who never played merely because a third party
// read the ranking (the same reason the nil case below is skipped).
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
		normalizeAccountLocked(account)
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

// SeasonTopLimit is how many rows /season's table shows (設計書 C-3b §5).
// It is unrelated to seasonRankLimit, which is how many places are PAID:
// the table is a display, the podium is money.
const SeasonTopLimit = 10

// SeasonView is one guild's running season as /season shows it: the table,
// the caller's own place in it, how long is left, and the season that
// closed before it.
//
// SelfRank is the caller's 1-based place in the FULL ranking, not in the
// truncated table, so an 11th-placed player is told "11位" rather than
// silently dropped. 0 means unranked — a 純利 of exactly 0, which
// SeasonRanks excludes on purpose (a guild is full of accounts that exist
// only because someone ran /balance).
type SeasonView struct {
	Month    string
	Ranks    []SeasonRank
	Self     SeasonRank
	SelfRank int
	Players  int
	DaysLeft int
	Last     *SeasonResult
}

// SeasonDaysLeft reports how many JST calendar days the season running at
// `now` still has, COUNTING TODAY: 1 on the last day of the month, 31 on the
// 1st of a 31-day month. Counting today is what makes "残り1日" mean 「今日で
// 終わり」 rather than 「もう終わっている」 — the season closes at the next
// midnight boundary (設計書 C-3b §4), so today is still playable.
//
// The month length is taken from the day before the 1st of the NEXT month
// rather than from a table, so February and leap years need no special case;
// time.Date normalises month 13 into January of the following year.
func SeasonDaysLeft(now time.Time) int {
	t := now.In(jst)
	lastOfMonth := time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, jst).AddDate(0, 0, -1)
	return lastOfMonth.Day() - t.Day() + 1
}

// seasonDaysLeftIn reports the days left in the season labelled `month`
// ("2006-01") when it is being read at `now` — the number that belongs
// BESIDE that month, which is not always SeasonDaysLeft(now).
//
// The two come apart at a month boundary, because they have different
// sources. The month a reader is shown is the PERSISTED SeasonMonth, which
// rolloverSeasonLocked only ever moves forward (a discipline it shares with
// LastAnnounced), while `now` is the instant its own caller read before
// taking the lock. Two /season invocations that straddle midnight can
// therefore reach the store in the reverse order of the instants they
// carry: the first opens 2026-08, and the second, holding 23:59:59 on
// 07-31, is handed the 8月 table and would count July's last day against
// it — 「8月・残り1日」, a month that has just begun reported as ending
// tonight.
//
// A lagging `now` is pulled up to the first midnight of `month`, so the
// answer is the one that month's own reader would get. It cannot lag the
// other way: the rollover runs inside this same Update, so SeasonMonth is
// never behind now's month by the time this is asked.
//
// An unparseable month (a hand-edited season_month) falls back to `now`
// rather than guessing a length — the same rule lotteryDrawDateLabel
// follows for a date it cannot read.
func seasonDaysLeftIn(month string, now time.Time) int {
	start, err := time.ParseInLocation("2006-01", month, jst)
	if err != nil {
		return SeasonDaysLeft(now)
	}
	if now.In(jst).Before(start) {
		return SeasonDaysLeft(start)
	}
	return SeasonDaysLeft(now)
}

// SeasonStatus returns guildID's season as userID sees it, opening the
// account (welcome bonus) and running the daily rollover first — the same
// entry every other read path uses. Going through the rollover is what stops
// /season on the 1st of a month from showing last month's table: the switch
// (rolloverSeasonLocked) is hooked into ensureTodayRateLocked, so reading
// without it would report a season that has already been paid out and
// zeroed by the next command to arrive.
//
// Every account is normalised on the way past, for topAssetsLocked's reason:
// SeasonRanks compares stored values, and a hand-edited 純利 outside
// [-MaxChips, MaxChips] would otherwise sit at the top of the table.
//
// The previous season is returned as a COPY, slice included: the value
// behind economy.LastSeason stays in the store's data, and handing a caller
// a pointer into it would let a display mutate the file's contents.
func (s *Store) SeasonStatus(guildID, userID string, now time.Time) (SeasonView, error) {
	var view SeasonView
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		ensureTodayRateLocked(economy, now, s.rng)
		account := ensureAccountLocked(economy, userID)
		for _, other := range economy.Users {
			if other == nil {
				continue // hand-edited {"users":{"someone":null}}; see topAssetsLocked
			}
			normalizeAccountLocked(other)
		}

		ranked := SeasonRanks(economy.Users, len(economy.Users))
		view = SeasonView{
			Month:   economy.SeasonMonth,
			Ranks:   ranked,
			Self:    SeasonRank{UserID: userID, Net: account.SeasonNet},
			Players: len(ranked),
			// Counted against the month being SHOWN, not against `now`:
			// see seasonDaysLeftIn for why the two can disagree.
			DaysLeft: seasonDaysLeftIn(economy.SeasonMonth, now),
		}
		for i, rank := range ranked {
			if rank.UserID == userID {
				view.SelfRank = i + 1
				break
			}
		}
		if len(view.Ranks) > SeasonTopLimit {
			view.Ranks = view.Ranks[:SeasonTopLimit]
		}
		if economy.LastSeason != nil {
			last := *economy.LastSeason
			last.Ranks = append([]SeasonRank(nil), economy.LastSeason.Ranks...)
			view.Last = &last
		}
		return nil
	})
	if err != nil {
		return SeasonView{}, err
	}
	return view, nil
}

// TopAssets returns guildID's top `limit` accounts by total assets, using
// today's rate (generated first if missing, same transaction).
func (s *Store) TopAssets(guildID string, now time.Time, limit int) ([]RankEntry, error) {
	var result []RankEntry
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		rate := ensureTodayRateLocked(economy, now, s.rng)
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
	var result ExchangeResult
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		rate := ensureTodayRateLocked(economy, now, s.rng)
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
	var result ExchangeResult
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		rate := ensureTodayRateLocked(economy, now, s.rng)
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
	var result AccountView
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		rate := ensureTodayRateLocked(economy, now, s.rng)
		account := *ensureAccountLocked(economy, userID)
		result = AccountView{Account: account, RateUsed: rate.Rate, TotalAssets: totalAssetsLocked(&account, rate.Rate)}
		return nil
	})
	return result, err
}

// --- the slot jackpot pool (設計書 C-3a §2) -------------------------------
//
// The pool is HOUSE money, not anyone's balance: it is funded from the edge
// the payout table already keeps, so it takes part in no account's
// Chips + Escrow conservation law. Its own law, which store_test.go pins
// down, is that economy.Jackpot moves ONLY by a credit through
// creditJackpotCappedLocked — the slot accrual (accrueJackpotLocked), the
// lottery house cut and the prize a capped winner could not take
// (drawLotteryLocked) — or by a 7️⃣7️⃣7️⃣ firing in Spin, which pays the pool
// out and resets it to JackpotSeed. Nothing else in this package writes it,
// and the one credit helper is what bounds it to [JackpotSeed, MaxJackpot].

// seedJackpotLocked is the pool's normalisation point: after it returns,
// economy.Jackpot is in [JackpotSeed, MaxJackpot] no matter what was on
// disk. Every writer below depends on that range, so every writer calls it
// first.
//
// It raises a pool below JackpotSeed back up to the seed, so no reader ever
// sees an unseeded 0. Every pre-C-3a data/casino.json unmarshals to
// Jackpot 0, and that is the case this exists for; the comparison is `<`
// rather than `== 0` because no legitimate sequence can produce anything in
// between — the pool is seeded at JackpotSeed, only ever grows, and is reset
// to JackpotSeed when it fires — so a value in (0, seed) can only come from a
// hand edit or a partial write, and repairing it here is the same discipline
// ensureTodayRateIndexLocked applies to a corrupt rate.
//
// It also truncates a pool ABOVE MaxJackpot back down to the cap. No
// legitimate sequence reaches that either, since creditJackpotCappedLocked
// never lets a credit cross it; the case that matters is the hand-edited or
// half-written file holding something like math.MaxInt64, which would make
// the very next credit wrap. Clamping on the way in means the arithmetic
// below never sees such a value at all.
//
// The carry is normalised at the same time, into [0, jackpotAccumScale) —
// the range accrueJackpotLocked leaves behind and the only range a
// legitimate sequence ever persists. A NEGATIVE JackpotAccum (again, only
// reachable by hand edit) would make accrueJackpotLocked's division hand
// back a negative contribution and SHRINK the pool, since Go's / and % both
// truncate towards zero. One at or above the scale is the mirror case: a
// file holding math.MaxInt64 wraps `JackpotAccum += bet*percent` to a
// negative carry, and the accrual that follows then feeds the pool nothing
// for as many spins as it takes to climb back — the same overflow the pool's
// own clamp above rules out, one field over. Both are repaired here so the
// arithmetic downstream never sees them. Caller must already hold the
// Store's lock.
func seedJackpotLocked(economy *GuildEconomy) {
	if economy.Jackpot < JackpotSeed {
		economy.Jackpot = JackpotSeed
	}
	if economy.Jackpot > MaxJackpot {
		economy.Jackpot = MaxJackpot
	}
	if economy.JackpotAccum < 0 {
		economy.JackpotAccum = 0
	} else if economy.JackpotAccum >= jackpotAccumScale {
		// Modulo rather than 0: the sub-chip part of a corrupt carry is
		// harmless and keeping it makes this a repair of the one thing that
		// is actually out of range.
		economy.JackpotAccum %= jackpotAccumScale
	}
}

// creditJackpotCappedLocked is the ONLY way chips enter the pool. It seeds
// first, then credits `amount` as far as MaxJackpot allows and reports what
// it actually moved.
//
// What does not fit is DROPPED, deliberately, and this is the one place
// C-3a's conservation law gives ground: the pool's cap is the only leak in
// the system, and everywhere else nothing is destroyed. That is the same
// contract creditChipsCappedLocked already has at MaxChips — a cap you
// cannot exceed is worth more than a chip you cannot lose, because a wrapped
// pool is negative money that the next 7️⃣7️⃣7️⃣ hands to a player.
//
// A non-positive amount moves nothing rather than shrinking the pool: the
// callers' addends are differences (house + (prize - paid)) computed from
// values that a hand-edited file can push negative, and a draw must never be
// able to drain the pool. Caller must already hold the Store's lock.
func creditJackpotCappedLocked(economy *GuildEconomy, amount int64) int64 {
	seedJackpotLocked(economy)
	if amount <= 0 {
		return 0
	}
	headroom := MaxJackpot - economy.Jackpot // in [0, MaxJackpot-JackpotSeed] after the seed
	if headroom <= 0 {
		return 0
	}
	if amount > headroom {
		amount = headroom
	}
	economy.Jackpot += amount
	return amount
}

// accrueJackpotLocked seeds the pool if needed, then feeds it this spin's
// JackpotContributionPercent of bet. The contribution is accumulated in
// 1/100-chip units and only whole chips are moved into the pool, so the
// minimum bet (10 chips → 0.2 chips per spin) accrues in full across five
// spins instead of truncating to nothing every time. Called from inside
// Spin's Update, before the reels are resolved: a 7️⃣7️⃣7️⃣ wins the pool
// INCLUDING its own contribution (設計書 §2's 積立 → 抽選 → 配当 order).
// Caller must already hold the Store's lock.
func accrueJackpotLocked(economy *GuildEconomy, bet int64) {
	seedJackpotLocked(economy)
	economy.JackpotAccum += bet * JackpotContributionPercent
	// Whole chips are taken out of the carry BEFORE the credit, not after:
	// creditJackpotCappedLocked seeds on the way in, and the seed now
	// normalises the carry too, so leaving the subtraction until afterwards
	// would make this line's result depend on what that nested seed did to
	// the field in between.
	whole := economy.JackpotAccum / jackpotAccumScale
	economy.JackpotAccum %= jackpotAccumScale
	// The carry is spent either way: at the cap the whole chips it converted
	// to are dropped rather than held back, so a full pool does not silently
	// bank an accrual that the next 7️⃣7️⃣7️⃣ reset would release all at once.
	creditJackpotCappedLocked(economy, whole)
}

// Spin atomically deducts bet chips, accrues the jackpot pool, resolves the
// spin (via the pure `spin`, using s.rng), credits any payout — the pool
// included when the reels are 7️⃣7️⃣7️⃣ — and persists, all in one Update
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
		s.ensureSeasonMonthLocked(economy)
		account := ensureAccountLocked(economy, userID)
		if account.Chips < bet {
			return &ErrInsufficientChips{Balance: account.Chips}
		}
		account.Chips -= bet
		accrueJackpotLocked(economy, bet)
		result = spin(bet, s.rng)
		if result.Reels == jackpotReels {
			result.JackpotWon = economy.Jackpot
			result.Payout += economy.Jackpot
			economy.Jackpot = JackpotSeed
		}
		result.JackpotPool = economy.Jackpot
		if err := creditChipsLocked(account, result.Payout); err != nil {
			return err // aborts: the deduction AND the accrual above are discarded with the transaction
		}
		// The 純利 is booked only after the credit has actually landed.
		// creditChipsLocked is all-or-nothing, so on the path that reaches
		// here the credited amount IS result.Payout — 設計書 C-3b §3's
		// "実際に口座へ入った額" and the owed amount coincide for the slot.
		addSeasonNetLocked(account, result.Payout-bet)
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
	var result []DailyRate
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		rate, idx := ensureTodayRateIndexLocked(economy, now, s.rng)
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
	Payout int64 // what ACTUALLY landed, stake included (0 on a loss)
	Owed   int64 // what the settlement owed; above Payout only when MaxChips kept the rest out
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
// ErrInvalidAmount rejects a negative payout — on any error nothing at all is
// persisted, so the escrow survives for a retry rather than vanishing.
//
// A payout that does not fit under MaxChips is NOT an error. The credit goes
// through creditChipsCappedLocked: the account takes what fits and the rest is
// dropped (設計書 C-3b §2, the same rule as the season bonus and the duel pot).
// Refusing would abort a transaction that has already closed an elapsed season
// above — and the monthly bonus paid by that very rollover is what fills the
// account to the cap, so the retry rebuilds the state that refused it and the
// game can never be settled again, escrow and all. A cap is a property of
// where the chips are going, not a reason to leave a game open.
//
// SettleResult.Owed keeps the owed figure so a caller can tell the two apart;
// Payout is what the player really received, which is what the boards print
// and what 純利 is booked from (§2: 「実際に口座へ入った額」).
func (s *Store) SettleGame(guildID, userID string, payout int64) (SettleResult, error) {
	var result SettleResult
	err := s.Update(func(d *Data) error {
		if payout < 0 {
			return ErrInvalidAmount
		}
		economy := ensureGuildLocked(d, guildID)
		s.ensureSeasonMonthLocked(economy)
		account := ensureAccountLocked(economy, userID)
		if account.Escrow == 0 {
			return ErrNoGameInProgress
		}
		// The escrow is read BEFORE it is cleared because it is the only
		// record of what this game was staked for: SettleGame is told the
		// payout and never the bet, and by the time the credit lands the
		// stake is gone. It is the bet plus every ダブル raise, which is
		// exactly 設計書 C-3b §3's 「賭けた額」 for a hand that doubled.
		staked := account.Escrow
		clearEscrowLocked(account)
		// The escrow is released FIRST so the cap is measured against the
		// headroom the stake leaves behind: a push (payout == staked) always
		// fits, and a player at the cap gets their own chips back rather than
		// having them counted twice.
		credited := creditChipsCappedLocked(account, payout)
		addSeasonNetLocked(account, credited-staked)
		result = SettleResult{Chips: account.Chips, Payout: credited, Owed: payout}
		return nil
	})
	if err != nil {
		return SettleResult{}, err
	}
	return result, nil
}

// addSeasonNetLocked adds delta to the account's running 純利, saturating at
// ±MaxChips. Caller must already hold the Store's lock.
//
// It clamps the CURRENT value before adding, not just the result, because
// the current value comes off disk unbounded: a hand-edited season_net near
// math.MaxInt64 would wrap the addition itself, and a wrapped 純利 puts the
// worst player at the top of the season board. Clamping both ends means the
// widest intermediate here is MaxChips + 2*MaxChips = 3e12, 3e6x below
// math.MaxInt64.
//
// Saturating rather than refusing is right because SeasonNet is not
// currency (types.go): losing precision at ±1e12 costs a rank ordering
// nobody can reach by playing, while refusing would abort a settlement that
// has already moved real chips.
func addSeasonNetLocked(account *UserAccount, delta int64) {
	net := account.SeasonNet
	if net > MaxChips {
		net = MaxChips
	} else if net < -MaxChips {
		net = -MaxChips
	}
	net += delta
	if net > MaxChips {
		net = MaxChips
	} else if net < -MaxChips {
		net = -MaxChips
	}
	account.SeasonNet = net
}

// ensureSeasonMonthLocked closes an elapsed season before the caller books a
// game's 純利. The rollover's home is ensureTodayRateIndexLocked, the read
// path every DISPLAY passes through — but a settlement is not a read:
// SettleGame, Spin and AcceptDuel take no `now` from the caller and reach
// addSeasonNetLocked without ever touching that path. A game whose result
// lands in the first minutes of a new month would otherwise be added to the
// month that has already ended, and the next display would then close that
// month WITH the foreign result on the podium and open the new one at zero.
//
// It calls rolloverSeasonLocked alone rather than the whole daily rollover:
// the rate history and the lottery draw are the display's business and want
// the caller's instant, while the month a result is booked into is decided
// by the clock at the moment the chips move — s.nowLocked(), read under the
// lock like s.rng. Being idempotent and forward-only (see below), running it
// here as well as on the read path costs nothing on the days it has nothing
// to do.
//
// Call it BEFORE the credit, never after: the switch zeroes every account's
// SeasonNet, so the other order erases the very result being booked.
//
// Caller must already hold the Store's lock (call only from inside an
// Update closure).
func (s *Store) ensureSeasonMonthLocked(economy *GuildEconomy) {
	rolloverSeasonLocked(economy, jstMonth(s.nowLocked()))
}

// rolloverSeasonLocked closes the running season when the JST month has
// moved on, and opens `month` (設計書 C-3b §4). It is called from
// ensureTodayRateIndexLocked, i.e. from inside every caller's own Update, so
// the prizes, the zeroed 純利 and the new SeasonMonth are committed by a
// single write — a crash cannot leave a guild that has paid out last month's
// podium and still thinks it is last month, which on the next command would
// pay the same three people again.
//
// Hanging it off the daily rollover rather than off the 9am scheduler is
// what makes it unmissable: every command reaches it, so a bot that was down
// through the whole of the 1st still switches on the first command after it
// comes back, and a guild that never configured an announcement channel
// switches at all. The boundary is midnight rather than 09:00 for the same
// reason the rate's is — 「月初の 0 時から新シーズン」 is the rule a player
// can predict.
//
// The comparison is `SeasonMonth < month`, FORWARD ONLY, the discipline
// C3-19 settled for LastAnnounced and drawLotteryLocked's DrawDate: a clock
// that moves backwards (an NTP correction, a hand-set host clock, a host
// whose zone data is fixed at boot) must not be able to close a month that
// has already paid out. With a `!=` the sequence Oct→Sep→Oct closes the
// season three times and mints three sets of prizes, and the second of those
// closes a September whose 純利 was zeroed by the first — i.e. pays the
// podium to whoever happened to play in the gap. Months are "2006-01", so
// string order is calendar order.
//
// An unopened guild (SeasonMonth "" — every guild in a pre-C-3b
// data/casino.json) only OPENS the month: there is no previous season to
// rank, and paying out here would hand prizes for 純利 accumulated before
// any season existed.
//
// Caller must already hold the Store's lock.
func rolloverSeasonLocked(economy *GuildEconomy, month string) {
	// BEFORE the early returns, and therefore before the overwrite below: an
	// upgrading file's one pending result has to reach the queue on the pass
	// that reads it, whatever that pass then decides about the month. A bot
	// that was down across the 1st comes back to a single call that both
	// migrates June and closes July, in that order.
	migrateUnannouncedSeasonsLocked(economy)
	if month == "" || economy.SeasonMonth >= month {
		return
	}
	if economy.SeasonMonth == "" {
		economy.SeasonMonth = month
		return
	}
	// Normalise BEFORE ranking, not after: the Net values are copied straight
	// into the persisted SeasonResult, and this loop reads the map directly
	// rather than through ensureAccountLocked, so a hand-edited 純利 of
	// math.MaxInt64 would otherwise be written back into the file as the
	// podium's record of the month. normalizeAccountLocked rather than
	// ensureAccountLocked, and nil skipped rather than repaired, for
	// topAssetsLocked's reason — opening an account here would mint the
	// welcome bonus for someone who never played.
	for _, account := range economy.Users {
		if account == nil {
			continue
		}
		normalizeAccountLocked(account)
	}
	ranks := SeasonRanks(economy.Users, len(economy.Users))
	// Players is counted from the FULL ranking, before the podium truncates
	// it: 「何人が遊んだか」 is not 「表彰台は何人か」, and the podium caps at
	// three (types.go).
	players := len(ranks)
	if len(ranks) > seasonRankLimit {
		ranks = ranks[:seasonRankLimit]
	}
	for i := range ranks {
		account := economy.Users[ranks[i].UserID]
		if account == nil {
			continue // unreachable: SeasonRanks skips nil entries
		}
		// The prize is capped, not refused, and Bonus records what actually
		// landed (設計書 C-3b §2). A winner already at MaxChips takes only what
		// fits and the remainder is DESTROYED rather than sent to the jackpot
		// pool the way a lottery overflow is: the lottery's prize was somebody
		// else's chips and has to go somewhere, while this one was minted a
		// line ago and never existed until it was credited.
		//
		// The credit deliberately does NOT go through addSeasonNetLocked: §3
		// excludes the season's own prize from 純利, so that winning a month
		// cannot, by itself, win the next one. The zeroing below would hide a
		// mistake here — which is why the exclusion is stated rather than left
		// to that accident.
		ranks[i].Bonus = creditChipsCappedLocked(account, SeasonBonus(i+1))
	}
	result := SeasonResult{Month: economy.SeasonMonth, Ranks: ranks, Players: players}
	economy.LastSeason = &result
	queueClosedSeasonLocked(economy, result)
	// Zero EVERY account, not just the ones that ranked: a new season starts
	// from a clean board for everybody, and leaving a non-ranked account's
	// 純利 behind would carry last month's losses into this month's ranking.
	for _, account := range economy.Users {
		if account == nil {
			continue
		}
		account.SeasonNet = 0
	}
	economy.SeasonMonth = month
}

// seasonUnannouncedLimit caps GuildEconomy.UnannouncedSeasons. Three is a
// quarter of outage — far longer than any believable one — and, unlike the
// lottery's seven days, each entry here @mentions a podium: posting a stack
// of them at once is itself a cost, so the queue is deliberately short.
const seasonUnannouncedLimit = 3

// queueClosedSeasonLocked puts a closed season in line for the 9am posting.
//
// A result with an EMPTY podium is not queued: nobody placed, so there is
// nothing to celebrate and nobody to ping (internal/commands renders it as
// the empty string and sends nothing). Queueing it anyway would spend one of
// the three slots every quiet month and could push a real podium off the
// front of the queue — the one entry that had to survive.
//
// The entry is a COPY down to its Ranks slice, the discipline
// migrateUnannouncedLocked follows: LastSeason and the queue entry are two
// independent records of one month, and only the latter is retired by a
// confirmed send.
// Caller must already hold the Store's lock.
func queueClosedSeasonLocked(economy *GuildEconomy, result SeasonResult) {
	if len(result.Ranks) == 0 {
		return
	}
	result.Ranks = append([]SeasonRank(nil), result.Ranks...)
	economy.UnannouncedSeasons = append(economy.UnannouncedSeasons, result)
	if n := len(economy.UnannouncedSeasons); n > seasonUnannouncedLimit {
		// Drop from the FRONT, for the reason drawLotteryLocked does: the
		// oldest podium is the one least worth posting months late. Copying
		// into the same backing array is safe because the destination index
		// is always lower than the source.
		economy.UnannouncedSeasons = append(economy.UnannouncedSeasons[:0], economy.UnannouncedSeasons[n-seasonUnannouncedLimit:]...)
	}
}

// migrateUnannouncedSeasonsLocked lifts a pre-C3B-08 file's one pending
// result into the announcement queue. Before C3B-08 the 9am posting read
// LastSeason directly; the queue replaced it, and collectDailyAnnouncements
// now reads nothing else. A casino.json written by the older build therefore
// holds a closed season in LastSeason and no "unannounced_seasons" key at
// all — and if the upgrade landed between the close and the next 09:00, that
// podium would never be posted by this pass or any later one.
//
// It runs from the TOP of rolloverSeasonLocked, i.e. before the very
// overwrite this task exists to survive, and every command and the
// announcement sweep reach it — so the old result is queued whether the bot
// comes back on the 31st or the 1st.
//
// "Not posted yet" is LastSeason.Month > LastSeasonAnnounced, exactly the
// test the queue replaced. A file that already HAS a queue is a post-C3B-08
// file and is left alone: there the queue is authoritative, and re-deriving
// an entry from LastSeason would repost a month MarkSeasonAnnounced has
// retired (LastSeason deliberately survives that retirement, so /season
// keeps showing the last real result). That empty-queue guard is also what
// makes this idempotent — the entry it migrates is itself the reason the
// next pass does nothing. A result with no podium is not queued for
// queueClosedSeasonLocked's reason.
//
// AnnounceChannelID is deliberately NOT part of this test, as in
// migrateUnannouncedLocked: whether a result is worth KEEPING and whether
// there is somewhere to post it today are two different questions, and only
// collectDailyAnnouncements answers the second.
// Caller must already hold the Store's lock.
func migrateUnannouncedSeasonsLocked(economy *GuildEconomy) {
	if len(economy.UnannouncedSeasons) > 0 {
		return
	}
	last := economy.LastSeason
	if last == nil || last.Month <= economy.LastSeasonAnnounced {
		return
	}
	queueClosedSeasonLocked(economy, *last)
}

// DuelSettlement is what AcceptDuel left behind — everything
// internal/commands/duel.go needs to render the result without a second
// store round-trip, in the "persist first, display second" order C-2
// established.
//
// The two payout fields are what was ACTUALLY CREDITED, which can be less
// than DuelPayout returned when the winner was already at MaxChips. Render
// these, never a recomputed 2 × bet, or the message will claim chips the
// balance beside it does not show.
type DuelSettlement struct {
	ChallengerWins   bool
	WinnerID         string
	ChallengerPayout int64 // credited to the challenger (0 on a loss)
	OpponentPayout   int64 // credited to the opponent (0 on a loss)
	ChallengerChips  int64 // the challenger's balance after the settlement
	OpponentChips    int64 // the opponent's balance after the settlement
}

// AcceptDuel stakes the opponent and settles the whole duel in ONE Update
// (設計書 C-3b §4.5). challengerWins comes from FlipDuel, drawn by the
// caller — the toss is injected rather than performed here so the settlement
// is deterministic under test and so the store keeps no randomness of its
// own beyond the daily-rate generator.
//
// One transaction is a requirement, not a convenience. Staking the opponent
// and paying the winner through two separate Updates would leave a window in
// which the opponent's chips are in escrow with no board that can release
// them: a crash there strands the stake until the next startup refund, and a
// concurrent second acceptance slips between the two halves and pays the pot
// twice. Everything below runs under the single writer's lock, so the duel
// either happens completely or not at all.
//
// Refusals and who they protect:
//   - ErrDuelSelf — both sides are the same account (see errors.go).
//   - ErrInvalidAmount — a bet outside [1, MaxChips]. The command layer's
//     10..1,000 option bounds are a UX nicety, not a guarantee.
//   - ErrNoGameInProgress — the challenger is not holding a duel stake. This
//     is the double-settlement guard, and it is the reason the FIRST thing
//     an acceptance does is zero that escrow: two buttons pressed in the same
//     instant resolve on two goroutines, and the second one finds an escrow
//     of 0 and pays nothing. EscrowGame is checked alongside the amount so a
//     stale duel button cannot settle a blackjack hand the challenger opened
//     afterwards.
//   - ErrDuelStakeMismatch — the challenger's stake is not this bet, so the
//     transfer would not be zero-sum (see errors.go).
//   - ErrGameInProgress / *ErrInsufficientChips — the OPPONENT cannot cover
//     the bet or is mid-game. 設計書 §4.5 is explicit that this does NOT
//     refund the challenger: nothing at all is persisted, so the challenge
//     stays live and the opponent can try again, decline, or let it expire.
//
// The settlement itself is zero-sum by construction: 2 × bet is taken out of
// the two escrows and 2 × bet is credited to the winner. The one exception
// is a winner already at MaxChips, who takes only what fits — the same
// truncation every other payout in this package makes, and the reason
// SeasonNet is fed the credited amount rather than the owed one (§2).
func (s *Store) AcceptDuel(guildID, challengerID, opponentID string, bet int64, challengerWins bool) (DuelSettlement, error) {
	var result DuelSettlement
	err := s.Update(func(d *Data) error {
		if challengerID == opponentID {
			return ErrDuelSelf
		}
		if bet < 1 || bet > MaxChips {
			return ErrInvalidAmount
		}
		economy := ensureGuildLocked(d, guildID)
		s.ensureSeasonMonthLocked(economy)
		challenger := ensureAccountLocked(economy, challengerID)
		opponent := ensureAccountLocked(economy, opponentID)

		if challenger.Escrow == 0 || challenger.EscrowGame != string(GameDuel) {
			return ErrNoGameInProgress
		}
		if challenger.Escrow != bet {
			return ErrDuelStakeMismatch
		}
		if opponent.Escrow > 0 {
			return ErrGameInProgress
		}
		// Stake the opponent through the same checked move every other game
		// uses, so an opponent who cannot cover the bet is refused with the
		// balance-carrying *ErrInsufficientChips and nothing is persisted.
		// The escrow is released again three lines down; it exists for the
		// width of this closure so that the debit is never written in a form
		// that could outlive a failure below it.
		if err := moveToEscrowLocked(opponent, bet); err != nil {
			return err
		}
		opponent.EscrowGame = string(GameDuel)

		challengerPayout, opponentPayout := DuelPayout(bet, challengerWins)
		clearEscrowLocked(challenger)
		clearEscrowLocked(opponent)
		// Both escrows are zero before either credit, so each account's
		// headroom (MaxChips - Escrow - Chips) is measured against chips the
		// account actually still holds rather than against its own spent
		// stake.
		challengerCredited := creditChipsCappedLocked(challenger, challengerPayout)
		opponentCredited := creditChipsCappedLocked(opponent, opponentPayout)
		addSeasonNetLocked(challenger, challengerCredited-bet)
		addSeasonNetLocked(opponent, opponentCredited-bet)

		winnerID := opponentID
		if challengerWins {
			winnerID = challengerID
		}
		result = DuelSettlement{
			ChallengerWins:   challengerWins,
			WinnerID:         winnerID,
			ChallengerPayout: challengerCredited,
			OpponentPayout:   opponentCredited,
			ChallengerChips:  challenger.Chips,
			OpponentChips:    opponent.Chips,
		}
		return nil
	})
	if err != nil {
		return DuelSettlement{}, err
	}
	return result, nil
}

// DeclineDuel returns the challenger's stake and closes the challenge — the
// 🚫 button and the 3-minute sweeper both land here (設計書 §4.5). The
// opponent never staked anything on a challenge that was not accepted, so
// nothing of theirs is touched and no account of theirs is opened.
//
// 設計書 §4.5 words this as SettleGame(payout = bet); the refund is written
// as moveFromEscrowLocked instead, which is the same outcome by a route that
// cannot fail. A credit is capped at MaxChips, so a challenger whose balance
// was hand-edited over the cap would have the excess DESTROYED by taking
// their own stake back — and a refusal (ErrChipCapExceeded) would strand the
// stake in escrow forever, locking the account out of every game. The move
// is the exact inverse of the stake, so the sum Chips + Escrow lands exactly
// where it started. That is the same reasoning, for the same reason, as
// RefundStaleEscrows below.
//
// SeasonNet is untouched: a declined challenge is not a game result, and §3
// counts only 勝敗.
//
// Returns ErrNoGameInProgress when the challenger holds no duel stake, which
// is both halves of the double-refund guard: a 🚫 pressed twice, and — the
// case that actually moves chips — a stale duel button pressed after the
// challenger has opened some other game, which without the EscrowGame check
// would refund that game's stake out from under its own board.
func (s *Store) DeclineDuel(guildID, challengerID string) error {
	return s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		account := ensureAccountLocked(economy, challengerID)
		if account.Escrow == 0 || account.EscrowGame != string(GameDuel) {
			return ErrNoGameInProgress
		}
		moveFromEscrowLocked(account)
		return nil
	})
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
//
// Preserving the sum is exactly why each refunded account goes through
// ensureAccountLocked first. The move is `Chips += Escrow`, which on a
// hand-edited Chips = Escrow = math.MaxInt64 file WRAPS to -2 and persists
// it; the next read normalises that to zero, so the one operation forbidden
// to destroy chips destroys all of them. Normalising the addends first is
// the same fix, at the same point, that the headroom subtractions get —
// which is what makes "every path that touches an account goes through
// ensureAccountLocked" true of the write paths without exception.
func (s *Store) RefundStaleEscrows(now time.Time) (int, error) {
	refunded := 0
	err := s.Update(func(d *Data) error {
		refunded = 0 // never double-count if the closure is ever run more than once
		for _, economy := range *d {
			if economy == nil {
				continue // hand-edited {"g": null}; see ensureGuildLocked
			}
			for userID, account := range economy.Users {
				if account == nil || account.Escrow <= 0 {
					continue // hand-edited {"users":{"someone":null}}; see topAssetsLocked
				}
				// Through the normalisation point before the addition, even
				// though the account is already non-nil: this is a write path
				// that adds Escrow into Chips, and only ensureAccountLocked
				// makes those two addends small enough to sum.
				account = ensureAccountLocked(economy, userID)
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

// --- the daily lottery (設計書 C-3a §3) -----------------------------------
//
// The pot is PLAYERS' money in transit, unlike the jackpot pool: chips leave
// the buyers' balances at purchase and come back to exactly one of them at
// the next 09:00 JST rollover, minus the house's 10% — which does not vanish
// either, it is handed to the jackpot pool. The conservation law
// store_test.go pins down is therefore, across one draw:
//
//	sum(Chips lost by buyers) == Sales
//	winner's Chips gained     == prize   (== floor(Sales*90/100) + carryover)
//	economy.Jackpot gained    == house   (== Sales - floor(Sales*90/100))
//
// The third line holds while the pool has room, which is the only qualifier
// the law carries: what MaxJackpot cannot fit is dropped by
// creditJackpotCappedLocked. So the law is not "chips never disappear" but
// "chips disappear ONLY where the pool's cap refuses them" — everywhere
// else, including the remainder below, they are conserved exactly.
//
// creditChipsCappedLocked credits `amount` chips as far as MaxChips allows
// and reports what it actually moved. It exists for the one credit in this
// package that has no error to return: the draw runs inside
// ensureTodayRateIndexLocked, a rollover that every read path depends on and
// that cannot fail. Refusing the payout outright (creditChipsLocked's
// behaviour) would either destroy the prize or stall the rollover forever on
// a winner sitting at the cap; crediting what fits and letting the caller
// send the rest to the jackpot pool keeps every chip in play. Caller must
// already hold the Store's lock.
func creditChipsCappedLocked(account *UserAccount, amount int64) int64 {
	if amount <= 0 || account.Chips < 0 || account.Escrow < 0 {
		return 0
	}
	headroom := MaxChips - account.Escrow - account.Chips
	if headroom <= 0 {
		return 0 // already at or over the cap (hand-edited file)
	}
	if amount > headroom {
		amount = headroom
	}
	account.Chips += amount
	return amount
}

// normalizeLotteryLocked is the lottery's normalisation point, the exact
// counterpart of seedJackpotLocked for the pool: after it returns, every
// persisted number in `lottery` is inside the range the arithmetic below
// was sized for, no matter what was on disk. 設計書 C-3a §8 states the rule
// this implements — a persisted number is pulled back into its legal range
// at the point it is READ, and every later computation stays inside that
// range.
//
// It exists because the alternative does not converge. Bolting an overflow
// check onto each individual addition fixes the one variable it names and
// moves the failure to the next one: guarding the pool's credit left the
// house cut exposed, guarding that left the remainder exposed, and the
// remainder left Carryover exposed — a file holding Carryover near
// math.MaxInt64 wrapped LotteryPrize's `share + carryover` to a negative
// prize, and both the prize AND the house cut then vanished while the pool
// still had room. One clamp at the read point closes all of them at once,
// and closes the ones nobody has thought of yet.
//
// Sales and Carryover are pinned to [0, MaxChips]. Negative is the case
// that corrupts arithmetic (Go's / truncates toward zero, so a negative
// sales hands back a house cut that SHRINKS the pool); MaxChips is the same
// ceiling every account balance obeys.
//
// The ceiling half repairs the FILE and nothing else. A value above MaxChips
// can only be a hand edit or a partial write, because drawLotteryLocked —
// the sole writer of Carryover, and the only path that can produce a number
// that large — splits the excess off and sends it to the jackpot pool before
// it persists anything. That division of labour is the rule 設計書 C-3a §8
// states: a truncation may delete a number the file brought in, never one
// this package computed. Reaching this clamp with our own number would be a
// bug in the writer, not a repair here.
//
// Tickets entries are pinned to [1, LotteryMaxTicketsPerDraw], the range
// BuyLotteryTickets already enforces: a non-positive holding bought nothing
// so it is deleted outright, and an oversized one is trimmed to the cap so
// PickLotteryWinner's weighted walk cannot be handed a total that wraps. An
// emptied map goes back to nil so it serialises as the zero Lottery does.
//
// It takes the whole GuildEconomy rather than the Lottery because the last
// repair it performs — migrateUnannouncedLocked, a file from an older writer
// fixed at the point it is read, the same kind of thing as the clamps — has
// to compare a draw's date against economy.LastAnnounced, which lives one
// level up from the pot.
// Caller must already hold the Store's lock.
func normalizeLotteryLocked(economy *GuildEconomy) {
	lottery := &economy.Lottery
	if lottery.Sales < 0 {
		lottery.Sales = 0
	} else if lottery.Sales > MaxChips {
		lottery.Sales = MaxChips
	}
	if lottery.Carryover < 0 {
		lottery.Carryover = 0
	} else if lottery.Carryover > MaxChips {
		lottery.Carryover = MaxChips
	}
	// Deleting during a range over a map is defined in Go: an entry removed
	// before it is reached is simply not produced.
	for id, n := range lottery.Tickets {
		switch {
		case n <= 0:
			delete(lottery.Tickets, id)
		case n > LotteryMaxTicketsPerDraw:
			lottery.Tickets[id] = LotteryMaxTicketsPerDraw
		}
	}
	if len(lottery.Tickets) == 0 {
		lottery.Tickets = nil
	}
	migrateUnannouncedLocked(economy)
}

// migrateUnannouncedLocked lifts a pre-C3-12 file's one pending result into
// the announcement queue. Before C3-12 a settled draw lived only in LastDraw
// and the 9am posting read THAT; the queue replaced it, and
// collectDailyAnnouncements now reads nothing else. A casino.json written by
// the older build therefore holds a winner in LastDraw and no "unannounced"
// key at all — and if the upgrade landed between the draw and the next 09:00,
// that winner would never be posted or celebrated, by this pass or any later
// one.
//
// It runs from normalizeLotteryLocked, i.e. at the READ point, for the reason
// 設計書 C-3a §8 gives for every other repair here: every command and the
// announcement sweep reach the lottery through ensureTodayRateIndexLocked,
// and normalisation happens there BEFORE drawLotteryLocked — so the old
// result is queued before today's draw can overwrite LastDraw.
//
// "Not posted yet" is LastDraw.Date > LastAnnounced, which is exactly the
// test the queue replaced. A file that already HAS a queue is a post-C3-12
// file and is left alone: there the queue is authoritative, and re-deriving
// an entry from LastDraw would repost a draw MarkAnnounced has retired
// (LastDraw deliberately survives that retirement, so /lottery status keeps
// showing the last real result). The same empty-queue guard is what makes
// this idempotent — the entry it migrates is itself the reason the next pass
// does nothing. A draw with no WinnerID is not a result and has nothing to
// celebrate, and a dateless one (only a hand edit produces it) fails the
// comparison, so neither reaches the queue.
//
// AnnounceChannelID is deliberately NOT part of this test, the same way
// drawLotteryLocked ignores it: whether a draw is WORTH KEEPING and whether
// there is somewhere to post it today are two different questions, and only
// collectDailyAnnouncements gets to answer the second. Gating the migration
// on a configured channel loses the result outright — the window is only
// open until the next draw, and drawLotteryLocked overwrites LastDraw — so a
// guild that configures its channel a day later would find the winner gone,
// which is precisely the failure this migration exists to prevent.
// Caller must already hold the Store's lock.
func migrateUnannouncedLocked(economy *GuildEconomy) {
	lottery := &economy.Lottery
	if len(lottery.Unannounced) > 0 {
		return
	}
	last := lottery.LastDraw
	if last == nil || last.WinnerID == "" || last.Date <= economy.LastAnnounced {
		return
	}
	// Copied, not aliased: LastDraw and the queue entry are two independent
	// records of one draw, and MarkAnnounced retires only the latter.
	lottery.Unannounced = append(lottery.Unannounced, *last)
}

// lotterySummaryLocked totals the pot currently being sold into: tickets
// sold, distinct buyers, and what the next draw would pay. Caller must
// already hold the Store's lock.
func lotterySummaryLocked(lottery *Lottery) (sold, buyers int, prize int64) {
	for _, n := range lottery.Tickets {
		if n <= 0 {
			continue // hand-edited junk: it bought nothing, so it counts as nothing
		}
		sold += n
		buyers++
	}
	prize, _ = LotteryPrize(lottery.Sales, lottery.Carryover)
	return sold, buyers, prize
}

// drawLotteryLocked runs the 09:00 JST draw if `today` is a day the lottery
// has not been drawn on yet, then opens an empty pot for the next one. It is
// called from ensureTodayRateIndexLocked, i.e. from inside every caller's own
// Update — the draw and the day's rate are committed by the same write, so a
// crash cannot leave a guild with a new rate and yesterday's pot.
//
// Nobody entered (no tickets, or a randSource too broken to name a winner):
// the whole prize — which is just the carryover, since no sales happened —
// rolls forward untouched and LastDraw is deliberately NOT overwritten, so
// /lottery status keeps showing the last real result through a quiet spell.
// The date is still stamped, so the empty draw is not retried all day.
//
// The comparison is `DrawDate < today` rather than `!=`: a clock that moves
// backwards (an NTP correction, a hand-set host clock) must not be able to
// re-draw a day that has already paid out — the same rule
// ensureTodayRateIndexLocked applies to the rate history. Caller must already
// hold the Store's lock, which is also what makes reading rng safe.
func drawLotteryLocked(economy *GuildEconomy, drawDate string, rng randSource) {
	lottery := &economy.Lottery
	if drawDate == "" || lottery.DrawDate >= drawDate {
		return
	}
	prize, house := LotteryPrize(lottery.Sales, lottery.Carryover)
	sold, buyers, _ := lotterySummaryLocked(lottery)
	winner := PickLotteryWinner(lottery.Tickets, rng)
	if winner == "" {
		// The prize rolls forward, but the house's cut takes the SAME road it
		// takes on a draw that named somebody — into the pool. Leaving it out
		// destroyed it: on the normal quiet day sales are 0 and so is the cut,
		// but a pot holding sales with no ticket holders (a hand-edited file,
		// or a partial write between the debit and Tickets) rolls only
		// floor(sales*90/100) forward and drops the remainder on the floor.
		// 設計書 C-3a §3 puts the cut in the pool unconditionally, and C3-07
		// settled the rule it follows from: chips leave circulation only where
		// a cap refuses them.
		//
		// What will not FIT in the carryover takes that road too. Carryover
		// obeys the same MaxChips ceiling an account does, and the prize is
		// this pot's share ON TOP of the carryover already there, so a pot
		// near the ceiling produces a prize above it. Splitting the excess off
		// here, at the write, is what keeps normalizeLotteryLocked's clamp
		// honest: that clamp exists to repair a hand-edited file, and letting
		// it meet a number the draw itself produced would turn it into a
		// deletion of chips this package created. The invariant it restores is
		// therefore established at the point of writing — Carryover is never
		// persisted above MaxChips — and the clamp stays as the defence
		// against the file, not against us.
		overflow := int64(0)
		if prize > MaxChips {
			overflow, prize = prize-MaxChips, MaxChips
		}
		creditJackpotCappedLocked(economy, house+overflow)
		lottery.Carryover = prize
		lottery.Tickets, lottery.Sales, lottery.DrawDate = nil, 0, drawDate
		return
	}
	winnerAccount := ensureAccountLocked(economy, winner)
	paid := creditChipsCappedLocked(winnerAccount, prize)
	// The winner's half of the lottery's 純利 (設計書 C-3b §3). It books
	// `paid`, not `prize`: a winner already at MaxChips takes only what fits,
	// and the remainder below goes to the jackpot pool rather than to them —
	// counting it would show a season rank their balance never earned. The
	// matching 「賭けた額」 was booked ticket by ticket in BuyLotteryTickets,
	// because that is where the chips actually left the buyer.
	addSeasonNetLocked(winnerAccount, paid)
	// The house's cut feeds the slot jackpot pool (設計書 C-3a §3), and so
	// does prize-paid: that remainder is 0 on every normal draw and non-zero
	// only when the winner was at MaxChips. It goes down the same path as the
	// cut instead of carrying over, because a carryover would hand THIS
	// winner's prize to whoever wins the NEXT draw, someone else. The pool
	// pays it back out on 7️⃣7️⃣7️⃣, so it is not destroyed — unless the pool
	// is itself at MaxJackpot, the single exception the cap buys us.
	//
	// creditJackpotCappedLocked seeds before it adds, which is what keeps a
	// pool still sitting at a pre-C-3a 0 from swallowing the cut when the
	// raise to the seed happens after the addition rather than before.
	creditJackpotCappedLocked(economy, house+(prize-paid))
	draw := LotteryDraw{
		Date: drawDate, WinnerID: winner, Prize: paid, TicketsSold: sold, Buyers: buyers,
	}
	lottery.LastDraw = &draw
	// The announcement queue is APPENDED to, never overwritten: two draws can
	// settle before either is posted (a bot down at 09:00 settles the missed
	// day at startup, then settles the new one the next morning), and the
	// first must not be lost. Only MarkAnnounced — i.e. a confirmed send —
	// takes entries back out.
	lottery.Unannounced = append(lottery.Unannounced, draw)
	if n := len(lottery.Unannounced); n > lotteryUnannouncedLimit {
		// Drop from the FRONT: the oldest announcement is the one least worth
		// posting weeks late. Copying into the same backing array is safe
		// because the destination index is always lower than the source.
		lottery.Unannounced = append(lottery.Unannounced[:0], lottery.Unannounced[n-lotteryUnannouncedLimit:]...)
	}
	// Carryover belongs to the no-winner path alone: a draw that named a
	// winner always reopens the pot empty.
	lottery.Carryover = 0
	lottery.Tickets, lottery.Sales, lottery.DrawDate = nil, 0, drawDate
}

// LotteryPurchase is what a successful /lottery buy left behind: the buyer's
// own numbers plus the state of the pot they just joined, so the command
// layer can render the confirmation without a second store round-trip.
type LotteryPurchase struct {
	Count       int       // tickets bought by THIS call
	Cost        int64     // chips debited by this call
	Balance     int64     // the buyer's chips after the purchase
	UserTickets int       // the buyer's whole holding for the next draw
	TicketsSold int       // every buyer's tickets for the next draw
	Buyers      int       // distinct buyers in the next draw
	Prize       int64     // what the next draw pays as of this purchase
	NextDrawAt  time.Time // the 09:00 JST these tickets are drawn at
}

// BuyLotteryTickets buys `count` tickets for guildID/userID at
// LotteryTicketPrice chips each, in one Update. Returns *ErrLotteryLimit
// when the purchase would take the buyer past LotteryMaxTicketsPerDraw for
// this draw (the cap is CUMULATIVE, so it also covers a single oversized
// call), *ErrInsufficientChips when the balance will not cover the cost, and
// ErrInvalidAmount below 1 — defense in depth, exactly as Spin treats its
// bet bounds: the command layer's option range is a UX nicety, not a
// guarantee. Nothing at all is persisted on any error, so a refused purchase
// cannot eat chips.
//
// The daily rollover runs FIRST, inside the same transaction: a purchase
// made after 09:00 JST on a day nothing else has touched the guild must join
// the NEW draw, not be swept into yesterday's pot by the rollover that a
// later command would trigger.
//
// A REFUSED purchase is reported through `refused`, held outside the
// closure, and the closure still returns nil (危険地帯). Returning the error
// from inside would abort Update's write and throw away the draw the
// rollover just settled — while rng, which lives on the Store and not in
// Data, would keep the position that draw advanced it to. A buyer who cannot
// afford a ticket could then re-run the command until the discarded draw
// named them the winner. Nothing of the PURCHASE is written on that path:
// the refusal checks all run before the first mutation, so the committed
// write carries the rollover alone.
func (s *Store) BuyLotteryTickets(guildID, userID string, count int, now time.Time) (LotteryPurchase, error) {
	if count < 1 {
		return LotteryPurchase{}, ErrInvalidAmount
	}
	var result LotteryPurchase
	var refused error
	err := s.Update(func(d *Data) error {
		result, refused = LotteryPurchase{}, nil
		economy := ensureGuildLocked(d, guildID)
		ensureTodayRateLocked(economy, now, s.rng) // rolls the draw over
		account := ensureAccountLocked(economy, userID)
		lottery := &economy.Lottery
		owned := lottery.Tickets[userID] // reading a nil map is legal and yields 0
		remaining := LotteryMaxTicketsPerDraw - owned
		if remaining < 0 {
			remaining = 0 // hand-edited holding already past the cap
		}
		if count > remaining {
			refused = &ErrLotteryLimit{Remaining: remaining}
			return nil // commit the rollover, not the purchase
		}
		cost := LotteryTicketPrice * int64(count) // count <= 10, so no overflow
		if account.Chips < cost {
			refused = &ErrInsufficientChips{Balance: account.Chips}
			return nil // commit the rollover, not the purchase
		}
		account.Chips -= cost
		// The ticket price is booked as 純利 the moment it is paid, not at
		// the draw, because a ticket has no refund path: unlike an escrowed
		// bet, these chips are gone whatever happens next (the draw may name
		// someone else, or the bot may never run it). Booking it here is also
		// what makes a buyer who never wins show up as negative rather than
		// as never having played.
		addSeasonNetLocked(account, -cost)
		if lottery.Tickets == nil {
			lottery.Tickets = make(map[string]int, 1)
		}
		lottery.Tickets[userID] = owned + count
		lottery.Sales += cost
		sold, buyers, prize := lotterySummaryLocked(lottery)
		result = LotteryPurchase{
			Count: count, Cost: cost, Balance: account.Chips,
			UserTickets: lottery.Tickets[userID],
			TicketsSold: sold, Buyers: buyers, Prize: prize,
			NextDrawAt: nextLotteryDrawAt(lottery.DrawDate, now),
		}
		return nil
	})
	if err != nil {
		return LotteryPurchase{}, err
	}
	if refused != nil {
		return LotteryPurchase{}, refused
	}
	return result, nil
}

// LotteryView is what /lottery status shows: the pot being sold into now and
// the last draw that produced a winner (nil until one has).
type LotteryView struct {
	Prize       int64     // what the next draw pays if nobody else buys
	TicketsSold int       // tickets in the next draw
	Buyers      int       // distinct buyers in the next draw
	UserTickets int       // the asking user's own tickets
	NextDrawAt  time.Time // the 09:00 JST these tickets are drawn at
	LastDraw    *LotteryDraw
}

// LotteryStatus reports guildID's lottery for userID, running the daily
// rollover in the same transaction — so merely LOOKING at the status after
// 09:00 JST performs the draw that the bot may have been down for, and the
// user sees the result rather than a stale pot.
func (s *Store) LotteryStatus(guildID, userID string, now time.Time) (LotteryView, error) {
	var result LotteryView
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		ensureTodayRateLocked(economy, now, s.rng) // rolls the draw over
		lottery := &economy.Lottery
		sold, buyers, prize := lotterySummaryLocked(lottery)
		result = LotteryView{
			Prize: prize, TicketsSold: sold, Buyers: buyers,
			UserTickets: lottery.Tickets[userID],
			NextDrawAt:  nextLotteryDrawAt(lottery.DrawDate, now),
		}
		if lottery.LastDraw != nil {
			// Hand back a COPY: the pointer belongs to the Data this Update
			// is about to write out and drop, and the caller reads the view
			// long after the lock is gone.
			draw := *lottery.LastDraw
			result.LastDraw = &draw
		}
		return nil
	})
	if err != nil {
		return LotteryView{}, err
	}
	return result, nil
}
