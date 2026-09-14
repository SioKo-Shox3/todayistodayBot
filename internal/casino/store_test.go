package casino

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixedNow is the arbitrary but FIXED JST instant every economy test is
// anchored to. Nothing in this package may read the wall clock from a test: a
// test that only passes before 09:00 JST is not evidence.
var fixedNow = time.Date(2026, 7, 10, 12, 0, 0, 0, jst)

// daysAfter returns fixedNow shifted by n JST calendar days.
func daysAfter(n int) time.Time { return fixedNow.AddDate(0, 0, n) }

// newTempStore returns a Store backed by a fresh, not-yet-created file in
// t.TempDir(). New (not Default) is the test-only constructor — production
// code must share Default()'s single mutex.
func newTempStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "casino.json")
	return New(path), path
}

// at pins the store's clock to tm and returns the store, so a claim reads
// as st.at(day).ClaimDaily(...). Production never sets s.clock; the methods
// that decide a JST date for themselves take no instant from the caller,
// exactly so that the date is chosen under the lock.
func (s *Store) at(tm time.Time) *Store {
	s.clock = func() time.Time { return tm }
	return s
}

// seedAccount writes a known balance for guildID/userID, bypassing the
// credit helpers so cap-boundary tests can start from any balance.
func seedAccount(t *testing.T, st *Store, guildID, userID string, account UserAccount) {
	t.Helper()
	seeded := account
	err := st.Update(func(d *Data) error {
		(*d)[guildID] = &GuildEconomy{Users: map[string]*UserAccount{userID: &seeded}}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding %s/%s: %v", guildID, userID, err)
	}
}

// readAccount reads guildID/userID back through a *fresh* Store, so the
// assertion is about what actually reached the file.
func readAccount(t *testing.T, path, guildID, userID string) UserAccount {
	t.Helper()
	data, err := New(path).Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	economy := data[guildID]
	if economy == nil {
		t.Fatalf("guild %q missing from persisted data: %+v", guildID, data)
	}
	account := economy.Users[userID]
	if account == nil {
		t.Fatalf("user %q missing from guild %q: %+v", userID, guildID, economy.Users)
	}
	return *account
}

// assertUpdateSeesEmptyData is the shared body of the three
// "degenerate file content" regressions: Snapshot and Update must both hand
// back a NON-NIL empty Data. A nil Data is not merely untidy — assigning to
// it panics with "assignment to entry in nil map", and discordgo runs every
// handler in its own goroutine without recover, so that panic kills the bot
// and leaves the same bad file behind: a permanent crash loop.
func assertUpdateSeesEmptyData(t *testing.T, st *Store) {
	t.Helper()

	snapshot, err := st.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if snapshot == nil {
		t.Fatal("Snapshot returned a nil Data — writing to it would panic with \"assignment to entry in nil map\"")
	}
	if len(snapshot) != 0 {
		t.Fatalf("expected an empty Data from Snapshot, got %+v", snapshot)
	}

	err = st.Update(func(d *Data) error {
		if *d == nil {
			// Returning an error rather than assigning keeps the failure an
			// assertion instead of a panic.
			return errors.New("Update handed the closure a nil Data")
		}
		(*d)["guild1"] = &GuildEconomy{Users: map[string]*UserAccount{}}
		return nil
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
}

func TestStore_Update_PersistsAndIsReadableByFreshInstance(t *testing.T) {
	st, path := newTempStore(t)

	err := st.Update(func(d *Data) error {
		(*d)["guild1"] = &GuildEconomy{
			AnnounceChannelID: "channel-1",
			Users:             map[string]*UserAccount{"user-1": {Coins: 3, Chips: 7}},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the file to exist after Update: %v", err)
	}
	var onDisk Data
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
	if onDisk["guild1"] == nil || onDisk["guild1"].AnnounceChannelID != "channel-1" {
		t.Fatalf("unexpected persisted data: %s", raw)
	}

	fresh, err := New(path).Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	economy := fresh["guild1"]
	if economy == nil {
		t.Fatalf("expected a fresh Store instance to read guild1 back, got %+v", fresh)
	}
	account := economy.Users["user-1"]
	if account == nil || account.Coins != 3 || account.Chips != 7 {
		t.Fatalf("unexpected account read back by a fresh Store: %+v", account)
	}
}

func TestStore_Update_MissingFile_TreatsAsEmptyNotError(t *testing.T) {
	st, path := newTempStore(t)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("precondition: expected %q not to exist, stat err = %v", path, err)
	}

	assertUpdateSeesEmptyData(t, st)
}

func TestStore_Read_EmptyFileTreatedAsEmptyData(t *testing.T) {
	st, path := newTempStore(t)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("writing zero-byte file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after writing zero-byte file: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("precondition: expected a zero-byte file, got %d bytes", info.Size())
	}

	assertUpdateSeesEmptyData(t, st)
}

func TestStore_Read_NullJSONTreatedAsEmptyData(t *testing.T) {
	st, path := newTempStore(t)
	if err := os.WriteFile(path, []byte("null"), 0o644); err != nil {
		t.Fatalf("writing null JSON file: %v", err)
	}

	assertUpdateSeesEmptyData(t, st)
}

func TestStore_Mint_AfterZeroByteFile_DoesNotPanic(t *testing.T) {
	st, path := newTempStore(t)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("writing zero-byte file: %v", err)
	}

	// End-to-end version of the nil-map regression: a real casino operation
	// (not just readLocked) over a truncated file must not panic.
	if err := st.Mint("guild1", "user-1", 250); err != nil {
		t.Fatalf("Mint over a zero-byte file returned error: %v", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Coins != 250 {
		t.Fatalf("expected 250 coins after Mint, got %d", account.Coins)
	}
}

func TestStore_Update_FnReturnsError_DoesNotPersist(t *testing.T) {
	st, path := newTempStore(t)

	err := st.Update(func(d *Data) error {
		(*d)["guild1"] = &GuildEconomy{AnnounceChannelID: "channel-1", Users: map[string]*UserAccount{}}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding Update returned error: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the seeded file: %v", err)
	}

	sentinel := errors.New("boom")
	err = st.Update(func(d *Data) error {
		(*d)["guild1"].AnnounceChannelID = "channel-2"
		(*d)["guild2"] = &GuildEconomy{Users: map[string]*UserAccount{}}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected Update to propagate the closure's error, got %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the file after the aborted Update: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("an aborted Update must persist nothing.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestStore_Update_ConcurrentWritesDoNotCorruptFile(t *testing.T) {
	st, path := newTempStore(t)

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			guildID := fmt.Sprintf("guild-%d", i)
			err := st.Update(func(d *Data) error {
				(*d)[guildID] = &GuildEconomy{
					AnnounceChannelID: fmt.Sprintf("channel-%d", i),
					Users:             map[string]*UserAccount{},
				}
				return nil
			})
			if err != nil {
				t.Errorf("Update() goroutine %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the file after concurrent Update: %v", err)
	}
	var data Data
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("file is not valid JSON after concurrent Update (the mutex failed to serialize writes): %v", err)
	}
	if len(data) != n {
		t.Fatalf("expected %d guild entries after %d concurrent Update calls, got %d — a lost update means the mutex is not serializing the read-modify-write", n, n, len(data))
	}
	for i := 0; i < n; i++ {
		guildID := fmt.Sprintf("guild-%d", i)
		economy := data[guildID]
		if economy == nil {
			t.Fatalf("guild %q is missing after concurrent Update — its write was lost", guildID)
		}
		if want := fmt.Sprintf("channel-%d", i); economy.AnnounceChannelID != want {
			t.Fatalf("guild %q: AnnounceChannelID = %q, want %q", guildID, economy.AnnounceChannelID, want)
		}
	}
}

func TestDefault_ReturnsSameInstanceAcrossCalls(t *testing.T) {
	a := Default()
	b := Default()
	if a != b {
		t.Fatal("expected Default() to return the same *Store pointer on every call, so every casino command shares one sync.Mutex")
	}
}

func TestStore_Mint_CreditsCoinsAutoCreatesAccount(t *testing.T) {
	st, path := newTempStore(t)

	if err := st.Mint("guild1", "user-1", 500); err != nil {
		t.Fatalf("Mint returned error: %v", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Coins != 500 {
		t.Fatalf("expected 500 coins after Mint, got %d", account.Coins)
	}
	if account.Chips != welcomeBonusChips {
		t.Fatalf("expected the auto-created account to carry the %d-chip welcome bonus, got %d", welcomeBonusChips, account.Chips)
	}
}

func TestStore_Mint_BelowMinimum(t *testing.T) {
	st, path := newTempStore(t)

	for _, amount := range []int64{0, -1} {
		if err := st.Mint("guild1", "user-1", amount); !errors.Is(err, ErrInvalidAmount) {
			t.Fatalf("Mint(%d) = %v, want ErrInvalidAmount", amount, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a rejected Mint must not create the data file, stat err = %v", err)
	}
}

func TestStore_Mint_AmountAboveMaxCoins_ReturnsInvalidAmount(t *testing.T) {
	st, path := newTempStore(t)

	if err := st.Mint("guild1", "user-1", MaxCoins+1); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("Mint(MaxCoins+1) = %v, want ErrInvalidAmount", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a rejected Mint must not create the data file, stat err = %v", err)
	}
}

func TestStore_Mint_ExactlyAtMaxCoins_Succeeds(t *testing.T) {
	st, path := newTempStore(t)

	if err := st.Mint("guild1", "user-1", MaxCoins); err != nil {
		t.Fatalf("minting exactly MaxCoins onto an empty balance must succeed, got %v", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Coins != MaxCoins {
		t.Fatalf("expected Coins == MaxCoins (%d), got %d", MaxCoins, account.Coins)
	}
}

func TestStore_Mint_OneOverMaxCoins_NoMutation(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, "guild1", "user-1", UserAccount{Coins: MaxCoins, Chips: 42})

	if err := st.Mint("guild1", "user-1", 1); !errors.Is(err, ErrCoinCapExceeded) {
		t.Fatalf("Mint(1) at exactly MaxCoins = %v, want ErrCoinCapExceeded", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Coins != MaxCoins || account.Chips != 42 {
		t.Fatalf("a cap-exceeded Mint must not move a single coin or chip, got %+v", account)
	}
}

func TestStore_Mint_ExceedsMaxCoins_NoMutation(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, "guild1", "user-1", UserAccount{Coins: 5000, Chips: 777})

	if err := st.Mint("guild1", "user-1", MaxCoins); !errors.Is(err, ErrCoinCapExceeded) {
		t.Fatalf("Mint(MaxCoins) onto a non-empty balance = %v, want ErrCoinCapExceeded", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Coins != 5000 || account.Chips != 777 {
		t.Fatalf("a cap-exceeded Mint must not move a single coin or chip, got %+v", account)
	}
}

func TestStore_SetAnnounceChannel_Persists(t *testing.T) {
	st, path := newTempStore(t)

	if err := st.SetAnnounceChannel("guild1", "channel-1"); err != nil {
		t.Fatalf("SetAnnounceChannel returned error: %v", err)
	}
	if err := st.SetAnnounceChannel("guild1", "channel-2"); err != nil {
		t.Fatalf("SetAnnounceChannel (overwrite) returned error: %v", err)
	}

	data, err := New(path).Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	economy := data["guild1"]
	if economy == nil {
		t.Fatalf("guild1 is missing from the persisted data: %+v", data)
	}
	if economy.AnnounceChannelID != "channel-2" {
		t.Fatalf("expected the most recent announce channel to be persisted, got %q", economy.AnnounceChannelID)
	}
	if economy.Users == nil {
		t.Fatal("ensureGuildLocked must leave a non-nil Users map behind")
	}
}

func TestCreditChipsLocked_AtCap(t *testing.T) {
	account := &UserAccount{Chips: MaxChips - 10}

	if err := creditChipsLocked(account, 10); err != nil {
		t.Fatalf("crediting exactly up to MaxChips must succeed, got %v", err)
	}
	if account.Chips != MaxChips {
		t.Fatalf("expected Chips == MaxChips (%d), got %d", MaxChips, account.Chips)
	}
}

func TestCreditChipsLocked_OverCap(t *testing.T) {
	account := &UserAccount{Chips: MaxChips - 10}

	if err := creditChipsLocked(account, 11); !errors.Is(err, ErrChipCapExceeded) {
		t.Fatalf("crediting 11 at MaxChips-10 = %v, want ErrChipCapExceeded", err)
	}
	if account.Chips != MaxChips-10 {
		t.Fatalf("a rejected credit must leave the balance untouched, got %d", account.Chips)
	}
}

func TestCreditChipsLocked_NegativeDelta(t *testing.T) {
	account := &UserAccount{Chips: 100}

	if err := creditChipsLocked(account, -1); !errors.Is(err, ErrChipCapExceeded) {
		t.Fatalf("crediting -1 = %v, want ErrChipCapExceeded (a credit is never a debit)", err)
	}
	if account.Chips != 100 {
		t.Fatalf("a rejected credit must leave the balance untouched, got %d", account.Chips)
	}
}

func TestCreditCoinsLocked_AtCap(t *testing.T) {
	account := &UserAccount{Coins: MaxCoins - 10}

	if err := creditCoinsLocked(account, 10); err != nil {
		t.Fatalf("crediting exactly up to MaxCoins must succeed, got %v", err)
	}
	if account.Coins != MaxCoins {
		t.Fatalf("expected Coins == MaxCoins (%d), got %d", MaxCoins, account.Coins)
	}
}

func TestCreditCoinsLocked_OverCap(t *testing.T) {
	account := &UserAccount{Coins: MaxCoins - 10}

	if err := creditCoinsLocked(account, 11); !errors.Is(err, ErrCoinCapExceeded) {
		t.Fatalf("crediting 11 at MaxCoins-10 = %v, want ErrCoinCapExceeded", err)
	}
	if account.Coins != MaxCoins-10 {
		t.Fatalf("a rejected credit must leave the balance untouched, got %d", account.Coins)
	}
}

func TestCreditCoinsLocked_NegativeDelta(t *testing.T) {
	account := &UserAccount{Coins: 100}

	if err := creditCoinsLocked(account, -1); !errors.Is(err, ErrCoinCapExceeded) {
		t.Fatalf("crediting -1 = %v, want ErrCoinCapExceeded (a credit is never a debit)", err)
	}
	if account.Coins != 100 {
		t.Fatalf("a rejected credit must leave the balance untouched, got %d", account.Coins)
	}
}

// seedRate pins guildID's DailyRate for now's JST calendar date, so exchange,
// ranking and history tests are deterministic without touching s.rng. It
// appends, keeping whatever the guild already holds.
func seedRate(t *testing.T, st *Store, guildID string, now time.Time, rate int) {
	t.Helper()
	err := st.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		economy.Rates = append(economy.Rates, DailyRate{Date: jstDate(now), Rate: rate, Trend: TrendFlat, Event: EventNone})
		return nil
	})
	if err != nil {
		t.Fatalf("seeding rate for %s: %v", guildID, err)
	}
}

// seedAccounts writes known balances for several users at once, bypassing the
// credit helpers so cap-boundary tests can start from any balance. Unlike
// seedAccount it preserves the guild's rate history.
func seedAccounts(t *testing.T, st *Store, guildID string, users map[string]UserAccount) {
	t.Helper()
	err := st.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		for userID, account := range users {
			seeded := account
			economy.Users[userID] = &seeded
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding accounts for %s: %v", guildID, err)
	}
}

// writeHandEditedFile writes raw straight to path, bypassing the Store
// entirely. The corrupt shapes the §3.3 crash-loop regressions need
// (a null account value, a rate record with no "rate" field) cannot be
// produced by any write path in this package, so they cannot be seeded with
// seedAccounts/seedRate — only a hand edit or a partial write creates them.
func writeHandEditedFile(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("writing hand-edited data file: %v", err)
	}
}

// readGuild reads guildID back through a *fresh* Store, so the assertion is
// about what actually reached the file.
func readGuild(t *testing.T, path, guildID string) GuildEconomy {
	t.Helper()
	data, err := New(path).Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	economy := data[guildID]
	if economy == nil {
		t.Fatalf("guild %q missing from persisted data: %+v", guildID, data)
	}
	return *economy
}

func TestStore_EnsureTodayRate_FirstDay(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "a guild's first day is the fixed 基準100, not a draw"}

	rate, err := st.EnsureTodayRate("guild1", fixedNow)
	if err != nil {
		t.Fatalf("EnsureTodayRate returned error: %v", err)
	}
	if rate.Rate != baseRate || rate.Trend != TrendFlat || rate.Event != EventNone {
		t.Fatalf("first day rate = %+v, want {Rate:%d Trend:%s Event:%s}", rate, baseRate, TrendFlat, EventNone)
	}
	if rate.Date != jstDate(fixedNow) {
		t.Fatalf("rate.Date = %q, want the JST calendar date %q", rate.Date, jstDate(fixedNow))
	}

	economy := readGuild(t, path, "guild1")
	if len(economy.Rates) != 1 || economy.Rates[0] != rate {
		t.Fatalf("persisted rates = %+v, want exactly the returned %+v", economy.Rates, rate)
	}
}

func TestStore_EnsureTodayRate_Idempotent(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "today's rate already exists, so no new draw may happen"}

	first, err := st.EnsureTodayRate("guild1", fixedNow)
	if err != nil {
		t.Fatalf("first EnsureTodayRate returned error: %v", err)
	}
	second, err := st.EnsureTodayRate("guild1", fixedNow.Add(6*time.Hour))
	if err != nil {
		t.Fatalf("second EnsureTodayRate returned error: %v", err)
	}
	if second != first {
		t.Fatalf("second call on the same JST day returned %+v, want the stored %+v", second, first)
	}

	economy := readGuild(t, path, "guild1")
	if len(economy.Rates) != 1 {
		t.Fatalf("persisted %d rate entries for one JST day, want 1: %+v", len(economy.Rates), economy.Rates)
	}
}

func TestStore_EnsureTodayRate_NextDay(t *testing.T) {
	st, path := newTempStore(t)
	seedRate(t, st, "guild1", fixedNow, 100)
	// No trend flip (0.5 >= 0.15), no event (0.5 >= 0.05), flat drift, noise
	// = -3 + 0.6*6 = +0.6% -> 100.6 -> 101.
	st.rng = &cyclicRand{floats: []float64{0.5, 0.5, 0.6}}

	rate, err := st.EnsureTodayRate("guild1", daysAfter(1))
	if err != nil {
		t.Fatalf("EnsureTodayRate returned error: %v", err)
	}
	if rate.Date != jstDate(daysAfter(1)) {
		t.Fatalf("rate.Date = %q, want %q", rate.Date, jstDate(daysAfter(1)))
	}
	if rate.Rate != 101 {
		t.Fatalf("rate.Rate = %d, want 101 (a random-walk step from yesterday's 100)", rate.Rate)
	}

	economy := readGuild(t, path, "guild1")
	if len(economy.Rates) != 2 {
		t.Fatalf("persisted %d rate entries, want yesterday's plus today's: %+v", len(economy.Rates), economy.Rates)
	}
}

func TestStore_EnsureTodayRate_TrimsHistoryTo30(t *testing.T) {
	st, path := newTempStore(t)
	err := st.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, "guild1")
		for i := 0; i < 30; i++ {
			economy.Rates = append(economy.Rates, DailyRate{Date: jstDate(daysAfter(i - 30)), Rate: 100, Trend: TrendFlat, Event: EventNone})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding 30 days of history: %v", err)
	}
	st.rng = &cyclicRand{floats: []float64{0.5, 0.5, 0.6}}

	if _, err := st.EnsureTodayRate("guild1", fixedNow); err != nil {
		t.Fatalf("EnsureTodayRate returned error: %v", err)
	}

	economy := readGuild(t, path, "guild1")
	if len(economy.Rates) != 30 {
		t.Fatalf("persisted %d rate entries, want the history trimmed to 30", len(economy.Rates))
	}
	if want := jstDate(daysAfter(-29)); economy.Rates[0].Date != want {
		t.Fatalf("oldest retained date = %q, want %q (the 31st-oldest entry must be dropped)", economy.Rates[0].Date, want)
	}
	if want := jstDate(fixedNow); economy.Rates[29].Date != want {
		t.Fatalf("newest date = %q, want today's %q", economy.Rates[29].Date, want)
	}
}

func TestStore_EnsureTodayRate_PersistsEventKind(t *testing.T) {
	st, path := newTempStore(t)
	seedRate(t, st, "guild1", fixedNow, 100)
	// No trend flip, event hit (0 < 0.05), magnitude 15%, direction 0.4 < 0.5
	// = surge -> 100 * 1.15 -> 115.
	st.rng = &cyclicRand{floats: []float64{0.5, 0, 0, 0.4}}

	rate, err := st.EnsureTodayRate("guild1", daysAfter(1))
	if err != nil {
		t.Fatalf("EnsureTodayRate returned error: %v", err)
	}
	if rate.Event != EventSurge || rate.Rate != 115 {
		t.Fatalf("returned rate = %+v, want Rate 115 with Event %q", rate, EventSurge)
	}

	economy := readGuild(t, path, "guild1")
	stored := economy.Rates[len(economy.Rates)-1]
	if stored.Event != EventSurge {
		t.Fatalf("persisted Event = %q, want %q — the draw must be stored, not re-derived from the day-over-day move", stored.Event, EventSurge)
	}
}

// TestStore_EnsureTodayRate_InRangePersistedEntry_KeptAsIs is the
// over-repair guard for the regeneration rule below: minRate and maxRate are
// legitimate, reachable values (the clamp is inclusive), so a stored record
// sitting exactly on either boundary must be returned untouched, with no draw
// at all.
func TestStore_EnsureTodayRate_InRangePersistedEntry_KeptAsIs(t *testing.T) {
	for _, rate := range []int{minRate, baseRate, maxRate} {
		t.Run(fmt.Sprintf("rate %d", rate), func(t *testing.T) {
			st, path := newTempStore(t)
			st.rng = forbiddenRand{t: t, reason: "a valid rate for today must be returned as stored, never regenerated"}
			writeHandEditedFile(t, path, fmt.Sprintf(
				`{"guild1":{"rates":[{"date":%q,"rate":%d,"trend":"bull","event":"surge"}],"users":{}}}`,
				jstDate(fixedNow), rate))

			want := DailyRate{Date: jstDate(fixedNow), Rate: rate, Trend: TrendBull, Event: EventSurge}
			got, err := st.EnsureTodayRate("guild1", fixedNow)
			if err != nil {
				t.Fatalf("EnsureTodayRate returned error: %v", err)
			}
			if got != want {
				t.Fatalf("EnsureTodayRate = %+v, want the stored %+v", got, want)
			}

			economy := readGuild(t, path, "guild1")
			if len(economy.Rates) != 1 || economy.Rates[0] != want {
				t.Fatalf("persisted rates = %+v, want exactly the stored %+v", economy.Rates, want)
			}
		})
	}
}

// TestStore_EnsureTodayRate_OutOfRangePersistedEntry_Regenerates is the
// crash-loop regression at its SOURCE. nextRate clamps every value it returns
// into [minRate, maxRate], so a today record outside that range can only come
// from a hand edit or a partial write — and a record that simply lacks the
// "rate" field unmarshals as Rate 0. Handing that 0 back as today's rate makes
// both exchange helpers divide by zero, which panics in a discordgo handler
// goroutine (no recover), kills the bot, and leaves the same bad file on disk:
// every restart re-crashes. Repairing it here covers every consumer at once
// (/exchange, /rank, /rate, the 9am announcement).
func TestStore_EnsureTodayRate_OutOfRangePersistedEntry_Regenerates(t *testing.T) {
	today := jstDate(fixedNow)
	yesterday := jstYesterday(fixedNow)
	// No previous day survives in the first six cases, so regeneration is the
	// day-1 fixed 基準100 with no randomness consumed.
	dayOne := DailyRate{Date: today, Rate: baseRate, Trend: TrendFlat, Event: EventNone}

	tests := []struct {
		name  string
		rates string
		want  []DailyRate // the full persisted history after the repair
	}{
		{
			name:  "rate field absent",
			rates: fmt.Sprintf(`[{"date":%q,"trend":"flat"}]`, today),
			want:  []DailyRate{dayOne},
		},
		{
			name:  "rate zero",
			rates: fmt.Sprintf(`[{"date":%q,"rate":0,"trend":"flat","event":"none"}]`, today),
			want:  []DailyRate{dayOne},
		},
		{
			name:  "negative rate",
			rates: fmt.Sprintf(`[{"date":%q,"rate":-5,"trend":"flat","event":"none"}]`, today),
			want:  []DailyRate{dayOne},
		},
		{
			name:  "one below minRate",
			rates: fmt.Sprintf(`[{"date":%q,"rate":%d,"trend":"flat","event":"none"}]`, today, minRate-1),
			want:  []DailyRate{dayOne},
		},
		{
			name:  "one above maxRate",
			rates: fmt.Sprintf(`[{"date":%q,"rate":%d,"trend":"flat","event":"none"}]`, today, maxRate+1),
			want:  []DailyRate{dayOne},
		},
		{
			name:  "absurd rate",
			rates: fmt.Sprintf(`[{"date":%q,"rate":1000000,"trend":"flat","event":"none"}]`, today),
			want:  []DailyRate{dayOne},
		},
		{
			// Two corrupt today records: stripping only the last one would
			// leave the other as both a duplicate date and nextRate's prevRate.
			name: "two corrupt today entries",
			rates: fmt.Sprintf(`[{"date":%q,"rate":0,"trend":"flat","event":"none"},{"date":%q,"trend":"flat"}]`,
				today, today),
			want: []DailyRate{dayOne},
		},
		{
			// The valid yesterday must survive and become the regeneration's
			// starting point — the repair drops the corrupt record only.
			name: "corrupt today entry after a valid yesterday",
			rates: fmt.Sprintf(`[{"date":%q,"rate":120,"trend":"flat","event":"none"},{"date":%q,"trend":"flat"}]`,
				yesterday, today),
			want: []DailyRate{
				{Date: yesterday, Rate: 120, Trend: TrendFlat, Event: EventNone},
				{Date: today, Rate: 120, Trend: TrendFlat, Event: EventNone},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, path := newTempStore(t)
			// fixedRand{0.5}: no trend flip (0.5 >= 0.15), no event (0.5 >= 0.05),
			// flat drift 0 and noise -3+0.5*6 = 0, so a regenerated day keeps the
			// previous rate. Deterministic without pinning a draw count.
			st.rng = fixedRand{float: 0.5}
			writeHandEditedFile(t, path, fmt.Sprintf(`{"guild1":{"rates":%s,"users":{}}}`, tc.rates))

			got, err := st.EnsureTodayRate("guild1", fixedNow)
			if err != nil {
				t.Fatalf("EnsureTodayRate returned error: %v", err)
			}
			if want := tc.want[len(tc.want)-1]; got != want {
				t.Fatalf("EnsureTodayRate = %+v, want %+v — an out-of-clamp record must count as \"no rate for today\" and be regenerated", got, want)
			}
			if got.Rate < minRate || got.Rate > maxRate {
				t.Fatalf("returned rate %d escapes the [%d, %d] clamp", got.Rate, minRate, maxRate)
			}

			economy := readGuild(t, path, "guild1")
			if len(economy.Rates) != len(tc.want) {
				t.Fatalf("persisted rates = %+v, want %+v — the corrupt record must be replaced, not left behind as a duplicate date", economy.Rates, tc.want)
			}
			for i := range tc.want {
				if economy.Rates[i] != tc.want[i] {
					t.Fatalf("persisted rate %d = %+v, want %+v", i, economy.Rates[i], tc.want[i])
				}
			}
		})
	}
}

// TestStore_EnsureTodayRate_ClockGoingBackwards_KeepsSettledRates pins that
// the rate history only ever moves FORWARD. The "is the last entry today?"
// check alone reads a clock that has gone backwards (an NTP correction, a
// hand-set system time, a host whose zone data is repaired at boot) as "no
// rate for that day yet", so D→D+1→D→D+1 appended D,D+1,D,D+1: day D got a
// second, different rate after /exchange had already paid out at the first.
func TestStore_EnsureTodayRate_ClockGoingBackwards_KeepsSettledRates(t *testing.T) {
	st, path := newTempStore(t)
	// 0.9: no trend flip (>= 0.15), no event (>= 0.05), noise -3+0.9*6 = +2.4%.
	st.rng = fixedRand{float: 0.9}

	day1, err := st.EnsureTodayRate("guild1", fixedNow)
	if err != nil {
		t.Fatalf("EnsureTodayRate for day 1 returned error: %v", err)
	}
	day2, err := st.EnsureTodayRate("guild1", daysAfter(1))
	if err != nil {
		t.Fatalf("EnsureTodayRate for day 2 returned error: %v", err)
	}

	back, err := st.EnsureTodayRate("guild1", fixedNow)
	if err != nil {
		t.Fatalf("EnsureTodayRate after the clock went back returned error: %v", err)
	}
	if back != day1 {
		t.Fatalf("day 1 re-read as %+v, want the rate it already settled on %+v", back, day1)
	}
	forward, err := st.EnsureTodayRate("guild1", daysAfter(1))
	if err != nil {
		t.Fatalf("EnsureTodayRate after the clock caught up returned error: %v", err)
	}
	if forward != day2 {
		t.Fatalf("day 2 re-read as %+v, want the rate it already settled on %+v", forward, day2)
	}

	economy := readGuild(t, path, "guild1")
	want := []DailyRate{day1, day2}
	if len(economy.Rates) != len(want) {
		t.Fatalf("persisted rates = %+v, want exactly %+v — a settled day must never be appended twice", economy.Rates, want)
	}
	for i := range want {
		if economy.Rates[i] != want[i] {
			t.Fatalf("persisted rate %d = %+v, want %+v", i, economy.Rates[i], want[i])
		}
	}
}

// TestStore_EnsureTodayRate_DateOlderThanHistory_ReturnsNewest covers the
// other half of "the history only moves forward": a date the history skipped
// over entirely (no entry of its own) must report the newest settled rate
// instead of being spliced in behind it.
func TestStore_EnsureTodayRate_DateOlderThanHistory_ReturnsNewest(t *testing.T) {
	st, path := newTempStore(t)
	seedRate(t, st, "guild1", daysAfter(3), 120)
	st.rng = forbiddenRand{t: t, reason: "a date the history has already passed is answered from the history, never drawn"}

	got, err := st.EnsureTodayRate("guild1", fixedNow)
	if err != nil {
		t.Fatalf("EnsureTodayRate returned error: %v", err)
	}
	want := DailyRate{Date: jstDate(daysAfter(3)), Rate: 120, Trend: TrendFlat, Event: EventNone}
	if got != want {
		t.Fatalf("EnsureTodayRate for a skipped-over day = %+v, want the newest settled %+v", got, want)
	}

	economy := readGuild(t, path, "guild1")
	if len(economy.Rates) != 1 || economy.Rates[0] != want {
		t.Fatalf("persisted rates = %+v, want the untouched %+v", economy.Rates, want)
	}
}

// TestStore_ExchangeAndRecentRates_AgreeAfterClockGoesBackwards is the
// user-visible half of the rule: /exchange charges the rate
// ensureTodayRateLocked returns, while /rate headlines the LAST element of
// RecentRates. On a day the history has already passed, those two must still
// be the same number — otherwise the bot quotes one rate and bills another.
func TestStore_ExchangeAndRecentRates_AgreeAfterClockGoesBackwards(t *testing.T) {
	st, _ := newTempStore(t)
	st.rng = fixedRand{float: 0.9}

	day1, err := st.EnsureTodayRate("guild1", fixedNow)
	if err != nil {
		t.Fatalf("EnsureTodayRate for day 1 returned error: %v", err)
	}
	day2, err := st.EnsureTodayRate("guild1", daysAfter(1))
	if err != nil {
		t.Fatalf("EnsureTodayRate for day 2 returned error: %v", err)
	}
	if day2.Rate == day1.Rate {
		t.Fatalf("day 2 drew the same rate %d as day 1 — the test cannot tell the two days apart", day2.Rate)
	}
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Coins: 100}})

	// The clock goes back to day 1, which the history has already passed.
	exchange, err := st.ExchangeCoinToChip("guild1", "user-1", 10, fixedNow)
	if err != nil {
		t.Fatalf("ExchangeCoinToChip returned error: %v", err)
	}
	history, err := st.RecentRates("guild1", fixedNow, 7)
	if err != nil {
		t.Fatalf("RecentRates returned error: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("RecentRates returned an empty history")
	}
	headline := history[len(history)-1]
	if exchange.RateUsed != headline.Rate {
		t.Fatalf("/exchange charged %d while /rate headlines %d (history %+v) — both read the same day", exchange.RateUsed, headline.Rate, history)
	}
	if exchange.RateUsed != day1.Rate {
		t.Fatalf("/exchange charged %d on day 1, want the rate day 1 settled on %d", exchange.RateUsed, day1.Rate)
	}
}

// seedRates writes a rate history straight into guildID, bypassing
// ensureTodayRateIndexLocked so a test can start from a history that only the
// pre-fix code could have produced (days stored out of order).
func seedRates(t *testing.T, st *Store, guildID string, rates []DailyRate) {
	t.Helper()
	err := st.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		economy.Rates = append([]DailyRate(nil), rates...)
		return nil
	})
	if err != nil {
		t.Fatalf("seeding rates for %s: %v", guildID, err)
	}
}

// TestStore_EnsureTodayRate_ReusesSettledDayInOutOfOrderHistory covers the
// histories the pre-fix code already wrote to disk: D, D+1, D. Asking for D+1
// again must hand back the D+1 ALREADY stored, not draw a second one — the
// tail (D) says nothing about whether D+1 has been settled, and /exchange has
// already paid out at the stored value.
func TestStore_EnsureTodayRate_ReusesSettledDayInOutOfOrderHistory(t *testing.T) {
	st, path := newTempStore(t)
	day1, day2 := jstDate(fixedNow), jstDate(daysAfter(1))
	seeded := []DailyRate{
		{Date: day1, Rate: 100, Trend: TrendFlat, Event: EventNone},
		{Date: day2, Rate: 102, Trend: TrendBull, Event: EventNone},
		{Date: day1, Rate: 104, Trend: TrendBull, Event: EventNone},
	}
	seedRates(t, st, "guild1", seeded)
	st.rng = fixedRand{float: 0.9}

	got, err := st.EnsureTodayRate("guild1", daysAfter(1))
	if err != nil {
		t.Fatalf("EnsureTodayRate returned error: %v", err)
	}
	if got != seeded[1] {
		t.Fatalf("EnsureTodayRate for an already settled day = %+v, want the stored %+v", got, seeded[1])
	}
	economy := readGuild(t, path, "guild1")
	if len(economy.Rates) != len(seeded) {
		t.Fatalf("persisted rates = %+v, want the untouched %+v", economy.Rates, seeded)
	}
	for i, want := range seeded {
		if economy.Rates[i] != want {
			t.Fatalf("persisted rate %d = %+v, want the untouched %+v", i, economy.Rates[i], want)
		}
	}

	// /exchange and /rate must quote that same stored rate.
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Coins: 100}})
	exchange, err := st.ExchangeCoinToChip("guild1", "user-1", 10, daysAfter(1))
	if err != nil {
		t.Fatalf("ExchangeCoinToChip returned error: %v", err)
	}
	if exchange.RateUsed != seeded[1].Rate {
		t.Fatalf("/exchange charged %d, want the settled %d", exchange.RateUsed, seeded[1].Rate)
	}
	history, err := st.RecentRates("guild1", daysAfter(1), 7)
	if err != nil {
		t.Fatalf("RecentRates returned error: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("RecentRates returned an empty history")
	}
	if headline := history[len(history)-1]; headline != seeded[1] {
		t.Fatalf("/rate headlines %+v while /exchange charged %d (history %+v)", headline, exchange.RateUsed, history)
	}
}

func TestStore_EnsureCasinoAccess_FirstAccessGrantsWelcomeBonus(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "a guild's first day is the fixed 基準100, not a draw"}

	if err := st.EnsureCasinoAccess("guild1", "user-1", fixedNow); err != nil {
		t.Fatalf("EnsureCasinoAccess returned error: %v", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != welcomeBonusChips {
		t.Fatalf("Chips = %d, want the %d-chip welcome bonus", account.Chips, welcomeBonusChips)
	}
	if account.Coins != 0 {
		t.Fatalf("Coins = %d, want a new account to start with no coins", account.Coins)
	}
}

func TestStore_EnsureCasinoAccess_Idempotent_NoDoubleBonus(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "today's rate already exists after the first call"}

	for i := 0; i < 3; i++ {
		if err := st.EnsureCasinoAccess("guild1", "user-1", fixedNow); err != nil {
			t.Fatalf("EnsureCasinoAccess call %d returned error: %v", i+1, err)
		}
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != welcomeBonusChips {
		t.Fatalf("Chips = %d after three calls, want the welcome bonus granted exactly once (%d)", account.Chips, welcomeBonusChips)
	}
	economy := readGuild(t, path, "guild1")
	if len(economy.Rates) != 1 {
		t.Fatalf("persisted %d rate entries after three calls on one JST day, want 1", len(economy.Rates))
	}
}

func TestStore_EnsureCasinoAccess_GeneratesTodayRate(t *testing.T) {
	st, path := newTempStore(t)

	if err := st.EnsureCasinoAccess("guild1", "user-1", fixedNow); err != nil {
		t.Fatalf("EnsureCasinoAccess returned error: %v", err)
	}

	economy := readGuild(t, path, "guild1")
	if len(economy.Rates) != 1 {
		t.Fatalf("persisted rates = %+v, want today's rate to have been generated", economy.Rates)
	}
	if economy.Rates[0].Date != jstDate(fixedNow) || economy.Rates[0].Rate != baseRate {
		t.Fatalf("persisted rate = %+v, want today's date %q at the base rate %d", economy.Rates[0], jstDate(fixedNow), baseRate)
	}
}

// TestStore_EnsureCasinoAccess_ThenFailingOperation_KeepsAccountAndBonus is
// the reason EnsureCasinoAccess is its own transaction: if the account were
// opened inside the caller's Update, a failing main operation would roll the
// new account and its welcome bonus back, and "which command you happened to
// run first" would change the outcome.
func TestStore_EnsureCasinoAccess_ThenFailingOperation_KeepsAccountAndBonus(t *testing.T) {
	st, path := newTempStore(t)

	if err := st.EnsureCasinoAccess("guild1", "user-1", fixedNow); err != nil {
		t.Fatalf("EnsureCasinoAccess returned error: %v", err)
	}

	_, err := st.ExchangeCoinToChip("guild1", "user-1", 10, fixedNow)
	var insufficient *ErrInsufficientCoins
	if !errors.As(err, &insufficient) {
		t.Fatalf("ExchangeCoinToChip on a coinless new account = %v, want *ErrInsufficientCoins", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != welcomeBonusChips {
		t.Fatalf("Chips = %d after the failing exchange, want the welcome bonus to survive it (%d)", account.Chips, welcomeBonusChips)
	}
	economy := readGuild(t, path, "guild1")
	if len(economy.Rates) != 1 {
		t.Fatalf("persisted rates = %+v, want today's rate to survive the failing exchange", economy.Rates)
	}
}

func TestStore_ClaimDaily_FirstClaim(t *testing.T) {
	st, path := newTempStore(t)

	result, err := st.at(fixedNow).ClaimDaily("guild1", "user-1")
	if err != nil {
		t.Fatalf("ClaimDaily returned error: %v", err)
	}
	if result.Amount != 200 || result.NewStreak != 1 || result.JackpotBonus {
		t.Fatalf("result = %+v, want {Amount:200 NewStreak:1 JackpotBonus:false}", result)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != welcomeBonusChips+200 {
		t.Fatalf("Chips = %d, want the welcome bonus plus 200 (%d)", account.Chips, welcomeBonusChips+200)
	}
	if account.StreakDays != 1 || account.LastDailyDate != jstDate(fixedNow) {
		t.Fatalf("account = %+v, want StreakDays 1 and LastDailyDate %q", account, jstDate(fixedNow))
	}
}

func TestStore_ClaimDaily_SameDayTwice(t *testing.T) {
	st, path := newTempStore(t)
	if _, err := st.at(fixedNow).ClaimDaily("guild1", "user-1"); err != nil {
		t.Fatalf("first ClaimDaily returned error: %v", err)
	}

	// Later the same JST calendar day.
	if _, err := st.at(fixedNow.Add(8*time.Hour)).ClaimDaily("guild1", "user-1"); !errors.Is(err, ErrAlreadyClaimedToday) {
		t.Fatalf("second ClaimDaily on the same JST day = %v, want ErrAlreadyClaimedToday", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != welcomeBonusChips+200 || account.StreakDays != 1 {
		t.Fatalf("a rejected claim must persist nothing, got %+v", account)
	}
}

func TestStore_ClaimDaily_ConsecutiveDay(t *testing.T) {
	st, path := newTempStore(t)
	if _, err := st.at(fixedNow).ClaimDaily("guild1", "user-1"); err != nil {
		t.Fatalf("day 1 ClaimDaily returned error: %v", err)
	}

	result, err := st.at(daysAfter(1)).ClaimDaily("guild1", "user-1")
	if err != nil {
		t.Fatalf("day 2 ClaimDaily returned error: %v", err)
	}
	if result.Amount != 250 || result.NewStreak != 2 || result.JackpotBonus {
		t.Fatalf("result = %+v, want {Amount:250 NewStreak:2 JackpotBonus:false}", result)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != welcomeBonusChips+200+250 {
		t.Fatalf("Chips = %d, want %d", account.Chips, welcomeBonusChips+200+250)
	}
}

func TestStore_ClaimDaily_GapResetsStreak(t *testing.T) {
	st, path := newTempStore(t)
	if _, err := st.at(fixedNow).ClaimDaily("guild1", "user-1"); err != nil {
		t.Fatalf("day 1 ClaimDaily returned error: %v", err)
	}
	if _, err := st.at(daysAfter(1)).ClaimDaily("guild1", "user-1"); err != nil {
		t.Fatalf("day 2 ClaimDaily returned error: %v", err)
	}

	// Day 3 skipped entirely; claim again on day 4.
	result, err := st.at(daysAfter(3)).ClaimDaily("guild1", "user-1")
	if err != nil {
		t.Fatalf("day 4 ClaimDaily returned error: %v", err)
	}
	if result.NewStreak != 1 || result.Amount != 200 {
		t.Fatalf("result = %+v, want the streak reset to 1 (200 chips) after a missed day", result)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.StreakDays != 1 {
		t.Fatalf("StreakDays = %d, want 1", account.StreakDays)
	}
}

func TestStore_ClaimDaily_SeventhDayJackpot(t *testing.T) {
	st, path := newTempStore(t)

	want := []int64{200, 250, 300, 350, 400, 450, 1000}
	var total int64
	for day := 0; day < 7; day++ {
		result, err := st.at(daysAfter(day)).ClaimDaily("guild1", "user-1")
		if err != nil {
			t.Fatalf("day %d ClaimDaily returned error: %v", day+1, err)
		}
		if result.Amount != want[day] || result.NewStreak != day+1 {
			t.Fatalf("day %d result = %+v, want {Amount:%d NewStreak:%d}", day+1, result, want[day], day+1)
		}
		if wantJackpot := day == 6; result.JackpotBonus != wantJackpot {
			t.Fatalf("day %d JackpotBonus = %v, want %v (the 大入り袋 lands on every 7th day)", day+1, result.JackpotBonus, wantJackpot)
		}
		total += want[day]
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != welcomeBonusChips+total {
		t.Fatalf("Chips = %d, want %d", account.Chips, welcomeBonusChips+total)
	}
}

// TestStore_ClaimDaily_DateNeverMovesBackwards is the regression for the
// day-boundary double claim: two /daily invocations that straddle midnight
// can reach the store in the reverse order of their instants, and a store
// that only rejects "same date as the last claim" pays out D, D+1, D, D+1 —
// four bonuses for two days. The claimed date must be monotonically
// non-decreasing, so the last two requests are rejected and the payout stops
// at 200+250=450.
func TestStore_ClaimDaily_DateNeverMovesBackwards(t *testing.T) {
	st, path := newTempStore(t)

	var total int64
	for _, step := range []struct {
		name string
		now  time.Time
	}{
		{"day D", fixedNow},
		{"day D+1", daysAfter(1)},
	} {
		result, err := st.at(step.now).ClaimDaily("guild1", "user-1")
		if err != nil {
			t.Fatalf("%s ClaimDaily returned error: %v", step.name, err)
		}
		total += result.Amount
	}
	if total != 450 {
		t.Fatalf("the two forward claims paid %d chips, want 450 (200+250)", total)
	}

	// The two stragglers: an instant from the day already claimed, then one
	// from the day whose claim already landed.
	for _, step := range []struct {
		name string
		now  time.Time
	}{
		{"day D again (backwards)", fixedNow},
		{"day D+1 again", daysAfter(1)},
	} {
		if _, err := st.at(step.now).ClaimDaily("guild1", "user-1"); !errors.Is(err, ErrAlreadyClaimedToday) {
			t.Fatalf("%s = %v, want ErrAlreadyClaimedToday — the claimed date must never move backwards", step.name, err)
		}
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != welcomeBonusChips+450 {
		t.Fatalf("Chips = %d, want %d — the rejected claims must pay nothing", account.Chips, welcomeBonusChips+450)
	}
	if account.StreakDays != 2 || account.LastDailyDate != jstDate(daysAfter(1)) {
		t.Fatalf("account = %+v, want StreakDays 2 and LastDailyDate %q", account, jstDate(daysAfter(1)))
	}
}

func TestStore_ClaimDaily_ChipCapExceeded_NoMutation(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Chips: MaxChips, Coins: 7}})

	if _, err := st.at(fixedNow).ClaimDaily("guild1", "user-1"); !errors.Is(err, ErrChipCapExceeded) {
		t.Fatalf("ClaimDaily at MaxChips = %v, want ErrChipCapExceeded", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != MaxChips || account.Coins != 7 || account.StreakDays != 0 || account.LastDailyDate != "" {
		t.Fatalf("a cap-exceeded claim must persist nothing — not the chips, not the streak, not the date: %+v", account)
	}
}

// TestStore_ClaimDaily_NonJSTLocation_StreakFollowsJSTCalendar pins the JST
// calendar-day boundary against a `now` that arrives in a foreign zone. The
// America/New_York case is the one that actually discriminates: on the
// 2026-03-08 spring-forward day, subtracting a day in the caller's OWN
// location (jstDate(now.AddDate(0,0,-1))) yields 2026-03-08 instead of
// 2026-03-07, which silently breaks the streak.
func TestStore_ClaimDaily_NonJSTLocation_StreakFollowsJSTCalendar(t *testing.T) {
	type streakCase struct {
		name       string
		first      time.Time
		second     time.Time
		wantDate   string
		wantStreak int
	}

	// The fixed-offset pair always runs: time.LoadLocation needs a tzdata file,
	// which a minimal container may not have, and a skipped test proves nothing.
	est := time.FixedZone("EST", -5*3600)
	cases := []streakCase{{
		name:       "fixed -05:00 offset",
		first:      time.Date(2026, 3, 6, 23, 0, 0, 0, est), // JST 2026-03-07 13:00
		second:     time.Date(2026, 3, 7, 23, 0, 0, 0, est), // JST 2026-03-08 13:00
		wantDate:   "2026-03-08",
		wantStreak: 2,
	}}

	if ny, err := time.LoadLocation("America/New_York"); err == nil {
		cases = append(cases, streakCase{
			name:       "America/New_York across the spring-forward transition",
			first:      time.Date(2026, 3, 6, 23, 0, 0, 0, ny),  // EST: JST 2026-03-07 13:00
			second:     time.Date(2026, 3, 8, 10, 30, 0, 0, ny), // EDT: JST 2026-03-08 23:30
			wantDate:   "2026-03-08",
			wantStreak: 2,
		})
	} else {
		t.Logf("time.LoadLocation(\"America/New_York\") failed (%v); running only the fixed-offset case", err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, path := newTempStore(t)

			first, err := st.at(tc.first).ClaimDaily("guild1", "user-1")
			if err != nil {
				t.Fatalf("first ClaimDaily returned error: %v", err)
			}
			if first.NewStreak != 1 {
				t.Fatalf("first claim NewStreak = %d, want 1", first.NewStreak)
			}

			second, err := st.at(tc.second).ClaimDaily("guild1", "user-1")
			if err != nil {
				t.Fatalf("second ClaimDaily returned error: %v", err)
			}
			if second.NewStreak != tc.wantStreak {
				t.Fatalf("second claim NewStreak = %d, want %d — consecutive JST calendar days must continue the streak whatever zone `now` carries", second.NewStreak, tc.wantStreak)
			}

			account := readAccount(t, path, "guild1", "user-1")
			if account.LastDailyDate != tc.wantDate {
				t.Fatalf("LastDailyDate = %q, want the JST calendar date %q", account.LastDailyDate, tc.wantDate)
			}
		})
	}
}

func TestStore_ExchangeCoinToChip_Success(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Coins: 10, Chips: 0}})
	seedRate(t, st, "guild1", fixedNow, 101)

	result, err := st.ExchangeCoinToChip("guild1", "user-1", 10, fixedNow)
	if err != nil {
		t.Fatalf("ExchangeCoinToChip returned error: %v", err)
	}
	// 10 * 101 * 97 / 100 = 979, with exactly one truncation.
	if result != (ExchangeResult{Spent: 10, Received: 979, RateUsed: 101}) {
		t.Fatalf("result = %+v, want {Spent:10 Received:979 RateUsed:101}", result)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Coins != 0 || account.Chips != 979 {
		t.Fatalf("account = %+v, want Coins 0 and Chips 979", account)
	}
}

func TestStore_ExchangeCoinToChip_InsufficientCoins_NoMutation(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Coins: 5, Chips: 42}})
	seedRate(t, st, "guild1", fixedNow, 101)

	_, err := st.ExchangeCoinToChip("guild1", "user-1", 10, fixedNow)
	var insufficient *ErrInsufficientCoins
	if !errors.As(err, &insufficient) {
		t.Fatalf("ExchangeCoinToChip = %v, want *ErrInsufficientCoins", err)
	}
	if insufficient.Balance != 5 {
		t.Fatalf("ErrInsufficientCoins.Balance = %d, want the current balance 5", insufficient.Balance)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Coins != 5 || account.Chips != 42 {
		t.Fatalf("a rejected exchange must not move a single coin or chip, got %+v", account)
	}
}

func TestStore_ExchangeCoinToChip_BelowMinimumInput(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Coins: 500, Chips: 42}})
	seedRate(t, st, "guild1", fixedNow, 101)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the seeded file: %v", err)
	}

	for _, coins := range []int64{0, -1} {
		if _, err := st.ExchangeCoinToChip("guild1", "user-1", coins, fixedNow); !errors.Is(err, ErrInvalidAmount) {
			t.Fatalf("ExchangeCoinToChip(%d) = %v, want ErrInvalidAmount", coins, err)
		}
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the file afterwards: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("a rejected exchange must persist nothing.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestStore_ExchangeChipToCoin_Success(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Coins: 0, Chips: 1100}})
	seedRate(t, st, "guild1", fixedNow, 70)

	result, err := st.ExchangeChipToCoin("guild1", "user-1", 1100, fixedNow)
	if err != nil {
		t.Fatalf("ExchangeChipToCoin returned error: %v", err)
	}
	// 1100 * 97 / (100 * 70) = 15, with exactly one truncation; converting
	// first and charging the fee afterwards would pay 14.
	if result != (ExchangeResult{Spent: 1100, Received: 15, RateUsed: 70}) {
		t.Fatalf("result = %+v, want {Spent:1100 Received:15 RateUsed:70}", result)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != 0 || account.Coins != 15 {
		t.Fatalf("account = %+v, want Chips 0 and Coins 15", account)
	}
}

func TestStore_ExchangeChipToCoin_InsufficientChips_NoMutation(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Coins: 3, Chips: 900}})
	seedRate(t, st, "guild1", fixedNow, 70)

	_, err := st.ExchangeChipToCoin("guild1", "user-1", 1100, fixedNow)
	var insufficient *ErrInsufficientChips
	if !errors.As(err, &insufficient) {
		t.Fatalf("ExchangeChipToCoin = %v, want *ErrInsufficientChips", err)
	}
	if insufficient.Balance != 900 {
		t.Fatalf("ErrInsufficientChips.Balance = %d, want the current balance 900", insufficient.Balance)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != 900 || account.Coins != 3 {
		t.Fatalf("a rejected exchange must not move a single coin or chip, got %+v", account)
	}
}

func TestStore_ExchangeChipToCoin_ResultBelowMinimum_NoMutation(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Coins: 3, Chips: 900}})
	seedRate(t, st, "guild1", fixedNow, 140)

	// 100 * 97 / (100 * 140) = 0: the minimum is on the RESULT here, not on
	// the input, so this is ErrBelowMinimumExchange rather than
	// ErrInvalidAmount.
	if _, err := st.ExchangeChipToCoin("guild1", "user-1", 100, fixedNow); !errors.Is(err, ErrBelowMinimumExchange) {
		t.Fatalf("ExchangeChipToCoin(100) at rate 140 = %v, want ErrBelowMinimumExchange", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != 900 || account.Coins != 3 {
		t.Fatalf("a rejected exchange must not move a single coin or chip, got %+v", account)
	}
}

func TestStore_ExchangeCoinToChip_ChipCapExceeded_NoMutation(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Coins: 10, Chips: MaxChips - 1}})
	seedRate(t, st, "guild1", fixedNow, 100)

	// 10 * 100 * 97 / 100 = 970 chips, which does not fit under MaxChips.
	if _, err := st.ExchangeCoinToChip("guild1", "user-1", 10, fixedNow); !errors.Is(err, ErrChipCapExceeded) {
		t.Fatalf("ExchangeCoinToChip = %v, want ErrChipCapExceeded", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Coins != 10 || account.Chips != MaxChips-1 {
		t.Fatalf("a cap-exceeded exchange must not persist the debit either, got %+v", account)
	}
}

func TestStore_ExchangeChipToCoin_CoinCapExceeded_NoMutation(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Coins: MaxCoins - 1, Chips: 1000}})
	seedRate(t, st, "guild1", fixedNow, 70)

	// 1000 * 97 / (100 * 70) = 13 coins, which does not fit under MaxCoins.
	if _, err := st.ExchangeChipToCoin("guild1", "user-1", 1000, fixedNow); !errors.Is(err, ErrCoinCapExceeded) {
		t.Fatalf("ExchangeChipToCoin = %v, want ErrCoinCapExceeded", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Coins != MaxCoins-1 || account.Chips != 1000 {
		t.Fatalf("a cap-exceeded exchange must not persist the debit either, got %+v", account)
	}
}

// TestStore_ExchangeCoinToChip_ZeroRatePersistedEntry_DoesNotPanic is the
// end-to-end half of the divide-by-zero regression: a today record with no
// "rate" field unmarshals as Rate 0, and exchangeCoinToChip's overflow guard
// divides by rate*97. The exchange must complete against a regenerated rate
// instead of taking the process down with it.
func TestStore_ExchangeCoinToChip_ZeroRatePersistedEntry_DoesNotPanic(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = fixedRand{float: 0.5}
	writeHandEditedFile(t, path, fmt.Sprintf(
		`{"guild1":{"rates":[{"date":%q,"trend":"flat"}],"users":{"user-1":{"coins":10,"chips":0}}}}`,
		jstDate(fixedNow)))

	result, err := st.ExchangeCoinToChip("guild1", "user-1", 10, fixedNow)
	if err != nil {
		t.Fatalf("ExchangeCoinToChip returned error: %v", err)
	}

	// The corrupt record was the guild's only one, so it regenerates to the
	// day-1 基準100: 10 * 100 * 97 / 100 = 970 chips.
	if result.RateUsed != baseRate || result.Spent != 10 || result.Received != 970 {
		t.Fatalf("result = %+v, want {Spent:10 Received:970 RateUsed:%d}", result, baseRate)
	}
	account := readAccount(t, path, "guild1", "user-1")
	if account.Coins != 0 || account.Chips != 970 {
		t.Fatalf("persisted account = %+v, want {Coins:0 Chips:970}", account)
	}
}

// TestStore_ExchangeChipToCoin_ZeroRatePersistedEntry_DoesNotPanic covers the
// other direction, whose divisor is 100*rate in the payout expression itself.
func TestStore_ExchangeChipToCoin_ZeroRatePersistedEntry_DoesNotPanic(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = fixedRand{float: 0.5}
	writeHandEditedFile(t, path, fmt.Sprintf(
		`{"guild1":{"rates":[{"date":%q,"trend":"flat"}],"users":{"user-1":{"coins":0,"chips":1000}}}}`,
		jstDate(fixedNow)))

	result, err := st.ExchangeChipToCoin("guild1", "user-1", 1000, fixedNow)
	if err != nil {
		t.Fatalf("ExchangeChipToCoin returned error: %v", err)
	}

	// Regenerated to 基準100: 1000 * 97 / (100 * 100) = 9 coins.
	if result.RateUsed != baseRate || result.Spent != 1000 || result.Received != 9 {
		t.Fatalf("result = %+v, want {Spent:1000 Received:9 RateUsed:%d}", result, baseRate)
	}
	account := readAccount(t, path, "guild1", "user-1")
	if account.Coins != 9 || account.Chips != 0 {
		t.Fatalf("persisted account = %+v, want {Coins:9 Chips:0}", account)
	}
}

func TestStore_TopAssets_SortsDescendingTieBrokenByUserID(t *testing.T) {
	st, _ := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{
		"user-a": {Chips: 100, Coins: 1}, // 100 + 1*100 = 200
		"user-b": {Chips: 200, Coins: 0}, // 200, tied with user-a
		"user-c": {Chips: 0, Coins: 5},   // 500
	})
	seedRate(t, st, "guild1", fixedNow, 100)

	entries, err := st.TopAssets("guild1", fixedNow, 10)
	if err != nil {
		t.Fatalf("TopAssets returned error: %v", err)
	}
	want := []RankEntry{
		{UserID: "user-c", TotalAssets: 500},
		{UserID: "user-a", TotalAssets: 200},
		{UserID: "user-b", TotalAssets: 200},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v (descending by assets, ties broken by ascending user ID)", i, entries[i], want[i])
		}
	}
}

