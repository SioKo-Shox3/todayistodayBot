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

// creditChipsLocked adds delta to account.Chips, refusing to exceed
// MaxChips. Caller must already hold the Store's lock. Every chip credit in
// this package goes through here — do NOT write `account.Chips += x`
// anywhere else. The bound is written as `Chips > MaxChips-delta` rather
// than `Chips+delta > MaxChips` because the latter can itself wrap.
func creditChipsLocked(account *UserAccount, delta int64) error {
	if delta < 0 || account.Chips > MaxChips-delta {
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
// a freshly-generated one (via nextRate) if missing, and trimming history to
// the most recent 30 entries (設計書). Caller must already hold the Store's
// lock, which is also what makes reading s.rng here safe — *rand.Rand is not
// safe for concurrent use.
func ensureTodayRateLocked(economy *GuildEconomy, today string, rng randSource) DailyRate {
	if n := len(economy.Rates); n > 0 && economy.Rates[n-1].Date == today {
		return economy.Rates[n-1]
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
	return result
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
	TotalAssets int64 // Chips + Coins*rate
}

// topAssetsLocked ranks economy's users by total assets at `rate`, ties
// broken by ascending UserID for deterministic output. Caller must already
// hold the Store's lock.
//
// Chips + Coins*rate cannot overflow int64: the store invariants are
// Chips <= MaxChips (1e12), Coins <= MaxCoins (1e12) and rate <= maxRate
// (140, the clamp ceiling), so the sum is bounded by
// 1e12 + 1e12*140 = 1.41e14, about 65,413x below math.MaxInt64
// (9.223e18). Every credit path enforces those caps.
func topAssetsLocked(economy *GuildEconomy, rate int, limit int) []RankEntry {
	entries := make([]RankEntry, 0, len(economy.Users))
	for userID, account := range economy.Users {
		entries = append(entries, RankEntry{UserID: userID, TotalAssets: account.Chips + account.Coins*int64(rate)})
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
// date), auto-creating the account (welcome bonus) on first interaction.
// Returns ErrAlreadyClaimedToday (no mutation) if already claimed today,
// or ErrChipCapExceeded (also no mutation) if the payout would break
// MaxChips.
func (s *Store) ClaimDaily(guildID, userID string, now time.Time) (DailyClaimResult, error) {
	today := jstDate(now)
	yesterday := jstYesterday(now) // NOT jstDate(now.AddDate(0,0,-1)) — see jstYesterday's doc
	var result DailyClaimResult
	err := s.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		account := ensureAccountLocked(economy, userID)
		if account.LastDailyDate == today {
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
		result = AccountView{Account: account, RateUsed: rate.Rate, TotalAssets: account.Chips + account.Coins*int64(rate.Rate)}
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
		ensureTodayRateLocked(economy, today, s.rng)
		recent := economy.Rates
		if len(recent) > limit {
			recent = recent[len(recent)-limit:]
		}
		result = recent
		return nil
	})
	return result, err
}
