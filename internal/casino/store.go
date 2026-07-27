package casino

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
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
// and every locking method added by later tasks). Use the unexported
// *Locked helpers instead. fn must also not perform network I/O, Discord
// API calls, or sleeps — the whole guild economy is blocked for its
// duration.
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