func TestStore_TopAssets_LimitsToRequestedCount(t *testing.T) {
	st, _ := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{
		"user-a": {Chips: 100, Coins: 1},
		"user-b": {Chips: 200, Coins: 0},
		"user-c": {Chips: 0, Coins: 5},
	})
	seedRate(t, st, "guild1", fixedNow, 100)

	entries, err := st.TopAssets("guild1", fixedNow, 2)
	if err != nil {
		t.Fatalf("TopAssets returned error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries for limit 2: %+v", len(entries), entries)
	}
	if entries[0].UserID != "user-c" || entries[1].UserID != "user-a" {
		t.Fatalf("entries = %+v, want the top two by assets", entries)
	}
}

// TestStore_TopAssets_MaxBalances_DoesNotOverflow is the int64 guard: without
// MaxChips/MaxCoins, Chips + Coins*rate wraps to a negative total and the
// ranking silently inverts, with no error or panic to notice.
func TestStore_TopAssets_MaxBalances_DoesNotOverflow(t *testing.T) {
	st, _ := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{
		"user-a": {Chips: MaxChips, Coins: MaxCoins},
		"user-b": {Chips: MaxChips, Coins: MaxCoins},
	})
	seedRate(t, st, "guild1", fixedNow, maxRate)

	entries, err := st.TopAssets("guild1", fixedNow, 10)
	if err != nil {
		t.Fatalf("TopAssets returned error: %v", err)
	}
	const want = int64(141_000_000_000_000) // MaxChips + MaxCoins*140
	for i, entry := range entries {
		if entry.TotalAssets != want {
			t.Fatalf("entry %d (%s) TotalAssets = %d, want %d — a negative or wrapped total means the caps stopped containing the arithmetic", i, entry.UserID, entry.TotalAssets, want)
		}
	}
	if len(entries) != 2 || entries[0].UserID != "user-a" {
		t.Fatalf("entries = %+v, want both users with the tie broken by ascending user ID", entries)
	}
}

// TestTopAssets_NilAccountDoesNotPanic covers the §3.3 nil-pointer path the
// existing nil-guild regression does NOT reach. ensureGuildLocked repairs the
// guild and its Users map, and ensureAccountLocked repairs only the ONE user a
// write path touches — so `{"users":{"someone":null}}` survives into the
// ranking loop, where account.Chips dereferences nil. discordgo runs handlers
// in goroutines without recover, so /rank would kill the bot and the bad file
// would still be there after the restart.
func TestTopAssets_NilAccountDoesNotPanic(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "today's rate is already in the file, so no draw may happen"}
	writeHandEditedFile(t, path, fmt.Sprintf(
		`{"guild1":{"rates":[{"date":%q,"rate":100,"trend":"flat","event":"none"}],`+
			`"users":{"ghost":null,"user-a":{"chips":100,"coins":1},"user-b":{"chips":50}}}}`,
		jstDate(fixedNow)))

	entries, err := st.TopAssets("guild1", fixedNow, 10)
	if err != nil {
		t.Fatalf("TopAssets returned error: %v", err)
	}

	want := []RankEntry{
		{UserID: "user-a", TotalAssets: 200}, // 100 + 1*100
		{UserID: "user-b", TotalAssets: 50},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d (the nil account must be skipped, not ranked): %+v", len(entries), len(want), entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, entries[i], want[i])
		}
	}

	// Skipped, not opened: ranking is a display-only read path, and granting
	// the 1,000-chip welcome bonus to someone who never ran a casino command
	// would let a /rank query mint currency. Account creation belongs to the
	// write path (ensureAccountLocked).
	economy := readGuild(t, path, "guild1")
	if account := economy.Users["ghost"]; account != nil {
		t.Fatalf("reading the ranking opened an account for the nil user: %+v", account)
	}
}

func TestStore_ViewAccount_FirstAccess_GrantsWelcomeBonus(t *testing.T) {
	st, path := newTempStore(t)

	view, err := st.ViewAccount("guild1", "user-1", fixedNow)
	if err != nil {
		t.Fatalf("ViewAccount returned error: %v", err)
	}
	if view.Account.Chips != welcomeBonusChips || view.Account.Coins != 0 {
		t.Fatalf("view.Account = %+v, want the %d-chip welcome bonus and no coins", view.Account, welcomeBonusChips)
	}
	if view.RateUsed != baseRate {
		t.Fatalf("view.RateUsed = %d, want the first day's %d", view.RateUsed, baseRate)
	}
	if view.TotalAssets != welcomeBonusChips {
		t.Fatalf("view.TotalAssets = %d, want %d", view.TotalAssets, welcomeBonusChips)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != welcomeBonusChips {
		t.Fatalf("persisted Chips = %d, want the welcome bonus to have been committed", account.Chips)
	}
}

func TestStore_RecentRates_GeneratesTodayIfMissing(t *testing.T) {
	st, _ := newTempStore(t)

	rates, err := st.RecentRates("guild1", fixedNow, 7)
	if err != nil {
		t.Fatalf("RecentRates returned error: %v", err)
	}
	if len(rates) != 1 {
		t.Fatalf("got %d entries, want today's freshly generated rate: %+v", len(rates), rates)
	}
	if rates[0].Date != jstDate(fixedNow) || rates[0].Rate != baseRate {
		t.Fatalf("rates[0] = %+v, want today's date %q at the base rate %d", rates[0], jstDate(fixedNow), baseRate)
	}
}

func TestStore_RecentRates_LimitsToRequestedCount(t *testing.T) {
	st, _ := newTempStore(t)
	for day := -4; day <= 0; day++ {
		seedRate(t, st, "guild1", daysAfter(day), 100+day)
	}

	rates, err := st.RecentRates("guild1", fixedNow, 3)
	if err != nil {
		t.Fatalf("RecentRates returned error: %v", err)
	}
	if len(rates) != 3 {
		t.Fatalf("got %d entries for limit 3: %+v", len(rates), rates)
	}
	if rates[2].Date != jstDate(fixedNow) {
		t.Fatalf("newest entry = %+v, want today's %q", rates[2], jstDate(fixedNow))
	}
}

func TestStore_RecentRates_ReturnsOldestToNewestOrder(t *testing.T) {
	st, _ := newTempStore(t)
	for day := -4; day <= 0; day++ {
		seedRate(t, st, "guild1", daysAfter(day), 100+day)
	}

	rates, err := st.RecentRates("guild1", fixedNow, 30)
	if err != nil {
		t.Fatalf("RecentRates returned error: %v", err)
	}
	if len(rates) != 5 {
		t.Fatalf("got %d entries, want all 5 seeded days: %+v", len(rates), rates)
	}
	for day := -4; day <= 0; day++ {
		i := day + 4
		if want := jstDate(daysAfter(day)); rates[i].Date != want {
			t.Fatalf("rates[%d].Date = %q, want %q — the history must come back oldest first, today last", i, rates[i].Date, want)
		}
	}
}

func TestStore_Spin_DeductsThenCreditsPayout(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, "guild1", "user-1", UserAccount{Chips: 1000})
	// Force 🍒🍒🍒 (×7): the deterministic win keeps the arithmetic below
	// exact instead of merely plausible.
	st.rng = &scriptedIntn{t: t, intns: []int{symbolDraw(t, SymbolCherry)}}

	result, err := st.Spin("guild1", "user-1", 100)
	if err != nil {
		t.Fatalf("Spin returned error: %v", err)
	}
	if result.Reels != [3]SlotSymbol{SymbolCherry, SymbolCherry, SymbolCherry} {
		t.Fatalf("Reels = %v, want 🍒🍒🍒 — Spin must draw from s.rng", result.Reels)
	}
	if result.Bet != 100 || result.Payout != 700 || result.IsJackpot {
		t.Fatalf("result = %+v, want Bet 100, Payout 700, IsJackpot false", result)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != 1600 {
		t.Fatalf("persisted Chips = %d, want 1600 (1000 - 100 bet + 700 payout) — the deduction and the credit must land in ONE transaction", account.Chips)
	}
}

func TestStore_Spin_InsufficientChips_NoDeduction(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, "guild1", "user-1", UserAccount{Chips: 50})
	st.rng = forbiddenRand{t: t, reason: "an unaffordable bet is refused before the reels are drawn"}

	_, err := st.Spin("guild1", "user-1", 100)
	var insufficient *ErrInsufficientChips
	if !errors.As(err, &insufficient) {
		t.Fatalf("Spin with a 100 bet on 50 chips = %v, want *ErrInsufficientChips", err)
	}
	if insufficient.Balance != 50 {
		t.Fatalf("ErrInsufficientChips.Balance = %d, want 50 (the command layer renders it without a second round-trip)", insufficient.Balance)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != 50 {
		t.Fatalf("persisted Chips = %d, want 50 — a refused bet must never be eaten", account.Chips)
	}
}

func TestStore_Spin_BetOutOfRange(t *testing.T) {
	for _, bet := range []int64{-1, 0, 9, 1001} {
		st, path := newTempStore(t)
		st.rng = forbiddenRand{t: t, reason: "a bet outside [10,1000] is rejected before any draw"}

		if _, err := st.Spin("guild1", "user-1", bet); !errors.Is(err, ErrBetOutOfRange) {
			t.Fatalf("Spin with bet %d = %v, want ErrBetOutOfRange", bet, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("bet %d: a rejected bet must persist nothing at all, but os.Stat(%q) = %v", bet, path, err)
		}
	}
}

func TestStore_Spin_BetAtRangeBoundaries_Accepted(t *testing.T) {
	for _, bet := range []int64{10, 1000} {
		st, _ := newTempStore(t)
		seedAccount(t, st, "guild1", "user-1", UserAccount{Chips: 5000})
		st.rng = &scriptedIntn{t: t, intns: []int{symbolDraw(t, SymbolLemon), symbolDraw(t, SymbolGrape), symbolDraw(t, SymbolBell)}}

		result, err := st.Spin("guild1", "user-1", bet)
		if err != nil {
			t.Fatalf("Spin with the boundary bet %d returned error: %v — [10,1000] is inclusive", bet, err)
		}
		if result.Bet != bet {
			t.Fatalf("result.Bet = %d, want %d", result.Bet, bet)
		}
	}
}

func TestStore_Spin_ChipCapExceeded_NoMutation(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, "guild1", "user-1", UserAccount{Chips: MaxChips})
	// 🍒🍒🍒 on a 10 bet pays 70, so the post-bet balance MaxChips-10 plus 70
	// breaks the cap and the whole transaction must roll back.
	st.rng = &scriptedIntn{t: t, intns: []int{symbolDraw(t, SymbolCherry)}}

	if _, err := st.Spin("guild1", "user-1", 10); !errors.Is(err, ErrChipCapExceeded) {
		t.Fatalf("Spin = %v, want ErrChipCapExceeded", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != MaxChips {
		t.Fatalf("persisted Chips = %d, want %d — a capped-out win must not eat the bet either", account.Chips, MaxChips)
	}
}

// TestStore_Spin_ConcurrentSpinsBySameUser_ConservesExactBalance is the
// balance-conservation regression for 設計書「同時ベットによる残高競合を構造的に
// 防ぐ」: with N goroutines betting on the same account, the final balance must
// equal starting - N*bet + Σpayout EXACTLY. A lost update (two closures reading
// the same pre-image) shows up here as a surplus.
func TestStore_Spin_ConcurrentSpinsBySameUser_ConservesExactBalance(t *testing.T) {
	dir := t.TempDir()
	st := New(filepath.Join(dir, "casino.json"))
	const guildID, userID = "guild1", "user1"
	const startingChips = int64(100000)
	const bet = int64(100)
	const n = 50

	if err := st.Update(func(d *Data) error {
		ensureAccountLocked(ensureGuildLocked(d, guildID), userID).Chips = startingChips
		return nil
	}); err != nil {
		t.Fatalf("seeding balance: %v", err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var totalPayout int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := st.Spin(guildID, userID, bet)
			if err != nil {
				t.Errorf("Spin: %v", err)
				return
			}
			mu.Lock()
			totalPayout += result.Payout
			mu.Unlock()
		}()
	}
	wg.Wait()

	snapshot, err := st.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	economy := snapshot[guildID]
	if economy == nil || economy.Users[userID] == nil {
		t.Fatalf("guild %q / user %q missing from persisted data: %+v", guildID, userID, snapshot)
	}
	finalChips := economy.Users[userID].Chips

	want := startingChips - n*bet + totalPayout
	if finalChips != want {
		t.Fatalf("lost update detected: got Chips=%d, want %d (starting=%d - n*bet=%d + totalPayout=%d) — Store.Update's mutex is not serializing concurrent Spin calls correctly",
			finalChips, want, startingChips, n*bet, totalPayout)
	}
}

// TestStore_ConcurrentMixedOperations_AcrossUsers_NoCorruption mirrors
// internal/store/events_test.go's concurrent-Save regression test (proven
// pattern in this codebase), but exercises the higher-level casino API
// surface (ClaimDaily/Spin mixed across N different users in the same guild)
// instead of a single raw write method.
func TestStore_ConcurrentMixedOperations_AcrossUsers_NoCorruption(t *testing.T) {
	dir := t.TempDir()
	st := New(filepath.Join(dir, "casino.json"))
	const guildID = "guild1"
	const n = 30
	// Pin the clock BEFORE the goroutines start: s.clock is written once here
	// and only read (under the lock) afterwards.
	st.at(time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC))

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			userID := fmt.Sprintf("user-%d", i)
			if _, err := st.ClaimDaily(guildID, userID); err != nil {
				t.Errorf("ClaimDaily(%s): %v", userID, err)
			}
			if _, err := st.Spin(guildID, userID, 50); err != nil {
				t.Errorf("Spin(%s): %v", userID, err)
			}
		}(i)
	}
	wg.Wait()

	snapshot, err := st.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	economy := snapshot[guildID]
	if economy == nil {
		t.Fatalf("guild %q missing from persisted data after %d concurrent goroutines: %+v", guildID, n, snapshot)
	}
	userCount := len(economy.Users)
	if userCount != n {
		t.Fatalf("expected %d distinct user accounts after %d concurrent goroutines, got %d — a lost update or file corruption dropped an account",
			n, n, userCount)
	}
}

// --- escrow (設計書 §3) ---------------------------------------------------
//
// The property under test in most of these is the CONSERVATION LAW: an
// account's Chips + Escrow may move only by the payout a settlement is
// handed. Staking, raising and refunding are moves between two fields of
// one account and must leave the sum untouched. assertHoldings checks the
// sum against the FILE, not against the in-memory store, so a path that
// forgets to persist half of a move fails here.

// escrowGuild / escrowUser are the fixed identifiers the escrow tests use.
const (
	escrowGuild = "guild1"
	escrowUser  = "user1"
)

// assertHoldings reads guildID/userID back through a fresh Store and checks
// the escrow trio and the conserved sum in one place.
func assertHoldings(t *testing.T, path, guildID, userID string, wantChips, wantEscrow int64, wantGame string) {
	t.Helper()
	account := readAccount(t, path, guildID, userID)
	if account.Chips != wantChips || account.Escrow != wantEscrow {
		t.Fatalf("holdings = Chips %d / Escrow %d, want Chips %d / Escrow %d", account.Chips, account.Escrow, wantChips, wantEscrow)
	}
	if account.Chips+account.Escrow != wantChips+wantEscrow {
		t.Fatalf("Chips+Escrow = %d, want %d", account.Chips+account.Escrow, wantChips+wantEscrow)
	}
	if account.EscrowGame != wantGame {
		t.Fatalf("EscrowGame = %q, want %q", account.EscrowGame, wantGame)
	}
	// EscrowOpenedAt must agree with the amount: a stamp left behind on a
	// settled account would make it look in-flight to anything reading it.
	if (account.EscrowOpenedAt != "") != (wantEscrow > 0) {
		t.Fatalf("EscrowOpenedAt = %q with Escrow %d — the two disagree", account.EscrowOpenedAt, account.Escrow)
	}
}

func TestOpenGame_StakesTheBetWithoutChangingHoldings(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})

	if err := st.OpenGame(escrowGuild, escrowUser, "highlow", 250, fixedNow); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}

	// 1000 chips became 750 free + 250 staked: the bet left the spendable
	// balance but not the player's holdings.
	assertHoldings(t, path, escrowGuild, escrowUser, 750, 250, "highlow")

	account := readAccount(t, path, escrowGuild, escrowUser)
	want := fixedNow.In(jst).Format(time.RFC3339)
	if account.EscrowOpenedAt != want {
		t.Fatalf("EscrowOpenedAt = %q, want %q (RFC3339 in JST)", account.EscrowOpenedAt, want)
	}
}

func TestOpenGame_SecondGameIsRefusedAndPersistsNothing(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})

	if err := st.OpenGame(escrowGuild, escrowUser, "highlow", 250, fixedNow); err != nil {
		t.Fatalf("first OpenGame returned error: %v", err)
	}
	err := st.OpenGame(escrowGuild, escrowUser, "blackjack", 100, fixedNow)
	if !errors.Is(err, ErrGameInProgress) {
		t.Fatalf("second OpenGame error = %v, want ErrGameInProgress", err)
	}

	// The refusal must not have touched the first game's stake — overwriting
	// it would strand 250 chips with no settlement able to return them.
	assertHoldings(t, path, escrowGuild, escrowUser, 750, 250, "highlow")
}

func TestOpenGame_RejectsBetsTheBalanceOrTheRulesDoNotAllow(t *testing.T) {
	tests := []struct {
		name    string
		chips   int64
		bet     int64
		wantErr error
	}{
		{name: "bet above balance", chips: 100, bet: 101, wantErr: &ErrInsufficientChips{Balance: 100}},
		{name: "zero bet", chips: 100, bet: 0, wantErr: ErrInvalidAmount},
		{name: "negative bet", chips: 100, bet: -50, wantErr: ErrInvalidAmount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, path := newTempStore(t)
			seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: tt.chips})

			err := st.OpenGame(escrowGuild, escrowUser, "highlow", tt.bet, fixedNow)
			if err == nil {
				t.Fatalf("OpenGame(%d) succeeded, want %v", tt.bet, tt.wantErr)
			}
			var insufficient *ErrInsufficientChips
			switch {
			case errors.As(tt.wantErr, &insufficient):
				var got *ErrInsufficientChips
				if !errors.As(err, &got) {
					t.Fatalf("OpenGame error = %v, want *ErrInsufficientChips", err)
				}
				if got.Balance != insufficient.Balance {
					t.Fatalf("reported balance = %d, want %d", got.Balance, insufficient.Balance)
				}
			default:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("OpenGame error = %v, want %v", err, tt.wantErr)
				}
			}
			// A refused open must not eat the bet.
			assertHoldings(t, path, escrowGuild, escrowUser, tt.chips, 0, "")
		})
	}
}

func TestAddToEscrow_RaisesTheStakeWithoutChangingHoldings(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})
	if err := st.OpenGame(escrowGuild, escrowUser, "blackjack", 100, fixedNow); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}

	if err := st.AddToEscrow(escrowGuild, escrowUser, 100); err != nil {
		t.Fatalf("AddToEscrow returned error: %v", err)
	}

	// The double moved another 100 across; holdings are still 1000.
	assertHoldings(t, path, escrowGuild, escrowUser, 800, 200, "blackjack")
}

func TestAddToEscrow_RefusalsPersistNothing(t *testing.T) {
	t.Run("no game in progress", func(t *testing.T) {
		st, path := newTempStore(t)
		seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})

		err := st.AddToEscrow(escrowGuild, escrowUser, 100)
		if !errors.Is(err, ErrNoGameInProgress) {
			t.Fatalf("AddToEscrow error = %v, want ErrNoGameInProgress", err)
		}
		assertHoldings(t, path, escrowGuild, escrowUser, 1000, 0, "")
	})

	t.Run("raise above the free balance", func(t *testing.T) {
		st, path := newTempStore(t)
		seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 150})
		if err := st.OpenGame(escrowGuild, escrowUser, "blackjack", 100, fixedNow); err != nil {
			t.Fatalf("OpenGame returned error: %v", err)
		}

		// 50 chips are free; the escrowed 100 is not available to raise with.
		var insufficient *ErrInsufficientChips
		err := st.AddToEscrow(escrowGuild, escrowUser, 100)
		if !errors.As(err, &insufficient) {
			t.Fatalf("AddToEscrow error = %v, want *ErrInsufficientChips", err)
		}
		if insufficient.Balance != 50 {
			t.Fatalf("reported balance = %d, want 50 (the free chips, not the holdings)", insufficient.Balance)
		}
		assertHoldings(t, path, escrowGuild, escrowUser, 50, 100, "blackjack")
	})
}

func TestSettleGame_PaysTheStakeAndWinningsAndClosesTheGame(t *testing.T) {
	tests := []struct {
		name      string
		bet       int64
		payout    int64
		wantChips int64
	}{
		// Holdings start at 1000 in every row, so wantChips is also the new
		// holdings: the sum moved by exactly (payout - bet).
		{name: "loss pays nothing", bet: 200, payout: 0, wantChips: 800},
		{name: "push returns the stake", bet: 200, payout: 200, wantChips: 1000},
		{name: "win pays stake plus winnings", bet: 200, payout: 400, wantChips: 1200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, path := newTempStore(t)
			seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})
			if err := st.OpenGame(escrowGuild, escrowUser, "highlow", tt.bet, fixedNow); err != nil {
				t.Fatalf("OpenGame returned error: %v", err)
			}

			result, err := st.SettleGame(escrowGuild, escrowUser, tt.payout)
			if err != nil {
				t.Fatalf("SettleGame returned error: %v", err)
			}
			if result.Chips != tt.wantChips || result.Payout != tt.payout {
				t.Fatalf("SettleResult = %+v, want Chips %d / Payout %d", result, tt.wantChips, tt.payout)
			}
			assertHoldings(t, path, escrowGuild, escrowUser, tt.wantChips, 0, "")
		})
	}
}

func TestSettleGame_SecondSettlementIsRefused(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})
	if err := st.OpenGame(escrowGuild, escrowUser, "highlow", 200, fixedNow); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}
	if _, err := st.SettleGame(escrowGuild, escrowUser, 400); err != nil {
		t.Fatalf("first SettleGame returned error: %v", err)
	}

	// Paying 400 again would mint 400 chips out of nothing.
	_, err := st.SettleGame(escrowGuild, escrowUser, 400)
	if !errors.Is(err, ErrNoGameInProgress) {
		t.Fatalf("second SettleGame error = %v, want ErrNoGameInProgress", err)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, 1200, 0, "")
}

func TestSettleGame_NegativePayoutIsRefusedAndKeepsTheEscrow(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})
	if err := st.OpenGame(escrowGuild, escrowUser, "highlow", 200, fixedNow); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}

	_, err := st.SettleGame(escrowGuild, escrowUser, -1)
	if !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("SettleGame(-1) error = %v, want ErrInvalidAmount", err)
	}
	// The stake must survive a refused settlement so it can be retried or
	// refunded — releasing it here would lose the chips entirely.
	assertHoldings(t, path, escrowGuild, escrowUser, 800, 200, "highlow")
}

func TestSettleGame_PayoutOverTheChipCapPersistsNothing(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: MaxChips})
	if err := st.OpenGame(escrowGuild, escrowUser, "highlow", 1000, fixedNow); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}

	// Free chips are MaxChips-1000; a payout of 2000 would land at
	// MaxChips+1000.
	_, err := st.SettleGame(escrowGuild, escrowUser, 2000)
	if !errors.Is(err, ErrChipCapExceeded) {
		t.Fatalf("SettleGame error = %v, want ErrChipCapExceeded", err)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, MaxChips-1000, 1000, "highlow")
}

func TestRefundStaleEscrows_ReturnsEveryStakeInEveryGuild(t *testing.T) {
	st, path := newTempStore(t)
	err := st.Update(func(d *Data) error {
		(*d)["guildA"] = &GuildEconomy{Users: map[string]*UserAccount{
			"u1": {Chips: 800, Escrow: 200, EscrowGame: "highlow", EscrowOpenedAt: "2026-09-14T12:00:00+09:00"},
			"u2": {Chips: 500}, // no game in flight
		}}
		(*d)["guildB"] = &GuildEconomy{Users: map[string]*UserAccount{
			"u3": {Chips: 0, Escrow: 1000, EscrowGame: "blackjack", EscrowOpenedAt: "2026-09-14T12:00:00+09:00"},
		}}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding returned error: %v", err)
	}

	count, err := st.RefundStaleEscrows(fixedNow)
	if err != nil {
		t.Fatalf("RefundStaleEscrows returned error: %v", err)
	}
	if count != 2 {
		t.Fatalf("refunded %d accounts, want 2 (the two with a stake, across both guilds)", count)
	}

	assertHoldings(t, path, "guildA", "u1", 1000, 0, "")
	assertHoldings(t, path, "guildA", "u2", 500, 0, "")
	assertHoldings(t, path, "guildB", "u3", 1000, 0, "")

	// Idempotent: a second startup finds nothing left to refund, so a
	// restart loop cannot pay the same stake twice.
	count, err = st.RefundStaleEscrows(fixedNow)
	if err != nil {
		t.Fatalf("second RefundStaleEscrows returned error: %v", err)
	}
	if count != 0 {
		t.Fatalf("second refund reported %d accounts, want 0", count)
	}
	assertHoldings(t, path, "guildA", "u1", 1000, 0, "")
}

func TestRefundStaleEscrows_SkipsHandEditedNulls(t *testing.T) {
	st, path := newTempStore(t)
	// {"g": null} and {"users":{"someone":null}} are both reachable from a
	// hand edit or a partial write. Dereferencing either kills the bot in a
	// discordgo handler goroutine and the bad file survives the crash — a
	// permanent crash loop, and this runs at startup where it would be a
	// boot loop.
	if err := os.WriteFile(path, []byte(`{"guildA":null,"guildB":{"users":{"u1":null,"u2":{"chips":100,"escrow":50}}}}`), 0o644); err != nil {
		t.Fatalf("writing hand-edited file: %v", err)
	}

	count, err := st.RefundStaleEscrows(fixedNow)
	if err != nil {
		t.Fatalf("RefundStaleEscrows returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("refunded %d accounts, want 1", count)
	}
	assertHoldings(t, path, "guildB", "u2", 150, 0, "")
}

func TestUserAccount_PreC2JSONWithoutEscrowFieldsLoadsAsNoGame(t *testing.T) {
	st, path := newTempStore(t)
	// Exactly the shape data/casino.json had before C-2: no escrow keys at
	// all. It must load as "no game in progress" and stay playable.
	if err := os.WriteFile(path, []byte(`{"guild1":{"users":{"user1":{"coins":5,"chips":1000,"last_daily_date":"2026-07-10","streak_days":3}}}}`), 0o644); err != nil {
		t.Fatalf("writing legacy file: %v", err)
	}

	account := readAccount(t, path, escrowGuild, escrowUser)
	if account.Escrow != 0 || account.EscrowGame != "" || account.EscrowOpenedAt != "" {
		t.Fatalf("legacy account loaded with escrow state: %+v", account)
	}
	if account.Chips != 1000 || account.Coins != 5 || account.StreakDays != 3 {
		t.Fatalf("legacy fields did not survive the round trip: %+v", account)
	}

	// And the account can open a game — i.e. the absent fields are genuinely
	// "no game", not an unreadable state.
	if err := st.OpenGame(escrowGuild, escrowUser, "highlow", 100, fixedNow); err != nil {
		t.Fatalf("OpenGame on a legacy account returned error: %v", err)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, 900, 100, "highlow")
}

func TestUserAccount_NoGameInProgressSerializesWithoutEscrowKeys(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading persisted file: %v", err)
	}
	// omitempty, so an account with no game in flight writes the same bytes
	// it did before C-2 — a downgrade or an external reader sees no change.
	for _, key := range []string{"escrow", "escrow_game", "escrow_opened_at"} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("persisted file contains %q for an account with no game: %s", key, raw)
		}
	}
}

func TestOpenGame_ConcurrentOpensForOneUserLeaveExactlyOneStake(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})

	// 50 goroutines race to open a game for the SAME account. The store's
	// single writer lock plus the Escrow>0 guard must let exactly one
	// through: two winners would mean the loser's stake overwrote the
	// winner's, stranding chips no settlement could return.
	const goroutines = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	otherErrs := []error{}
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := st.OpenGame(escrowGuild, escrowUser, "highlow", 100, fixedNow)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrGameInProgress):
			default:
				otherErrs = append(otherErrs, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(otherErrs) > 0 {
		t.Fatalf("unexpected errors from concurrent OpenGame: %v", otherErrs)
	}
	if succeeded != 1 {
		t.Fatalf("%d of %d concurrent OpenGame calls succeeded, want exactly 1", succeeded, goroutines)
	}
	// One stake of 100, and holdings still 1000.
	assertHoldings(t, path, escrowGuild, escrowUser, 900, 100, "highlow")
}

func TestTotalAssets_CountEscrowAsTheUsersOwnMoney(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000, Coins: 2})

	before, err := st.ViewAccount(escrowGuild, escrowUser, fixedNow)
	if err != nil {
		t.Fatalf("ViewAccount returned error: %v", err)
	}
	beforeRank, err := st.TopAssets(escrowGuild, fixedNow, 10)
	if err != nil {
		t.Fatalf("TopAssets returned error: %v", err)
	}

	if err := st.OpenGame(escrowGuild, escrowUser, "highlow", 400, fixedNow); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}

	after, err := st.ViewAccount(escrowGuild, escrowUser, fixedNow)
	if err != nil {
		t.Fatalf("ViewAccount returned error: %v", err)
	}
	afterRank, err := st.TopAssets(escrowGuild, fixedNow, 10)
	if err != nil {
		t.Fatalf("TopAssets returned error: %v", err)
	}

	// Staking a bet must not make the player poorer on /balance or in the
	// ranking — the chips are still theirs until the hand settles.
	if after.TotalAssets != before.TotalAssets {
		t.Fatalf("ViewAccount total assets moved from %d to %d across a bet", before.TotalAssets, after.TotalAssets)
	}
	if len(beforeRank) != 1 || len(afterRank) != 1 {
		t.Fatalf("ranking rows = %d before / %d after, want 1 each", len(beforeRank), len(afterRank))
	}
	if afterRank[0].TotalAssets != beforeRank[0].TotalAssets {
		t.Fatalf("ranking total assets moved from %d to %d across a bet", beforeRank[0].TotalAssets, afterRank[0].TotalAssets)
	}
	if after.Account.Escrow != 400 {
		t.Fatalf("AccountView.Account.Escrow = %d, want 400 (/balance needs the staked amount)", after.Account.Escrow)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, 600, 400, "highlow")
}

// --- the cap counts escrow (評価者の指摘 C2-02 P1) -------------------------
//
// Chips and Escrow are both the player's money, so a credit must see their
// SUM against MaxChips. When the cap looked at Chips alone, a stake in
// flight opened exactly as much room as it had vacated: a daily bonus or an
// exchange could fill Chips back to MaxChips, and the refund that followed
// then had nowhere to put the stake — chips vanished.

func TestClaimDaily_WhileStaked_CannotMintChipsOverTheCap(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: MaxChips})
	if err := st.OpenGame(escrowGuild, escrowUser, "highlow", 1000, fixedNow); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}

	// Holdings are already MaxChips (MaxChips-1000 free + 1000 staked), so
	// the bonus has no room even though Chips alone looks 1000 short.
	if _, err := st.ClaimDaily(escrowGuild, escrowUser); !errors.Is(err, ErrChipCapExceeded) {
		t.Fatalf("ClaimDaily = %v, want ErrChipCapExceeded", err)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, MaxChips-1000, 1000, "highlow")

	// And the stake still comes back whole at the next startup.
	count, err := st.RefundStaleEscrows(fixedNow)
	if err != nil {
		t.Fatalf("RefundStaleEscrows returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("refunded %d accounts, want 1", count)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, MaxChips, 0, "")
}

func TestExchangeCoinToChip_WhileStaked_CannotMintChipsOverTheCap(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: MaxChips, Coins: 10})
	if err := st.OpenGame(escrowGuild, escrowUser, "highlow", 1000, fixedNow); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}

	if _, err := st.ExchangeCoinToChip(escrowGuild, escrowUser, 10, fixedNow); !errors.Is(err, ErrChipCapExceeded) {
		t.Fatalf("ExchangeCoinToChip = %v, want ErrChipCapExceeded", err)
	}
	// The refusal persists nothing: the coins are not spent either.
	assertHoldings(t, path, escrowGuild, escrowUser, MaxChips-1000, 1000, "highlow")
	if account := readAccount(t, path, escrowGuild, escrowUser); account.Coins != 10 {
		t.Fatalf("Coins = %d, want 10 (a refused exchange must not debit)", account.Coins)
	}

	count, err := st.RefundStaleEscrows(fixedNow)
	if err != nil {
		t.Fatalf("RefundStaleEscrows returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("refunded %d accounts, want 1", count)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, MaxChips, 0, "")
}

func TestRefundStaleEscrows_HandEditedOverTheCapKeepsEveryChip(t *testing.T) {
	st, path := newTempStore(t)
	// Only a hand edit can reach Chips + Escrow > MaxChips now. The refund
	// still hands back the whole stake: capping here would destroy chips,
	// and the refund is a move between the account's own two fields, not a
	// credit — it creates nothing the cap needs to police.
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{
		Chips: MaxChips, Escrow: 500, EscrowGame: "blackjack", EscrowOpenedAt: "2026-09-14T12:00:00+09:00",
	})

	count, err := st.RefundStaleEscrows(fixedNow)
	if err != nil {
		t.Fatalf("RefundStaleEscrows returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("refunded %d accounts, want 1", count)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, MaxChips+500, 0, "")
}

// --- the jackpot pool (設計書 C-3a §2) ------------------------------------

// seedJackpot writes a known pool and carry for guildID, bypassing accrual so
// a test can start from any pool state. Unlike seedAccount it preserves the
// guild's accounts and rate history.
func seedJackpot(t *testing.T, st *Store, guildID string, pool, accum int64) {
	t.Helper()
	err := st.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		economy.Jackpot = pool
		economy.JackpotAccum = accum
		return nil
	})
	if err != nil {
		t.Fatalf("seeding the jackpot for %s: %v", guildID, err)
	}
}

// losingReels scripts a reel triple that pays nothing (🍋🍇🔔), so a test
// about the POOL is not also a test about the payout table. scriptedIntn
// wraps, so one triple drives any number of spins.
func losingReels(t *testing.T) []int {
	t.Helper()
	return []int{symbolDraw(t, SymbolLemon), symbolDraw(t, SymbolGrape), symbolDraw(t, SymbolBell)}
}

// TestStore_Spin_AccruesTheSmallestBetWithoutLosingTheRemainder pins down the
// reason the carry exists at all: the minimum bet's 2% is 0.2 chips, so an
// accrual that moved whole chips only would truncate to 0 EVERY spin and the
// pool would never grow from small play. Five 10-chip spins must add exactly
// one chip and leave no carry behind.
func TestStore_Spin_AccruesTheSmallestBetWithoutLosingTheRemainder(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, "guild1", "user-1", UserAccount{Chips: 1000})
	st.rng = &scriptedIntn{t: t, intns: losingReels(t)}

	for spinNo := 1; spinNo <= 5; spinNo++ {
		result, err := st.Spin("guild1", "user-1", 10)
		if err != nil {
			t.Fatalf("spin %d returned error: %v", spinNo, err)
		}
		if result.JackpotWon != 0 {
			t.Fatalf("spin %d won %d from the pool on 🍋🍇🔔 — only 7️⃣7️⃣7️⃣ fires", spinNo, result.JackpotWon)
		}
		// The whole chip lands on the fifth spin, when the carry reaches 100.
		wantPool, wantAccum := JackpotSeed, int64(20*spinNo)
		if spinNo == 5 {
			wantPool, wantAccum = JackpotSeed+1, 0
		}
		if result.JackpotPool != wantPool {
			t.Fatalf("spin %d: JackpotPool = %d, want %d", spinNo, result.JackpotPool, wantPool)
		}
		economy := readGuild(t, path, "guild1")
		if economy.Jackpot != wantPool || economy.JackpotAccum != wantAccum {
			t.Fatalf("after spin %d the persisted pool is (Jackpot %d, JackpotAccum %d), want (%d, %d)",
				spinNo, economy.Jackpot, economy.JackpotAccum, wantPool, wantAccum)
		}
	}
}

// TestStore_Spin_TripleSevenPaysTheWholePoolAndResetsToTheSeed is the firing
// rule of 設計書 §2: the ×196 table payout AND the entire pool, in one credit,
// after which the pool restarts from the seed. The pool paid out includes this
// spin's own accrual — 積立 → 抽選 → 配当 is the order Spin's closure runs in.
func TestStore_Spin_TripleSevenPaysTheWholePoolAndResetsToTheSeed(t *testing.T) {
	st, path := newTempStore(t)
	const startingChips = int64(50_000)
	const bet = int64(100)
	const poolBefore = int64(37_500)
	seedAccount(t, st, "guild1", "user-1", UserAccount{Chips: startingChips})
	seedJackpot(t, st, "guild1", poolBefore, 0)
	st.rng = &scriptedIntn{t: t, intns: []int{symbolDraw(t, SymbolSeven)}}

	result, err := st.Spin("guild1", "user-1", bet)
	if err != nil {
		t.Fatalf("Spin returned error: %v", err)
	}

	// bet*2 = 200 hundredths = 2 whole chips accrued before the draw resolves.
	wantWon := poolBefore + 2
	if result.JackpotWon != wantWon {
		t.Fatalf("JackpotWon = %d, want %d (the pool at the moment it fired, this spin's own 2 percent included)", result.JackpotWon, wantWon)
	}
	if result.Payout != bet*196+wantWon {
		t.Fatalf("Payout = %d, want %d (bet*196 + pool) — JackpotWon is a COMPONENT of Payout, not an extra credit", result.Payout, bet*196+wantWon)
	}
	if result.JackpotPool != JackpotSeed {
		t.Fatalf("JackpotPool = %d, want the seed %d — the pool restarts from the seed, not from 0", result.JackpotPool, JackpotSeed)
	}

	economy := readGuild(t, path, "guild1")
	if economy.Jackpot != JackpotSeed || economy.JackpotAccum != 0 {
		t.Fatalf("persisted pool = (Jackpot %d, JackpotAccum %d), want (%d, 0)", economy.Jackpot, economy.JackpotAccum, JackpotSeed)
	}
	if got := economy.Users["user-1"].Chips; got != startingChips-bet+result.Payout {
		t.Fatalf("persisted Chips = %d, want %d (start - bet + payout) — the deduction, the accrual and the whole payout are ONE transaction",
			got, startingChips-bet+result.Payout)
	}
}

// TestStore_Spin_TripleDiamondLeavesThePoolAlone guards the other half of the
// rule: 💎💎💎 is a jackpot for the PUBLIC CELEBRATION only (IsJackpot), and
// pays its ×98 from the table. The 10-chip bet's 2% cannot reach a whole chip,
// so the pool here must not move by even one.
func TestStore_Spin_TripleDiamondLeavesThePoolAlone(t *testing.T) {
	st, path := newTempStore(t)
	const bet = int64(10)
	const poolBefore = int64(5_000)
	seedAccount(t, st, "guild1", "user-1", UserAccount{Chips: 1000})
	seedJackpot(t, st, "guild1", poolBefore, 0)
	st.rng = &scriptedIntn{t: t, intns: []int{symbolDraw(t, SymbolDiamond)}}

	result, err := st.Spin("guild1", "user-1", bet)
	if err != nil {
		t.Fatalf("Spin returned error: %v", err)
	}
	if !result.IsJackpot {
		t.Fatalf("IsJackpot = false for 💎💎💎 — the celebration is unchanged by C-3a: %+v", result)
	}
	if result.JackpotWon != 0 {
		t.Fatalf("JackpotWon = %d for 💎💎💎, want 0 — only 7️⃣7️⃣7️⃣ empties the pool", result.JackpotWon)
	}
	if result.Payout != bet*98 {
		t.Fatalf("Payout = %d, want %d (the ×98 table payout, nothing from the pool)", result.Payout, bet*98)
	}
	if result.JackpotPool != poolBefore {
		t.Fatalf("JackpotPool = %d, want %d unchanged", result.JackpotPool, poolBefore)
	}

	economy := readGuild(t, path, "guild1")
	if economy.Jackpot != poolBefore || economy.JackpotAccum != bet*JackpotContributionPercent {
		t.Fatalf("persisted pool = (Jackpot %d, JackpotAccum %d), want (%d, %d): the 2 percent is carried, not rounded into the pool",
			economy.Jackpot, economy.JackpotAccum, poolBefore, bet*JackpotContributionPercent)
	}
}

// preC3aFile is a data/casino.json exactly as the bot wrote it before the
// jackpot fields existed: no "jackpot", no "jackpot_accum". Both unmarshal to
// 0, and 0 is the one pool value that must never be shown or paid — hence the
// seed. This is the upgrade path for every guild already on disk.
const preC3aFile = `{"guild1":{"announce_channel_id":"chan-1","rates":[{"date":"2026-07-10","rate":100,"trend":"flat","event":""}],"last_announced":"","users":{"user-1":{"coins":0,"chips":1000,"last_daily_date":"","streak_days":0}}}}`

func TestStore_Spin_SeedsThePoolOfAPreC3aFile(t *testing.T) {
	st, path := newTempStore(t)
	writeHandEditedFile(t, path, preC3aFile)
	st.rng = &scriptedIntn{t: t, intns: losingReels(t)}

	result, err := st.Spin("guild1", "user-1", 10)
	if err != nil {
		t.Fatalf("Spin returned error: %v", err)
	}
	if result.JackpotPool != JackpotSeed {
		t.Fatalf("JackpotPool = %d on the first spin of a pre-C-3a file, want the seed %d", result.JackpotPool, JackpotSeed)
	}

	economy := readGuild(t, path, "guild1")
	if economy.Jackpot != JackpotSeed {
		t.Fatalf("persisted Jackpot = %d, want the seed %d — a file written before C-3a must be seeded, not left at 0", economy.Jackpot, JackpotSeed)
	}
}

// TestStore_EnsureTodayRate_SeedsThePoolOfAPreC3aFile covers the OTHER seeding
// point. Every display of the pool (/balance, /rank, the 9am announcement)
// goes through today's rate, so a guild whose members have not spun since the
// upgrade would otherwise be shown a pool of 0 that is really the seed.
func TestStore_EnsureTodayRate_SeedsThePoolOfAPreC3aFile(t *testing.T) {
	st, path := newTempStore(t)
	writeHandEditedFile(t, path, preC3aFile)
	st.rng = forbiddenRand{t: t, reason: "today's rate is already in the file, so no draw may happen"}

	if _, err := st.EnsureTodayRate("guild1", fixedNow); err != nil {
		t.Fatalf("EnsureTodayRate returned error: %v", err)
	}

	economy := readGuild(t, path, "guild1")
	if economy.Jackpot != JackpotSeed {
		t.Fatalf("persisted Jackpot = %d after EnsureTodayRate, want the seed %d", economy.Jackpot, JackpotSeed)
	}
}

// TestStore_Spin_ConservesChipsAndPool is the 危険地帯 regression of 設計書 §8:
// across a run of spins with every outcome in it (loss, cherry pair, ×98 and
// two 7️⃣7️⃣7️⃣ firings), currency may appear in exactly two places and nowhere
// else. Both laws are checked against the numbers the RESULTS reported, so the
// test cannot drift into re-implementing the payout table:
//
//	Chips: final - initial == Σpayout - Σbet
//	Pool (in 1/100 chips, the unit the carry is kept in):
//	  final - initial == Σ(bet × 2) - Σfired + one seed per firing
func TestStore_Spin_ConservesChipsAndPool(t *testing.T) {
	st, path := newTempStore(t)
	const initialChips = int64(1_000_000)
	const initialPool = int64(12_345)
	const initialAccum = int64(73)
	seedAccount(t, st, "guild1", "user-1", UserAccount{Chips: initialChips})
	seedJackpot(t, st, "guild1", initialPool, initialAccum)

	// Four spins' worth of draws, cycled: 7️⃣7️⃣7️⃣ (fires), 🍒🍋🍒 (pair
	// refund), 🍋🍇🔔 (loss), 💎💎💎 (×98, pool untouched). scriptedIntn wraps,
	// so eight spins run the cycle twice and fire the pool twice.
	seven, cherry, lemon := symbolDraw(t, SymbolSeven), symbolDraw(t, SymbolCherry), symbolDraw(t, SymbolLemon)
	grape, bell, diamond := symbolDraw(t, SymbolGrape), symbolDraw(t, SymbolBell), symbolDraw(t, SymbolDiamond)
	st.rng = &scriptedIntn{t: t, intns: []int{
		seven, seven, seven,
		cherry, lemon, cherry,
		lemon, grape, bell,
		diamond, diamond, diamond,
	}}

	bets := []int64{10, 37, 250, 1000}
	var totalBet, totalPayout, totalContribUnits, totalDrained int64
	fires := 0
	for i := 0; i < 8; i++ {
		bet := bets[i%len(bets)]
		result, err := st.Spin("guild1", "user-1", bet)
		if err != nil {
			t.Fatalf("spin %d returned error: %v", i, err)
		}
		totalBet += bet
		totalPayout += result.Payout
		totalContribUnits += bet * JackpotContributionPercent
		if result.JackpotWon > 0 {
			fires++
			totalDrained += result.JackpotWon
		}
		economy := readGuild(t, path, "guild1")
		if economy.Jackpot != result.JackpotPool {
			t.Fatalf("spin %d: result reported JackpotPool %d but the file holds %d", i, result.JackpotPool, economy.Jackpot)
		}
	}
	if fires != 2 {
		t.Fatalf("the scripted cycle fired the pool %d times, want 2 — the conservation law is untested without a firing", fires)
	}

	economy := readGuild(t, path, "guild1")
	gotChips := economy.Users["user-1"].Chips
	if wantChips := initialChips - totalBet + totalPayout; gotChips != wantChips {
		t.Fatalf("Chips = %d, want %d (initial - Σbet + Σpayout): chips appeared or vanished outside the payout",
			gotChips, wantChips)
	}
	// In 1/100-chip units, so the carry is part of the sum rather than a
	// rounding excuse. A firing removes the whole pool and puts the seed back.
	gotUnits := economy.Jackpot*jackpotAccumScale + economy.JackpotAccum
	wantUnits := initialPool*jackpotAccumScale + initialAccum + totalContribUnits -
		totalDrained*jackpotAccumScale + int64(fires)*JackpotSeed*jackpotAccumScale
	if gotUnits != wantUnits {
		t.Fatalf("pool = %d hundredths, want %d (initial + Σaccrual - Σfired + seeds): the pool moved outside accrual and firing",
			gotUnits, wantUnits)
	}
}

// --- the daily lottery (設計書 C-3a §3) -----------------------------------

// seedLotteryChips gives every named user in guildID a known chip balance,
// creating the guild and the accounts. Balances are written directly rather
// than through the credit helpers so a test can start from any figure.
func seedLotteryChips(t *testing.T, st *Store, guildID string, chips map[string]int64) {
	t.Helper()
	err := st.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, guildID)
		for userID, amount := range chips {
			ensureAccountLocked(economy, userID).Chips = amount
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding lottery chips for %s: %v", guildID, err)
	}
}

// totalChips sums every account's chips in one guild — the left-hand side of
// the lottery's conservation law.
func totalChips(economy GuildEconomy) int64 {
	var total int64
	for _, account := range economy.Users {
		total += account.Chips
	}
	return total
}

// TestBuyLotteryTickets_RefusesBeyondThePerDrawCap pins the CUMULATIVE cap:
// 10 tickets per buyer per draw, whatever mix of calls they arrive in. The
// refusal must persist nothing at all — a purchase that eats chips without
// handing out tickets is the worst failure this method can have.
func TestBuyLotteryTickets_RefusesBeyondThePerDrawCap(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = &lotteryRand{t: t, float: 0.5} // no draw may happen: any Intn fails the test
	const guildID, userID = "guild1", "u1"
	seedLotteryChips(t, st, guildID, map[string]int64{userID: 1000})

	first, err := st.BuyLotteryTickets(guildID, userID, 6, fixedNow)
	if err != nil {
		t.Fatalf("first BuyLotteryTickets(6) returned error: %v", err)
	}
	if first.UserTickets != 6 || first.Cost != 300 || first.Balance != 700 {
		t.Fatalf("first purchase = %+v, want UserTickets=6 Cost=300 Balance=700", first)
	}

	var limit *ErrLotteryLimit
	_, err = st.BuyLotteryTickets(guildID, userID, 5, fixedNow)
	if !errors.As(err, &limit) {
		t.Fatalf("BuyLotteryTickets(5) on top of 6 returned %v, want *ErrLotteryLimit", err)
	}
	if limit.Remaining != 4 {
		t.Fatalf("ErrLotteryLimit.Remaining = %d, want 4 (10 - 6 already held)", limit.Remaining)
	}

	refused := readGuild(t, path, guildID)
	if refused.Lottery.Sales != 300 || refused.Lottery.Tickets[userID] != 6 || refused.Users[userID].Chips != 700 {
		t.Fatalf("the refused purchase was persisted: Sales=%d Tickets[%s]=%d Chips=%d, want 300/6/700",
			refused.Lottery.Sales, userID, refused.Lottery.Tickets[userID], refused.Users[userID].Chips)
	}

	// Exactly filling the cap is allowed; one more ticket is not, and the
	// headroom reported is 0 rather than a negative number.
	if _, err := st.BuyLotteryTickets(guildID, userID, 4, fixedNow); err != nil {
		t.Fatalf("BuyLotteryTickets(4) filling the cap returned error: %v", err)
	}
	_, err = st.BuyLotteryTickets(guildID, userID, 1, fixedNow)
	if !errors.As(err, &limit) || limit.Remaining != 0 {
		t.Fatalf("BuyLotteryTickets(1) at the cap returned %v (Remaining=%d), want *ErrLotteryLimit with Remaining=0", err, limit.Remaining)
	}

	// A single oversized call is the same violation, reported the same way:
	// the cap is on the HOLDING, so it needs no separate per-call check.
	fresh, _ := newTempStore(t)
	fresh.rng = &lotteryRand{t: t, float: 0.5}
	seedLotteryChips(t, fresh, guildID, map[string]int64{userID: 1000})
	_, err = fresh.BuyLotteryTickets(guildID, userID, 11, fixedNow)
	if !errors.As(err, &limit) || limit.Remaining != LotteryMaxTicketsPerDraw {
		t.Fatalf("BuyLotteryTickets(11) from nothing returned %v (Remaining=%d), want *ErrLotteryLimit with Remaining=%d",
			err, limit.Remaining, LotteryMaxTicketsPerDraw)
	}
	if _, err := fresh.BuyLotteryTickets(guildID, userID, 0, fixedNow); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("BuyLotteryTickets(0) returned %v, want ErrInvalidAmount", err)
	}
}

// TestBuyLotteryTickets_RefusesAndPersistsNothingWhenChipsAreShort is the
// other refusal: the balance check must leave the pot and the balance exactly
// where they were, so a short buyer can retry after /daily.
func TestBuyLotteryTickets_RefusesAndPersistsNothingWhenChipsAreShort(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = &lotteryRand{t: t, float: 0.5}
	const guildID, userID = "guild1", "u1"
	seedLotteryChips(t, st, guildID, map[string]int64{userID: 49}) // one chip short of a ticket

	var short *ErrInsufficientChips
	_, err := st.BuyLotteryTickets(guildID, userID, 1, fixedNow)
	if !errors.As(err, &short) {
		t.Fatalf("BuyLotteryTickets with 49 chips returned %v, want *ErrInsufficientChips", err)
	}
	if short.Balance != 49 {
		t.Fatalf("ErrInsufficientChips.Balance = %d, want 49", short.Balance)
	}

	after := readGuild(t, path, guildID)
	if after.Users[userID].Chips != 49 || after.Lottery.Sales != 0 || len(after.Lottery.Tickets) != 0 {
		t.Fatalf("the refused purchase was persisted: Chips=%d Sales=%d Tickets=%v, want 49/0/empty",
			after.Users[userID].Chips, after.Lottery.Sales, after.Lottery.Tickets)
	}
}

// TestLotteryDraw_PaysTheWinnerFeedsTheJackpotAndConservesChips is the
// conservation law of 設計書 C-3a §3, checked end to end across one draw:
// the buyers' chips fall by exactly the sales, the winner's rise by exactly
// the prize, and the jackpot pool rises by exactly the house's cut. Nothing
// is created, and the only chips that can be destroyed are the ones
// MaxJackpot refuses to let into the pool — not reachable here, where the
// pool sits at the seed, and pinned separately by the MaxJackpot tests
// below.
func TestLotteryDraw_PaysTheWinnerFeedsTheJackpotAndConservesChips(t *testing.T) {
	st, path := newTempStore(t)
	// Roll 5 falls in u3's range (u1 [0,1), u2 [1,4), u3 [4,10)).
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{5}}
	const guildID = "guild1"
	seedLotteryChips(t, st, guildID, map[string]int64{"u1": 1000, "u2": 1000, "u3": 1000})

	for userID, count := range map[string]int{"u1": 1, "u2": 3, "u3": 6} {
		if _, err := st.BuyLotteryTickets(guildID, userID, count, fixedNow); err != nil {
			t.Fatalf("BuyLotteryTickets(%s, %d): %v", userID, count, err)
		}
	}

	sold := readGuild(t, path, guildID)
	const wantSales = int64(500) // 10 tickets * 50 chips
	if sold.Lottery.Sales != wantSales {
		t.Fatalf("Sales after 10 tickets = %d, want %d", sold.Lottery.Sales, wantSales)
	}
	if got := totalChips(sold); got != 3000-wantSales {
		t.Fatalf("chips after the purchases = %d, want %d (3000 - sales): the debit does not match the sales",
			got, 3000-wantSales)
	}
	jackpotBefore := sold.Jackpot

	// The next day's first touch rolls the draw over.
	if _, err := st.EnsureTodayRate(guildID, daysAfter(1)); err != nil {
		t.Fatalf("EnsureTodayRate: %v", err)
	}

	drawn := readGuild(t, path, guildID)
	const wantPrize, wantHouse = int64(450), int64(50)
	if got := drawn.Users["u3"].Chips - sold.Users["u3"].Chips; got != wantPrize {
		t.Fatalf("winner u3 gained %d chips, want %d (= floor(500*90/100) + 0 carryover)", got, wantPrize)
	}
	for _, loser := range []string{"u1", "u2"} {
		if drawn.Users[loser].Chips != sold.Users[loser].Chips {
			t.Fatalf("%s's chips moved from %d to %d — only the winner is paid",
				loser, sold.Users[loser].Chips, drawn.Users[loser].Chips)
		}
	}
	if got := drawn.Jackpot - jackpotBefore; got != wantHouse {
		t.Fatalf("the jackpot pool gained %d, want %d (= sales - floor(sales*90/100)): the house's cut is not reaching the pool",
			got, wantHouse)
	}
	if got := totalChips(drawn); got != 3000-wantSales+wantPrize {
		t.Fatalf("chips after the draw = %d, want %d (= start - sales + prize)", got, 3000-wantSales+wantPrize)
	}

	day1 := jstDate(daysAfter(1))
	if drawn.Lottery.DrawDate != day1 || drawn.Lottery.Sales != 0 || len(drawn.Lottery.Tickets) != 0 || drawn.Lottery.Carryover != 0 {
		t.Fatalf("the pot was not reopened empty: %+v (want DrawDate=%s, Sales=0, no tickets, Carryover=0)", drawn.Lottery, day1)
	}
	want := LotteryDraw{Date: day1, WinnerID: "u3", Prize: wantPrize, TicketsSold: 10, Buyers: 3}
	if drawn.Lottery.LastDraw == nil || *drawn.Lottery.LastDraw != want {
		t.Fatalf("LastDraw = %+v, want %+v", drawn.Lottery.LastDraw, want)
	}
}

// TestJackpotCap_CreditsUpToTheCapAndDropsTheRest pins creditJackpotCappedLocked,
// the single door into the pool. The property being fixed is not any one
// arithmetic result but the INVARIANT: whatever the stored pool and whatever
// the addend — including an addend that has already wrapped upstream — the
// pool it leaves behind is inside [JackpotSeed, MaxJackpot]. That is what
// makes the overflow the C3-07 evaluation found unreachable rather than
// merely unlikely, so the table deliberately includes values no legitimate
// sequence can produce.
func TestJackpotCap_CreditsUpToTheCapAndDropsTheRest(t *testing.T) {
	cases := []struct {
		name                   string
		pool, amount           int64
		wantCredited, wantPool int64
	}{
		{"well below the cap credits in full", 5_000, 100, 100, 5_100},
		{"a pre-C-3a zero is seeded before the credit, not after", 0, 50, 50, JackpotSeed + 50},
		{"a credit straddling the cap is truncated to the headroom", MaxJackpot - 10, 50, 10, MaxJackpot},
		{"a pool already at the cap takes nothing", MaxJackpot, 100, 0, MaxJackpot},
		{"a hand-edited MaxInt64 pool is clamped, not wrapped", math.MaxInt64, 100, 0, MaxJackpot},
		{"a hand-edited MinInt64 pool is raised to the seed", math.MinInt64, 100, 100, JackpotSeed + 100},
		{"a negative addend never drains the pool", 5_000, -100, 0, 5_000},
		{"an addend bigger than the whole cap lands exactly on it", JackpotSeed, math.MaxInt64, MaxJackpot - JackpotSeed, MaxJackpot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			economy := &GuildEconomy{Jackpot: tc.pool}
			if credited := creditJackpotCappedLocked(economy, tc.amount); credited != tc.wantCredited {
				t.Fatalf("credited %d, want %d", credited, tc.wantCredited)
			}
			if economy.Jackpot != tc.wantPool {
				t.Fatalf("pool = %d, want %d", economy.Jackpot, tc.wantPool)
			}
			if economy.Jackpot < JackpotSeed || economy.Jackpot > MaxJackpot {
				t.Fatalf("pool = %d, outside [%d, %d] — the invariant the cap exists for",
					economy.Jackpot, JackpotSeed, MaxJackpot)
			}
		})
	}
}

// TestStore_JackpotCapStopsADrawAndASpinAtMaxJackpot walks the two real
// credit paths — the lottery house cut and the slot accrual — into a pool
// with less headroom than they want, through the Store rather than the
// helper. The draw's 50-chip cut meets 10 chips of room: 10 land and 40 are
// dropped, which is the one place C-3a's conservation law gives ground. The
// player side must NOT give ground with it: the winner is still paid in full,
// because the cap costs the house its cut and never a balance.
func TestStore_JackpotCapStopsADrawAndASpinAtMaxJackpot(t *testing.T) {
	st, path := newTempStore(t)
	// Roll 5 falls in u3's range (u1 [0,1), u2 [1,4), u3 [4,10)).
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{5}}
	const guildID = "guild1"
	seedLotteryChips(t, st, guildID, map[string]int64{"u1": 1_000, "u2": 1_000, "u3": 1_000})
	for userID, count := range map[string]int{"u1": 1, "u2": 3, "u3": 6} {
		if _, err := st.BuyLotteryTickets(guildID, userID, count, fixedNow); err != nil {
			t.Fatalf("BuyLotteryTickets(%s, %d): %v", userID, count, err)
		}
	}
	seedJackpot(t, st, guildID, MaxJackpot-10, 0)

	if _, err := st.EnsureTodayRate(guildID, daysAfter(1)); err != nil {
		t.Fatalf("EnsureTodayRate: %v", err)
	}

	drawn := readGuild(t, path, guildID)
	if drawn.Jackpot != MaxJackpot {
		t.Fatalf("pool after the draw = %d, want exactly MaxJackpot %d (the 50-chip cut had 10 chips of room)",
			drawn.Jackpot, MaxJackpot)
	}
	// u3 bought 6 of the 10 tickets (300 chips) and won the 450-chip prize.
	if got := drawn.Users["u3"].Chips; got != 1_150 {
		t.Fatalf("winner u3 holds %d chips, want 1150 (1000 - 300 spent + 450 prize): the pool's cap must not touch a payout", got)
	}

	// Now the accrual path, against a pool that is already full.
	seedLotteryChips(t, st, guildID, map[string]int64{"u1": 50_000})
	st.rng = &scriptedIntn{t: t, intns: losingReels(t)}
	result, err := st.at(daysAfter(1)).Spin(guildID, "u1", 1_000)
	if err != nil {
		t.Fatalf("Spin returned error: %v", err)
	}
	if result.JackpotPool != MaxJackpot {
		t.Fatalf("JackpotPool after the spin = %d, want MaxJackpot %d", result.JackpotPool, MaxJackpot)
	}
	spun := readGuild(t, path, guildID)
	if spun.Jackpot != MaxJackpot {
		t.Fatalf("persisted pool after the spin = %d, want MaxJackpot %d — the 20-chip accrual must be dropped, not added",
			spun.Jackpot, MaxJackpot)
	}
	if spun.JackpotAccum < 0 || spun.JackpotAccum >= jackpotAccumScale {
		t.Fatalf("JackpotAccum = %d, want [0, %d): the carry is spent at the cap too, not banked",
			spun.JackpotAccum, jackpotAccumScale)
	}
}

// TestStore_JackpotCapNormalisesAHandEditedPool is the other half: the values
// that can only reach the file by a hand edit or a half-finished write, which
// is exactly how the C3-07 evaluation reproduced the wrap. Both cases here
// wrapped the pool into a large NEGATIVE number before the cap existed — and
// a negative pool is not a cosmetic problem, it is money the next 7️⃣7️⃣7️⃣
// would hand to a player.
func TestStore_JackpotCapNormalisesAHandEditedPool(t *testing.T) {
	t.Run("a MaxInt64 pool is clamped at the read point, before the accrual", func(t *testing.T) {
		st, path := newTempStore(t)
		seedAccount(t, st, "guild1", "u1", UserAccount{Chips: 50_000})
		seedJackpot(t, st, "guild1", math.MaxInt64, 0)
		st.rng = &scriptedIntn{t: t, intns: losingReels(t)}

		if _, err := st.Spin("guild1", "u1", 1_000); err != nil {
			t.Fatalf("Spin returned error: %v", err)
		}
		economy := readGuild(t, path, "guild1")
		if economy.Jackpot != MaxJackpot {
			t.Fatalf("pool = %d, want MaxJackpot %d — seedJackpotLocked must clamp before accrueJackpotLocked adds",
				economy.Jackpot, MaxJackpot)
		}
	})

	t.Run("a carryover that overflows the prize cannot drain the pool", func(t *testing.T) {
		// LotteryPrize computes share+carryover, which wraps NEGATIVE here.
		// creditChipsCappedLocked then pays 0, so house+(prize-paid) is a
		// large negative addend — the second reproduction in the C3-07
		// evaluation, which used to SHRINK the pool by it.
		st, path := newTempStore(t)
		st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0}}
		const guildID, userID = "guild1", "u1"
		seedLotteryChips(t, st, guildID, map[string]int64{userID: 1_000})
		if _, err := st.BuyLotteryTickets(guildID, userID, 2, fixedNow); err != nil {
			t.Fatalf("BuyLotteryTickets: %v", err)
		}
		if err := st.Update(func(d *Data) error {
			ensureGuildLocked(d, guildID).Lottery.Carryover = math.MaxInt64 - 40
			return nil
		}); err != nil {
			t.Fatalf("seeding the overflowing carryover: %v", err)
		}
		seedJackpot(t, st, guildID, 5_000, 0)

		if _, err := st.EnsureTodayRate(guildID, daysAfter(1)); err != nil {
			t.Fatalf("EnsureTodayRate: %v", err)
		}
		economy := readGuild(t, path, guildID)
		if economy.Jackpot != 5_000 {
			t.Fatalf("pool = %d, want 5000 unchanged: a wrapped, negative addend must move nothing", economy.Jackpot)
		}
		if economy.Jackpot < JackpotSeed || economy.Jackpot > MaxJackpot {
			t.Fatalf("pool = %d, outside [%d, %d]", economy.Jackpot, JackpotSeed, MaxJackpot)
		}
	})
}

// TestLotteryDraw_NoBuyersRollsThePrizeForwardAndKeepsTheLastResult covers
// the quiet day: no ticket was sold, so no winner is named, the pot rolls
// forward whole, and — the part that is easy to get wrong — the previous
// real result is NOT overwritten, so /lottery status still has something to
// show. The carryover must then land in the next draw that does have buyers.
func TestLotteryDraw_NoBuyersRollsThePrizeForwardAndKeepsTheLastResult(t *testing.T) {
	st, path := newTempStore(t)
	// One roll only: it belongs to the day-2 draw. A draw on the buyer-less
	// day 1 would consume it and fail the test.
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0}}
	const guildID, userID = "guild1", "u1"
	seedLotteryChips(t, st, guildID, map[string]int64{userID: 1000})

	older := LotteryDraw{Date: "2026-07-01", WinnerID: "u9", Prize: 1234, TicketsSold: 7, Buyers: 2}
	if err := st.Update(func(d *Data) error {
		lottery := &ensureGuildLocked(d, guildID).Lottery
		lottery.Carryover, lottery.DrawDate, lottery.LastDraw = 300, jstDate(fixedNow), &older
		return nil
	}); err != nil {
		t.Fatalf("seeding the carryover: %v", err)
	}

	if _, err := st.EnsureTodayRate(guildID, daysAfter(1)); err != nil {
		t.Fatalf("EnsureTodayRate on the buyer-less day: %v", err)
	}

	quiet := readGuild(t, path, guildID)
	if quiet.Lottery.Carryover != 300 {
		t.Fatalf("Carryover after a buyer-less draw = %d, want 300 (the whole prize rolls forward)", quiet.Lottery.Carryover)
	}
	if quiet.Lottery.DrawDate != jstDate(daysAfter(1)) {
		t.Fatalf("DrawDate = %q, want %q — an empty draw must still stamp the day, or it retries all day",
			quiet.Lottery.DrawDate, jstDate(daysAfter(1)))
	}
	if quiet.Lottery.LastDraw == nil || *quiet.Lottery.LastDraw != older {
		t.Fatalf("LastDraw = %+v, want the untouched %+v — a quiet day must not blank out the last real result",
			quiet.Lottery.LastDraw, older)
	}

	// The carryover now rides on the next draw that does have a buyer.
	if _, err := st.BuyLotteryTickets(guildID, userID, 1, daysAfter(1)); err != nil {
		t.Fatalf("BuyLotteryTickets on day 1: %v", err)
	}
	if _, err := st.EnsureTodayRate(guildID, daysAfter(2)); err != nil {
		t.Fatalf("EnsureTodayRate on day 2: %v", err)
	}

	paid := readGuild(t, path, guildID)
	const wantPrize = int64(345) // floor(50*90/100)=45, plus the 300 carried over
	want := LotteryDraw{Date: jstDate(daysAfter(2)), WinnerID: userID, Prize: wantPrize, TicketsSold: 1, Buyers: 1}
	if paid.Lottery.LastDraw == nil || *paid.Lottery.LastDraw != want {
		t.Fatalf("LastDraw = %+v, want %+v (the carryover must be part of the prize)", paid.Lottery.LastDraw, want)
	}
	if paid.Lottery.Carryover != 0 {
		t.Fatalf("Carryover after a paid draw = %d, want 0 — it was handed to the winner", paid.Lottery.Carryover)
	}
	// 1000 - 50 spent + 345 won.
	if got := paid.Users[userID].Chips; got != 1295 {
		t.Fatalf("buyer's chips = %d, want 1295 (1000 - 50 + 345)", got)
	}
}

// TestLotteryDraw_RunsAtMostOncePerDay is the double-payout regression. Every
// command goes through the rollover, so the guard has to hold across repeated
// touches of any kind — and across a clock that moves BACKWARDS, which is why
// the date comparison is `<` and not `!=`.
func TestLotteryDraw_RunsAtMostOncePerDay(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0}} // exactly one draw is allowed
	const guildID, userID = "guild1", "u1"
	seedLotteryChips(t, st, guildID, map[string]int64{userID: 1000})
	if _, err := st.BuyLotteryTickets(guildID, userID, 2, fixedNow); err != nil {
		t.Fatalf("BuyLotteryTickets: %v", err)
	}

	if _, err := st.EnsureTodayRate(guildID, daysAfter(1)); err != nil {
		t.Fatalf("first EnsureTodayRate: %v", err)
	}
	drawn := readGuild(t, path, guildID)
	if drawn.Lottery.LastDraw == nil {
		t.Fatalf("no draw happened on the new day: %+v", drawn.Lottery)
	}
	wonChips := drawn.Users[userID].Chips

	// Four more touches of the same day, through four different entry points.
	if _, err := st.EnsureTodayRate(guildID, daysAfter(1)); err != nil {
		t.Fatalf("second EnsureTodayRate: %v", err)
	}
	if _, err := st.LotteryStatus(guildID, userID, daysAfter(1)); err != nil {
		t.Fatalf("LotteryStatus: %v", err)
	}
	if _, err := st.ViewAccount(guildID, userID, daysAfter(1)); err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	// ...and one with the clock wound back to the day the pot was sold on.
	if _, err := st.EnsureTodayRate(guildID, fixedNow); err != nil {
		t.Fatalf("EnsureTodayRate with a backwards clock: %v", err)
	}

	after := readGuild(t, path, guildID)
	if after.Users[userID].Chips != wonChips {
		t.Fatalf("chips moved from %d to %d after re-touching the same day — the draw paid out twice",
			wonChips, after.Users[userID].Chips)
	}
	if *after.Lottery.LastDraw != *drawn.Lottery.LastDraw {
		t.Fatalf("LastDraw changed from %+v to %+v on a second touch of the same day",
			drawn.Lottery.LastDraw, after.Lottery.LastDraw)
	}
}

// TestEnsureTodayRate_StartupFallbackDrawsTheMissedLottery is the
// 取りこぼし防止 of 設計書 C-3a §3: the bot was down at 09:00 — for two whole
// days here — and the draw still happens, at the first touch after it comes
// back, without any announcement channel being configured.
func TestEnsureTodayRate_StartupFallbackDrawsTheMissedLottery(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0}}
	const guildID, userID = "guild1", "u1"
	seedLotteryChips(t, st, guildID, map[string]int64{userID: 1000})
	if _, err := st.BuyLotteryTickets(guildID, userID, 4, fixedNow); err != nil {
		t.Fatalf("BuyLotteryTickets: %v", err)
	}
	if configured := readGuild(t, path, guildID); configured.AnnounceChannelID != "" {
		t.Fatalf("this test must run with no announcement channel, got %q", configured.AnnounceChannelID)
	}

	// Two days later the process starts again and ensures the rate.
	if _, err := st.EnsureTodayRate(guildID, daysAfter(2)); err != nil {
		t.Fatalf("EnsureTodayRate: %v", err)
	}

	after := readGuild(t, path, guildID)
	const wantPrize = int64(180) // floor(200*90/100)
	want := LotteryDraw{Date: jstDate(daysAfter(2)), WinnerID: userID, Prize: wantPrize, TicketsSold: 4, Buyers: 1}
	if after.Lottery.LastDraw == nil || *after.Lottery.LastDraw != want {
		t.Fatalf("LastDraw = %+v, want %+v — a draw missed while the bot was down must happen at the next touch",
			after.Lottery.LastDraw, want)
	}
	if got := after.Users[userID].Chips; got != 1000-200+wantPrize {
		t.Fatalf("buyer's chips = %d, want %d (1000 - 200 + %d)", got, 1000-200+wantPrize, wantPrize)
	}
}

// TestBuyLotteryTickets_ConcurrentPurchasesConserveTicketsAndSales is the
// lost-update regression for the pot: 50 single-ticket purchases arrive on 50
// goroutines (discordgo runs every interaction in its own), and the ticket
// map, the sales figure and every balance must agree afterwards. A
// read-modify-write outside Store.Update's mutex loses tickets here.
func TestBuyLotteryTickets_ConcurrentPurchasesConserveTicketsAndSales(t *testing.T) {
	st, path := newTempStore(t)
	const guildID = "guild1"
	users := []string{"u1", "u2", "u3", "u4", "u5"}
	const perUser = 10 // exactly the per-draw cap, so every call must succeed

	var wg sync.WaitGroup
	for _, userID := range users {
		for i := 0; i < perUser; i++ {
			wg.Add(1)
			go func(userID string) {
				defer wg.Done()
				if _, err := st.BuyLotteryTickets(guildID, userID, 1, fixedNow); err != nil {
					t.Errorf("BuyLotteryTickets(%s): %v", userID, err)
				}
			}(userID)
		}
	}
	wg.Wait()

	after := readGuild(t, path, guildID)
	wantSold := len(users) * perUser
	sold := 0
	for _, n := range after.Lottery.Tickets {
		sold += n
	}
	if sold != wantSold {
		t.Fatalf("lost update detected: %d tickets persisted, want %d — Store.Update is not serializing concurrent purchases",
			sold, wantSold)
	}
	wantSales := LotteryTicketPrice * int64(wantSold)
	if after.Lottery.Sales != wantSales {
		t.Fatalf("Sales = %d, want %d (= %d tickets * %d chips)", after.Lottery.Sales, wantSales, wantSold, LotteryTicketPrice)
	}
	for _, userID := range users {
		if got := after.Lottery.Tickets[userID]; got != perUser {
			t.Fatalf("%s holds %d tickets, want %d", userID, got, perUser)
		}
		// Every account opened on the welcome bonus and spent 10*50 of it.
		wantChips := int64(welcomeBonusChips) - LotteryTicketPrice*perUser
		if got := after.Users[userID].Chips; got != wantChips {
			t.Fatalf("%s's chips = %d, want %d (welcome bonus - %d tickets)", userID, got, wantChips, perUser)
		}
	}
}

// TestLotteryStatus_ReportsThePotTheHoldingAndTheNextDraw covers what
// /lottery status renders: the prize as it stands, the pot's size, the
// asking user's own holding, the next 09:00 JST, and the previous result
// (nil until there has been one).
func TestLotteryStatus_ReportsThePotTheHoldingAndTheNextDraw(t *testing.T) {
	st, _ := newTempStore(t)
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{5}}
	const guildID = "guild1"
	seedLotteryChips(t, st, guildID, map[string]int64{"u1": 1000, "u2": 1000, "u3": 1000})
	for userID, count := range map[string]int{"u1": 1, "u2": 3, "u3": 6} {
		if _, err := st.BuyLotteryTickets(guildID, userID, count, fixedNow); err != nil {
			t.Fatalf("BuyLotteryTickets(%s): %v", userID, err)
		}
	}

	view, err := st.LotteryStatus(guildID, "u2", fixedNow)
	if err != nil {
		t.Fatalf("LotteryStatus returned error: %v", err)
	}
	wantNext := time.Date(2026, 7, 11, 9, 0, 0, 0, jst) // fixedNow is 12:00 JST on the 10th
	if view.Prize != 450 || view.TicketsSold != 10 || view.Buyers != 3 || view.UserTickets != 3 {
		t.Fatalf("view = %+v, want Prize=450 TicketsSold=10 Buyers=3 UserTickets=3", view)
	}
	if !view.NextDrawAt.Equal(wantNext) {
		t.Fatalf("NextDrawAt = %s, want %s (the next 09:00 JST)", view.NextDrawAt, wantNext)
	}
	if view.LastDraw != nil {
		t.Fatalf("LastDraw = %+v, want nil before the first draw", view.LastDraw)
	}

	// Asking after the rollover both performs the draw and reports it.
	drawn, err := st.LotteryStatus(guildID, "u1", daysAfter(1))
	if err != nil {
		t.Fatalf("LotteryStatus after the rollover returned error: %v", err)
	}
	if drawn.LastDraw == nil || drawn.LastDraw.WinnerID != "u3" || drawn.LastDraw.Prize != 450 {
		t.Fatalf("LastDraw = %+v, want u3 with a prize of 450", drawn.LastDraw)
	}
	if drawn.Prize != 0 || drawn.TicketsSold != 0 || drawn.UserTickets != 0 {
		t.Fatalf("the reopened pot = %+v, want an empty one (Prize/TicketsSold/UserTickets all 0)", drawn)
	}
}

// TestLotteryDraw_HoldsThePotUntilNineAM is the schedule of 設計書 C-3a §3:
// the draw is at 09:00 JST, not at midnight. Both halves of the failure a
// midnight boundary produces are checked here — a pot settled nine hours
// early, and a ticket bought in those nine hours landing in a pot that has
// already been drawn, so the 09:00 its confirmation named passes it by.
func TestLotteryDraw_HoldsThePotUntilNineAM(t *testing.T) {
	st, path := newTempStore(t)
	// One roll only: it belongs to the 09:00 draw. Any earlier draw consumes
	// it and fails the test right where the bug used to be.
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0}}
	const guildID, userID = "guild1", "u1"
	seedLotteryChips(t, st, guildID, map[string]int64{userID: 1000})
	if _, err := st.BuyLotteryTickets(guildID, userID, 2, fixedNow); err != nil { // day D, 12:00
		t.Fatalf("BuyLotteryTickets on day D: %v", err)
	}

	// Day D+1, 08:59:59 — past midnight, before the draw.
	beforeNine := daysAfter(1).Add(-3*time.Hour - 1*time.Second)
	if beforeNine.In(jst).Hour() != 8 {
		t.Fatalf("test setup: beforeNine is %s, want an 08:xx JST instant", beforeNine.In(jst))
	}
	status, err := st.LotteryStatus(guildID, userID, beforeNine)
	if err != nil {
		t.Fatalf("LotteryStatus before 9am: %v", err)
	}
	if status.LastDraw != nil || status.TicketsSold != 2 || status.Prize != 90 {
		t.Fatalf("the pot was settled before 09:00: LastDraw=%+v TicketsSold=%d Prize=%d, want nil/2/90",
			status.LastDraw, status.TicketsSold, status.Prize)
	}

	// A ticket bought in that window must join the very draw the status just
	// announced, not a pot that is already gone.
	if _, err := st.BuyLotteryTickets(guildID, userID, 1, beforeNine); err != nil {
		t.Fatalf("BuyLotteryTickets before 9am: %v", err)
	}
	if held := readGuild(t, path, guildID); held.Lottery.Tickets[userID] != 3 || held.Lottery.Sales != 150 {
		t.Fatalf("the pot before 09:00 holds %v tickets / %d sales, want 3/150 — the 08:00 purchase opened a new pot",
			held.Lottery.Tickets, held.Lottery.Sales)
	}

	// 09:00:00 sharp draws it, all three tickets included.
	atNine := beforeNine.Add(1 * time.Second)
	if _, err := st.EnsureTodayRate(guildID, atNine); err != nil {
		t.Fatalf("EnsureTodayRate at 9am: %v", err)
	}
	drawn := readGuild(t, path, guildID)
	const wantPrize = int64(135) // floor(150*90/100)
	want := LotteryDraw{Date: jstDate(atNine), WinnerID: userID, Prize: wantPrize, TicketsSold: 3, Buyers: 1}
	if drawn.Lottery.LastDraw == nil || *drawn.Lottery.LastDraw != want {
		t.Fatalf("LastDraw at 09:00 = %+v, want %+v", drawn.Lottery.LastDraw, want)
	}
	if got := drawn.Users[userID].Chips; got != 1000-150+wantPrize {
		t.Fatalf("buyer's chips = %d, want %d (1000 - 150 + %d)", got, 1000-150+wantPrize, wantPrize)
	}
}

// TestBuyLotteryTickets_RefusedPurchaseKeepsTheSettledDraw closes the re-roll
// the refusal path used to open (危険地帯). The rollover runs inside the
// purchase's own transaction, so aborting that transaction on a refusal threw
// the draw away — but rng lives on the Store, not in Data, so it kept the
// position the discarded draw had advanced it to. Calling again re-drew the
// same pot from a different roll: a buyer with no chips could retry until the
// draw named them, which breaks both "at most one draw a day" and the
// weighting PickLotteryWinner promises.
func TestBuyLotteryTickets_RefusedPurchaseKeepsTheSettledDraw(t *testing.T) {
	st, path := newTempStore(t)
	// Exactly one roll: a second draw — the re-roll under test — fails here.
	// Roll 1 names u2 (u1 holds [0,1), u2 holds [1,2)).
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{1}}
	const guildID = "guild1"
	seedLotteryChips(t, st, guildID, map[string]int64{"u1": 50, "u2": 1000})
	for _, userID := range []string{"u1", "u2"} {
		if _, err := st.BuyLotteryTickets(guildID, userID, 1, fixedNow); err != nil {
			t.Fatalf("BuyLotteryTickets(%s): %v", userID, err)
		}
	}
	sold := readGuild(t, path, guildID)
	if sold.Users["u1"].Chips != 0 {
		t.Fatalf("test setup: u1 must be broke after buying, got %d chips", sold.Users["u1"].Chips)
	}
	u2Before := sold.Users["u2"].Chips

	// The broke buyer is the first to touch the guild after 09:00, so their
	// refused purchase is what carries the draw.
	var short *ErrInsufficientChips
	if _, err := st.BuyLotteryTickets(guildID, "u1", 1, daysAfter(1)); !errors.As(err, &short) {
		t.Fatalf("BuyLotteryTickets with 0 chips returned %v, want *ErrInsufficientChips", err)
	}

	drawn := readGuild(t, path, guildID)
	day1 := jstDate(daysAfter(1))
	want := LotteryDraw{Date: day1, WinnerID: "u2", Prize: 90, TicketsSold: 2, Buyers: 2}
	if drawn.Lottery.LastDraw == nil || *drawn.Lottery.LastDraw != want {
		t.Fatalf("LastDraw after the refused purchase = %+v, want %+v — the refusal threw the draw away",
			drawn.Lottery.LastDraw, want)
	}
	if drawn.Lottery.DrawDate != day1 {
		t.Fatalf("DrawDate = %q, want %q — an unstamped date lets the next call draw again", drawn.Lottery.DrawDate, day1)
	}
	if got := drawn.Users["u2"].Chips - u2Before; got != 90 {
		t.Fatalf("winner u2 gained %d chips, want 90 — the payout was rolled back with the refusal", got)
	}
	if drawn.Users["u1"].Chips != 0 || drawn.Lottery.Sales != 0 || len(drawn.Lottery.Tickets) != 0 {
		t.Fatalf("the refused purchase was persisted: Chips=%d Sales=%d Tickets=%v, want 0/0/empty",
			drawn.Users["u1"].Chips, drawn.Lottery.Sales, drawn.Lottery.Tickets)
	}

	// Retrying is the exploit: the scripted rolls are exhausted, so any
	// second draw fails inside lotteryRand.Intn.
	if _, err := st.BuyLotteryTickets(guildID, "u1", 1, daysAfter(1)); !errors.As(err, &short) {
		t.Fatalf("the retry returned %v, want *ErrInsufficientChips", err)
	}
	after := readGuild(t, path, guildID)
	if *after.Lottery.LastDraw != want || after.Users["u2"].Chips != drawn.Users["u2"].Chips {
		t.Fatalf("LastDraw became %+v (u2 at %d chips) on the retry — the draw was re-run",
			after.Lottery.LastDraw, after.Users["u2"].Chips)
	}
}

// TestLotteryDraw_WinnerAtTheChipCapSendsTheRemainderToTheJackpot pins what
// the draw does when the winner has no room for the whole prize. MaxChips is
// a hard invariant of this package — every other credit path refuses rather
// than breach it — but a rollover has nobody to refuse TO: it runs inside
// whatever command happened to touch the guild first. So the winner is paid
// as far as the cap allows and the remainder follows the house's cut into the
// jackpot pool. Carrying it forward instead would move THIS winner's prize to
// whoever wins the NEXT draw — a different player — which is what this test
// exists to forbid; the pool is the one destination that keeps the chips in
// play without handing them to a named person.
func TestLotteryDraw_WinnerAtTheChipCapSendsTheRemainderToTheJackpot(t *testing.T) {
	st, path := newTempStore(t)
	// Two draws, each named by roll 0: day 1 goes to u1 (sorted first), day 2
	// is u2's alone.
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0, 0}}
	const guildID = "guild1"
	seedLotteryChips(t, st, guildID, map[string]int64{"u1": MaxChips, "u2": 1000})
	for _, userID := range []string{"u1", "u2"} {
		if _, err := st.BuyLotteryTickets(guildID, userID, 1, fixedNow); err != nil {
			t.Fatalf("BuyLotteryTickets(%s): %v", userID, err)
		}
	}
	sold := readGuild(t, path, guildID)
	jackpotBefore, chipsBefore := sold.Jackpot, totalChips(sold)
	const salesDay1 = int64(100) // 2 tickets at LotteryTicketPrice

	if _, err := st.EnsureTodayRate(guildID, daysAfter(1)); err != nil {
		t.Fatalf("EnsureTodayRate on day 1: %v", err)
	}
	drawn := readGuild(t, path, guildID)
	// prize 90, but u1 spent 50 of MaxChips on the ticket, so only 50 fits.
	want := LotteryDraw{Date: jstDate(daysAfter(1)), WinnerID: "u1", Prize: 50, TicketsSold: 2, Buyers: 2}
	if drawn.Lottery.LastDraw == nil || *drawn.Lottery.LastDraw != want {
		t.Fatalf("LastDraw = %+v, want %+v — LastDraw must report what was actually PAID", drawn.Lottery.LastDraw, want)
	}
	if got := drawn.Users["u1"].Chips; got != MaxChips {
		t.Fatalf("capped winner's chips = %d, want MaxChips (%d) — the cap was breached or the payout was skipped", got, MaxChips)
	}
	if drawn.Lottery.Carryover != 0 {
		t.Fatalf("Carryover = %d, want 0 — a draw that named a winner must reopen the pot empty instead of moving the remainder to the next winner",
			drawn.Lottery.Carryover)
	}
	// 10 house + the 40 the winner had no room for.
	if got := drawn.Jackpot - jackpotBefore; got != 50 {
		t.Fatalf("the jackpot pool gained %d, want 50 (10 house + 40 unpayable remainder)", got)
	}
	// Conservation: what the draw handed out, in chips and in pool, is
	// exactly what it took in — sales plus the carryover it started from (0).
	if got := (totalChips(drawn) - chipsBefore) + (drawn.Jackpot - jackpotBefore); got != salesDay1 {
		t.Fatalf("chips gained + pool gained = %d, want %d (sales + carryover) — the draw created or destroyed chips", got, salesDay1)
	}

	// Day 2: u2 is the only buyer, so a carried-over remainder would land in
	// u2's account here.
	if _, err := st.BuyLotteryTickets(guildID, "u2", 1, daysAfter(1)); err != nil {
		t.Fatalf("BuyLotteryTickets on day 1: %v", err)
	}
	before := readGuild(t, path, guildID)
	u2Before, jackpotBefore2 := before.Users["u2"].Chips, before.Jackpot
	if _, err := st.EnsureTodayRate(guildID, daysAfter(2)); err != nil {
		t.Fatalf("EnsureTodayRate on day 2: %v", err)
	}
	next := readGuild(t, path, guildID)
	const wantNextPrize = int64(45) // floor(50*90/100), with nothing carried over
	if got := next.Users["u2"].Chips - u2Before; got != wantNextPrize {
		t.Fatalf("the next winner gained %d chips, want %d — u1's unpayable remainder must not reach another player", got, wantNextPrize)
	}
	if got := next.Jackpot - jackpotBefore2; got != 5 {
		t.Fatalf("the pool gained %d on day 2, want 5 (the house's cut alone)", got)
	}
}

// TestBuyLotteryTickets_ReportsTheDrawTheTicketsAreActuallyIn closes the gap
// between the two clocks a request crosses: the `now` a command captured, and
// the state that request finds when the store's lock finally lets it in. The
// 09:00 announcement scheduler draws in between, so a purchase read at
// 08:59:59 lands on a guild whose draw for today is already settled. The
// tickets are right — drawLotteryLocked declines to re-draw, so they join
// tomorrow's pot — and it is only the instant reported back that used to be
// wrong: today's 09:00, an instant already in the past. Both entry points are
// checked, because /lottery status re-reads the same field a moment later and
// a fix to only one of them would contradict the other on screen.
func TestBuyLotteryTickets_ReportsTheDrawTheTicketsAreActuallyIn(t *testing.T) {
	// The draw under test has already run before this test starts; an empty
	// script makes any draw these calls perform fail loudly right here.
	const guildID, userID = "guild1", "u1"
	justBeforeNine := time.Date(2026, 7, 11, 8, 59, 59, 0, jst) // fixedNow is 12:00 on the 10th
	today, tomorrow := time.Date(2026, 7, 11, 9, 0, 0, 0, jst), time.Date(2026, 7, 12, 9, 0, 0, 0, jst)

	tests := []struct {
		name         string
		lastDrawDate string
		want         time.Time
	}{
		// The bug: the 11th's 09:00 draw ran while this request was in
		// flight, so the tickets are in the 12th's.
		{name: "the 09:00 draw ran while the request was in flight", lastDrawDate: "2026-07-11", want: tomorrow},
		// The ordinary path, which must not move: the last draw was
		// yesterday's, so today's 09:00 is still ahead.
		{name: "the last draw was yesterday", lastDrawDate: "2026-07-10", want: today},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := newTempStore(t)
			st.rng = &lotteryRand{t: t, float: 0.5}
			seedLotteryChips(t, st, guildID, map[string]int64{userID: 1000})
			if err := st.Update(func(d *Data) error {
				ensureGuildLocked(d, guildID).Lottery.DrawDate = tc.lastDrawDate
				return nil
			}); err != nil {
				t.Fatalf("seeding DrawDate: %v", err)
			}

			purchase, err := st.BuyLotteryTickets(guildID, userID, 2, justBeforeNine)
			if err != nil {
				t.Fatalf("BuyLotteryTickets: %v", err)
			}
			if !purchase.NextDrawAt.Equal(tc.want) {
				t.Fatalf("purchase.NextDrawAt = %s, want %s — the buyer was pointed at the wrong draw",
					purchase.NextDrawAt, tc.want)
			}
			// The tickets themselves were never in question: they are in the
			// pot the answer above now names.
			if purchase.UserTickets != 2 || purchase.TicketsSold != 2 {
				t.Fatalf("purchase = %+v, want 2 tickets held and 2 sold into the open pot", purchase)
			}

			view, err := st.LotteryStatus(guildID, userID, justBeforeNine)
			if err != nil {
				t.Fatalf("LotteryStatus: %v", err)
			}
			if !view.NextDrawAt.Equal(tc.want) {
				t.Fatalf("view.NextDrawAt = %s, want %s — status contradicts the purchase confirmation",
					view.NextDrawAt, tc.want)
			}
			if view.UserTickets != 2 {
				t.Fatalf("view.UserTickets = %d, want 2 — the pot named by NextDrawAt is not the one holding the tickets",
					view.UserTickets)
			}
		})
	}
}
