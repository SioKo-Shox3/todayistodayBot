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

// TestTopAssets_HandEditedMaxInt64IsNormalisedNotWrapped is the same guard
// one step further out. The test above proves the arithmetic is safe for
// balances AT the caps; this one proves the caps actually hold for the
// values that reach this loop. topAssetsLocked reads economy.Users
// directly, so a hand-edited math.MaxInt64 account never met a credit path
// and never met ensureAccountLocked either — Chips + Escrow + Coins*rate
// then wraps NEGATIVE and the wrapped account sorts last instead of first,
// silently, with no error or panic. Normalising each entry first is what
// keeps the bound the comment claims a real one.
func TestTopAssets_HandEditedMaxInt64IsNormalisedNotWrapped(t *testing.T) {
	st, _ := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{
		"user-a": {Chips: math.MaxInt64, Escrow: math.MaxInt64, Coins: math.MaxInt64},
		"user-b": {Chips: 100},
	})
	seedRate(t, st, "guild1", fixedNow, maxRate)

	entries, err := st.TopAssets("guild1", fixedNow, 10)
	if err != nil {
		t.Fatalf("TopAssets returned error: %v", err)
	}

	want := []RankEntry{
		{UserID: "user-a", TotalAssets: MaxChips + MaxChips + MaxCoins*maxRate}, // every field at its cap
		{UserID: "user-b", TotalAssets: 100},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v — a wrapped total sorts the hand-edited account last", i, entries[i], want[i])
		}
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

// A payout with nowhere to go is CAPPED, not refused (設計書 C-3b §2): the
// game closes, what fits lands and the rest is dropped. Refusing was the
// pre-C3B-12 behaviour — see TestSettleGame_AtTheCapAfterTheMonthlyBonus...
// below for what it cost.
func TestSettleGame_PayoutOverTheChipCapKeepsOnlyWhatFits(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: MaxChips - 500})
	if err := st.OpenGame(escrowGuild, escrowUser, "highlow", 1000, fixedNow); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}

	// Free chips are MaxChips-1500, so of a 2000 payout only 1500 fits.
	result, err := st.SettleGame(escrowGuild, escrowUser, 2000)
	if err != nil {
		t.Fatalf("SettleGame returned error: %v", err)
	}
	if result.Payout != 1500 || result.Owed != 2000 || result.Chips != MaxChips {
		t.Fatalf("SettleResult = %+v, want Payout 1500 / Owed 2000 / Chips %d", result, MaxChips)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, MaxChips, 0, "")
	// 純利 counts what LANDED: 1500 credited against a 1000 stake is +500, not
	// the +1000 the owed figure would book (§2's last bullet).
	assertSeasonNet(t, path, escrowGuild, escrowUser, 500)
}

// The whole point of C3B-12: the monthly bonus is itself what fills the
// account, so a refusal here rolls back the season switch that caused it and
// the next attempt rebuilds the same state — the hand could never be settled.
//
// July's leader carries a 100-chip hand into August with 300 chips of room.
// The rollover pays the 1位 bonus first, capped at those 300, leaving 100 of
// room for a 200-chip payout.
func TestSettleGame_AtTheCapAfterTheMonthlyBonusStillClosesTheGame(t *testing.T) {
	st, path := newTempStore(t)
	err := st.Update(func(d *Data) error {
		(*d)[seasonGuild] = &GuildEconomy{
			SeasonMonth: "2026-07",
			Users: map[string]*UserAccount{
				"leader": {
					Chips:          MaxChips - 300,
					Escrow:         100,
					EscrowGame:     string(GameHighLow),
					EscrowOpenedAt: "2026-07-31T23:55:00+09:00",
					SeasonNet:      500,
				},
			},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding the season returned error: %v", err)
	}

	result, err := st.at(seasonAugust).SettleGame(seasonGuild, "leader", 200)
	if err != nil {
		t.Fatalf("SettleGame returned error: %v — the cap must not refuse a settlement", err)
	}
	if result.Payout != 100 || result.Owed != 200 || result.Chips != MaxChips {
		t.Fatalf("SettleResult = %+v, want Payout 100 / Owed 200 / Chips %d", result, MaxChips)
	}
	assertHoldings(t, path, seasonGuild, "leader", MaxChips, 0, "")

	// The season switch COMMITTED. This is the assertion that fails on a
	// refusal: a rolled-back transaction leaves the guild in July with the
	// hand still in escrow, and every retry repeats it.
	economy := readGuild(t, path, seasonGuild)
	if economy.SeasonMonth != "2026-08" {
		t.Fatalf("SeasonMonth = %q, want 2026-08 — the rollover rolled back with the settlement", economy.SeasonMonth)
	}
	if len(economy.UnannouncedSeasons) != 1 || economy.UnannouncedSeasons[0].Month != "2026-07" {
		t.Fatalf("UnannouncedSeasons = %+v, want July's closed result", economy.UnannouncedSeasons)
	}
	// 純利: the rollover zeroed it, then the settlement booked 100 landed
	// against the 100 staked. The bonus itself stays out of 純利 (§3).
	assertSeasonNet(t, path, seasonGuild, "leader", 0)

	// The game is CLOSED, not waiting for a retry that can never succeed.
	if _, err := st.SettleGame(seasonGuild, "leader", 200); !errors.Is(err, ErrNoGameInProgress) {
		t.Fatalf("second SettleGame returned %v, want ErrNoGameInProgress", err)
	}
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

// TestRefundStaleEscrows_HandEditedMaxInt64DoesNotWrapTheRefund is the
// counterpart of the credit-path regression above, for the one path that
// moves chips without crediting them. moveFromEscrowLocked does
// `account.Chips += account.Escrow`; on a hand-edited
// Chips = Escrow = math.MaxInt64 file that sum WRAPS, and the refund
// PERSISTS Chips = -2 — a negative balance the next read normalises to
// zero, i.e. every chip on the account destroyed by the one operation whose
// whole contract is that it destroys none. Taking the refunded account
// through ensureAccountLocked first is what keeps the addends inside the
// caps, so the sum stays a sum.
func TestRefundStaleEscrows_HandEditedMaxInt64DoesNotWrapTheRefund(t *testing.T) {
	st, path := newTempStore(t)
	const guildID, userID = "guild1", "u1"
	seedAccount(t, st, guildID, userID, UserAccount{
		Chips: math.MaxInt64, Escrow: math.MaxInt64,
		EscrowGame: "blackjack", EscrowOpenedAt: "2026-09-14T12:00:00+09:00",
	})

	count, err := st.RefundStaleEscrows(fixedNow)
	if err != nil {
		t.Fatalf("RefundStaleEscrows returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("refunded %d accounts, want 1", count)
	}

	// What reached the FILE is the assertion that matters: a negative Chips
	// there is chips the next read silently clamps away.
	persisted := readAccount(t, path, guildID, userID)
	if persisted.Chips < 0 || persisted.Escrow < 0 {
		t.Fatalf("the refund wrapped: persisted Chips %d / Escrow %d", persisted.Chips, persisted.Escrow)
	}
	if persisted.Escrow != 0 || persisted.EscrowGame != "" || persisted.EscrowOpenedAt != "" {
		t.Fatalf("escrow not cleared: Escrow %d / Game %q / OpenedAt %q",
			persisted.Escrow, persisted.EscrowGame, persisted.EscrowOpenedAt)
	}
	// Both fields came back to MaxChips before the move, and the move
	// preserves their sum — the refund itself still truncates nothing.
	if persisted.Chips != 2*MaxChips {
		t.Fatalf("persisted Chips = %d, want %d (both caps, moved into one field)", persisted.Chips, 2*MaxChips)
	}

	// And the next read tops the balance out at the cap instead of finding
	// it at zero.
	view, err := st.ViewAccount(guildID, userID, fixedNow)
	if err != nil {
		t.Fatalf("ViewAccount: %v", err)
	}
	if view.Account.Chips != MaxChips {
		t.Fatalf("ViewAccount Chips = %d, want %d (the cap)", view.Account.Chips, MaxChips)
	}
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

	t.Run("a carryover near MaxInt64 is normalised, so neither the prize nor the cut is lost", func(t *testing.T) {
		// The C3-11 regression. LotteryPrize computes share+carryover, which
		// WRAPPED NEGATIVE on this input: creditChipsCappedLocked then paid
		// the winner 0, house+(prize-paid) came out hugely negative and moved
		// nothing, and the prize and the 10% cut both vanished while the pool
		// still had room. normalizeLotteryLocked pulls Carryover down to
		// MaxChips at the rollover's read point, so the whole draw is
		// computed on in-range values and every chip lands somewhere.
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
		// Sales 100 (2 tickets at 50), Carryover clamped to MaxChips:
		//   prize = 100*90/100 + 1e12 = 1e12+90, house = 100-90 = 10.
		// The winner holds 900 after the purchase, so the cap lets through
		// 1e12-900 and leaves a remainder of 990 — that remainder plus the
		// cut, 1000 chips, is what the pool must receive.
		const (
			wantPaid      = MaxChips - 900
			wantRemainder = (MaxChips + 90) - wantPaid
			wantPool      = 5_000 + wantRemainder + 10
		)
		economy := readGuild(t, path, guildID)
		account := economy.Users[userID]
		if account == nil {
			t.Fatalf("the winner's account is missing")
		}
		if account.Chips != MaxChips {
			t.Fatalf("winner chips = %d, want %d (900 + the %d the cap let through) — the prize must not vanish",
				account.Chips, MaxChips, wantPaid)
		}
		if economy.Jackpot != wantPool {
			t.Fatalf("pool = %d, want %d (5000 + the %d remainder + the 10 house cut) — the cut must not vanish either",
				economy.Jackpot, wantPool, wantRemainder)
		}
		if economy.Jackpot < JackpotSeed || economy.Jackpot > MaxJackpot {
			t.Fatalf("pool = %d, outside [%d, %d]", economy.Jackpot, JackpotSeed, MaxJackpot)
		}
		if got := economy.Lottery.LastDraw; got == nil || got.Prize != wantPaid {
			t.Fatalf("LastDraw = %+v, want Prize %d — the recorded prize is what was CREDITED", got, wantPaid)
		}
		// The pot reopens empty and in range, so the next draw starts clean
		// rather than inheriting the hand-edited number.
		if economy.Lottery.Carryover != 0 || economy.Lottery.Sales != 0 {
			t.Fatalf("reopened pot = Carryover %d / Sales %d, want 0 / 0",
				economy.Lottery.Carryover, economy.Lottery.Sales)
		}
	})
}

// TestNormalizeLottery_PullsHandEditedValuesIntoRange pins the normalisation
// point itself (設計書 C-3a §8): every persisted lottery number is dragged
// back into the range the arithmetic was sized for, and a value already in
// range is left exactly alone.
func TestNormalizeLottery_PullsHandEditedValuesIntoRange(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Lottery
		want Lottery
	}{
		{
			name: "a normal pot is untouched",
			in:   Lottery{Sales: 500, Carryover: 300, Tickets: map[string]int{"u1": 1, "u2": 10}},
			want: Lottery{Sales: 500, Carryover: 300, Tickets: map[string]int{"u1": 1, "u2": 10}},
		},
		{
			name: "negative sales and carryover become zero",
			in:   Lottery{Sales: -50, Carryover: -1},
			want: Lottery{Sales: 0, Carryover: 0},
		},
		{
			name: "sales and carryover past MaxChips are truncated to it",
			in:   Lottery{Sales: math.MaxInt64, Carryover: math.MaxInt64 - 40},
			want: Lottery{Sales: MaxChips, Carryover: MaxChips},
		},
		{
			name: "non-positive holdings are deleted, oversized ones trimmed",
			in:   Lottery{Tickets: map[string]int{"zero": 0, "neg": -3, "big": 11, "ok": 4}},
			want: Lottery{Tickets: map[string]int{"big": LotteryMaxTicketsPerDraw, "ok": 4}},
		},
		{
			name: "a map left empty goes back to nil",
			in:   Lottery{Tickets: map[string]int{"zero": 0}},
			want: Lottery{Tickets: nil},
		},
		{
			name: "an already-nil map stays nil",
			in:   Lottery{Sales: 1},
			want: Lottery{Sales: 1, Tickets: nil},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			economy := GuildEconomy{Lottery: tc.in}
			normalizeLotteryLocked(&economy)
			got := economy.Lottery
			if got.Sales != tc.want.Sales || got.Carryover != tc.want.Carryover {
				t.Fatalf("Sales/Carryover = %d/%d, want %d/%d",
					got.Sales, got.Carryover, tc.want.Sales, tc.want.Carryover)
			}
			if (got.Tickets == nil) != (tc.want.Tickets == nil) {
				t.Fatalf("Tickets = %v, want nil-ness %v", got.Tickets, tc.want.Tickets == nil)
			}
			if len(got.Tickets) != len(tc.want.Tickets) {
				t.Fatalf("Tickets = %v, want %v", got.Tickets, tc.want.Tickets)
			}
			for id, n := range tc.want.Tickets {
				if got.Tickets[id] != n {
					t.Fatalf("Tickets[%q] = %d, want %d (whole map %v)", id, got.Tickets[id], n, got.Tickets)
				}
			}
		})
	}
}

// TestLotteryDraw_SurvivesAHandEditedPot runs a real draw off a file whose
// Sales is negative and whose Tickets hold junk counts. Before the
// normalisation point this either mispaid or risked a wrapped weight total
// in PickLotteryWinner; now the junk holders are simply not in the draw and
// the negative sales pays nothing.
func TestLotteryDraw_SurvivesAHandEditedPot(t *testing.T) {
	st, path := newTempStore(t)
	// Exactly one roll, for the single surviving holder.
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0}}
	const guildID = "guild1"
	seedLotteryChips(t, st, guildID, map[string]int64{"ok": 100, "zero": 100, "neg": 100})
	if err := st.Update(func(d *Data) error {
		lottery := &ensureGuildLocked(d, guildID).Lottery
		lottery.DrawDate = jstDate(fixedNow)
		lottery.Sales = -1_000
		lottery.Carryover = 200
		lottery.Tickets = map[string]int{"ok": 999, "zero": 0, "neg": -5}
		return nil
	}); err != nil {
		t.Fatalf("seeding the hand-edited pot: %v", err)
	}
	seedJackpot(t, st, guildID, 5_000, 0)

	if _, err := st.EnsureTodayRate(guildID, daysAfter(1)); err != nil {
		t.Fatalf("EnsureTodayRate: %v", err)
	}
	economy := readGuild(t, path, guildID)
	draw := economy.Lottery.LastDraw
	if draw == nil {
		t.Fatalf("no draw was recorded — the junk pot must still name the one real holder")
	}
	// Sales normalises to 0, so the prize is the carryover alone and the
	// house cut is 0. "ok" is trimmed to the 10-ticket cap and is the only
	// entrant left, so it wins and its 100 chips grow by 200.
	if draw.WinnerID != "ok" {
		t.Fatalf("winner = %q, want \"ok\" — holdings of 0 and -5 bought nothing", draw.WinnerID)
	}
	if draw.TicketsSold != LotteryMaxTicketsPerDraw || draw.Buyers != 1 {
		t.Fatalf("draw = %d tickets / %d buyers, want %d / 1", draw.TicketsSold, draw.Buyers, LotteryMaxTicketsPerDraw)
	}
	if draw.Prize != 200 {
		t.Fatalf("prize = %d, want 200 — a negative Sales normalises to 0, leaving the carryover", draw.Prize)
	}
	if got := economy.Users["ok"].Chips; got != 300 {
		t.Fatalf("winner chips = %d, want 300 (100 + the 200 prize)", got)
	}
	if economy.Jackpot != 5_000 {
		t.Fatalf("pool = %d, want 5000 unchanged — normalised sales of 0 yields no house cut", economy.Jackpot)
	}
	// The junk holders never had chips taken and must not have gained any.
	for _, id := range []string{"zero", "neg"} {
		if got := economy.Users[id].Chips; got != 100 {
			t.Fatalf("%s chips = %d, want 100 untouched", id, got)
		}
	}
}

// TestLotteryRollover_NormalisesTheStoredPotBeforeAnyRead pins the ORDER the
// C3-11 regression turned on: the clamp has to happen before the draw and
// the seeding read the numbers, not after. A pot whose day is already
// stamped runs no draw, so what the file holds afterwards is the
// normalisation and nothing else.
func TestLotteryRollover_NormalisesTheStoredPotBeforeAnyRead(t *testing.T) {
	st, path := newTempStore(t)
	const guildID = "guild1"
	seedLotteryChips(t, st, guildID, map[string]int64{"u1": 100})
	if err := st.Update(func(d *Data) error {
		lottery := &ensureGuildLocked(d, guildID).Lottery
		// Stamped for a day AFTER the read below, so drawLotteryLocked
		// declines and only the normalisation can have written anything.
		lottery.DrawDate = jstDate(daysAfter(5))
		lottery.Sales = math.MaxInt64
		lottery.Carryover = -7
		lottery.Tickets = map[string]int{"u1": 42}
		return nil
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	view, err := st.LotteryStatus(guildID, "u1", fixedNow)
	if err != nil {
		t.Fatalf("LotteryStatus: %v", err)
	}
	// prize = floor(MaxChips*90/100) + 0 — a number, not a wrapped negative.
	if want := MaxChips * LotteryPrizePercent / 100; view.Prize != want {
		t.Fatalf("view prize = %d, want %d", view.Prize, want)
	}
	if view.UserTickets != LotteryMaxTicketsPerDraw {
		t.Fatalf("view tickets = %d, want %d", view.UserTickets, LotteryMaxTicketsPerDraw)
	}

	economy := readGuild(t, path, guildID)
	if economy.Lottery.Sales != MaxChips || economy.Lottery.Carryover != 0 {
		t.Fatalf("persisted pot = Sales %d / Carryover %d, want %d / 0 — the clamp must be WRITTEN, not just applied to a copy",
			economy.Lottery.Sales, economy.Lottery.Carryover, MaxChips)
	}
	if economy.Lottery.Tickets["u1"] != LotteryMaxTicketsPerDraw {
		t.Fatalf("persisted holding = %d, want %d", economy.Lottery.Tickets["u1"], LotteryMaxTicketsPerDraw)
	}
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
		economy := ensureGuildLocked(d, guildID)
		// Seeded as ALREADY POSTED. Without this, the file is indistinguishable
		// from a pre-queue one — a winner in last_draw, no queue, no posting
		// ever recorded — and migrateUnannouncedLocked rightly queues it, which
		// would put an entry in Unannounced that the quiet draw below did not
		// put there and make the assertion on it read the wrong mechanism.
		economy.LastAnnounced = older.Date
		lottery := &economy.Lottery
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
	// Nothing to announce: the queue is for draws that NAMED somebody.
	if len(quiet.Lottery.Unannounced) != 0 {
		t.Fatalf("Unannounced = %+v, want empty — a draw nobody entered has no winner to post", quiet.Lottery.Unannounced)
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
	if len(paid.Lottery.Unannounced) != 1 || paid.Lottery.Unannounced[0] != want {
		t.Fatalf("Unannounced = %+v, want [%+v] — a paid draw is queued for the 9am posting", paid.Lottery.Unannounced, want)
	}
	if paid.Lottery.Carryover != 0 {
		t.Fatalf("Carryover after a paid draw = %d, want 0 — it was handed to the winner", paid.Lottery.Carryover)
	}
	// 1000 - 50 spent + 345 won.
	if got := paid.Users[userID].Chips; got != 1295 {
		t.Fatalf("buyer's chips = %d, want 1295 (1000 - 50 + 345)", got)
	}
}

// TestLotteryDraw_NoWinnerConservesEveryChipOfTheSales is the conservation
// law stated for the branch that names nobody: whatever the pot held,
//
//	Δ(all chips) + Δ(jackpot pool) + Δ(carryover) = the sales the draw consumed.
//
// The branch used to keep only the prize (Carryover = prize) and drop the
// house's cut, which is invisible on a normal quiet day — no buyers means no
// sales means no cut — and costs real chips on a pot that holds sales with
// no ticket holders. Only a hand edit or a write that landed between the
// chip debit and Tickets produces that pot, which is why it is seeded
// directly here rather than bought.
func TestLotteryDraw_NoWinnerConservesEveryChipOfTheSales(t *testing.T) {
	st, path := newTempStore(t)
	// No rolls: a draw with no ticket holders must not reach the rng at all.
	st.rng = &lotteryRand{t: t, float: 0.5}
	const guildID = "guild1"
	seedLotteryChips(t, st, guildID, map[string]int64{"u1": 1000})
	seedJackpot(t, st, guildID, JackpotSeed, 0)
	const sales = int64(100)
	if err := st.Update(func(d *Data) error {
		lottery := &ensureGuildLocked(d, guildID).Lottery
		lottery.Sales, lottery.Carryover, lottery.Tickets = sales, 0, nil
		lottery.DrawDate = jstDate(fixedNow)
		return nil
	}); err != nil {
		t.Fatalf("seeding the ticket-less pot: %v", err)
	}
	before := readGuild(t, path, guildID)

	if _, err := st.EnsureTodayRate(guildID, daysAfter(1)); err != nil {
		t.Fatalf("EnsureTodayRate on the draw day: %v", err)
	}

	after := readGuild(t, path, guildID)
	chips := totalChips(after) - totalChips(before)
	pool := after.Jackpot - before.Jackpot
	carry := after.Lottery.Carryover - before.Lottery.Carryover
	if chips+pool+carry != sales {
		t.Fatalf("chips %+d + pool %+d + carryover %+d = %d, want %d (the sales the draw consumed): the draw is not conserving chips",
			chips, pool, carry, chips+pool+carry, sales)
	}
	// And the split itself: 90% forward, the 10% cut into the pool.
	if chips != 0 {
		t.Fatalf("accounts moved by %+d, want 0 — a draw with no winner pays nobody", chips)
	}
	if pool != 10 {
		t.Fatalf("the jackpot pool gained %d, want 10 (= sales - floor(sales*90/100)): the house's cut is not reaching the pool",
			pool)
	}
	if after.Lottery.Carryover != 90 {
		t.Fatalf("Carryover = %d, want 90 (= floor(sales*90/100), the whole prize)", after.Lottery.Carryover)
	}
	if after.Lottery.Sales != 0 || after.Lottery.DrawDate != jstDate(daysAfter(1)) {
		t.Fatalf("the pot was not reopened on the settled day: Sales %d, DrawDate %q (want 0 and %q)",
			after.Lottery.Sales, after.Lottery.DrawDate, jstDate(daysAfter(1)))
	}
	if after.Lottery.LastDraw != nil || len(after.Lottery.Unannounced) != 0 {
		t.Fatalf("LastDraw = %+v / Unannounced = %+v, want both empty — a draw that named nobody is not a result",
			after.Lottery.LastDraw, after.Lottery.Unannounced)
	}
}

// TestEnsureAccount_NormalisesHandEditedBalances pins the account's
// normalisation point. The property is the same one seedJackpotLocked and
// normalizeLotteryLocked establish for the pool and the pot: after the one
// read point every write path goes through, the balances are inside the
// range types.go sized the arithmetic for, whatever was on disk. The table
// therefore holds values no legitimate sequence can produce.
func TestEnsureAccount_NormalisesHandEditedBalances(t *testing.T) {
	cases := []struct {
		name     string
		in, want UserAccount
	}{
		{"in range is left alone", UserAccount{Chips: 800, Escrow: 200, Coins: 5}, UserAccount{Chips: 800, Escrow: 200, Coins: 5}},
		{"exactly at the caps is left alone", UserAccount{Chips: MaxChips, Escrow: MaxChips, Coins: MaxCoins}, UserAccount{Chips: MaxChips, Escrow: MaxChips, Coins: MaxCoins}},
		{"MaxInt64 balances come back to the caps", UserAccount{Chips: math.MaxInt64, Escrow: math.MaxInt64, Coins: math.MaxInt64}, UserAccount{Chips: MaxChips, Escrow: MaxChips, Coins: MaxCoins}},
		{"negative balances come back to zero", UserAccount{Chips: -1, Escrow: math.MinInt64, Coins: -7}, UserAccount{}},
		{"one field over does not disturb the others", UserAccount{Chips: MaxChips + 1, Escrow: 200, Coins: 5}, UserAccount{Chips: MaxChips, Escrow: 200, Coins: 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, path := newTempStore(t)
			const guildID, userID = "guild1", "u1"
			// seedAccounts writes the struct straight into the map, so the
			// out-of-range value actually reaches the file.
			seedAccounts(t, st, guildID, map[string]UserAccount{userID: tc.in})

			if _, err := st.ViewAccount(guildID, userID, fixedNow); err != nil {
				t.Fatalf("ViewAccount: %v", err)
			}

			got := readAccount(t, path, guildID, userID)
			if got.Chips != tc.want.Chips || got.Escrow != tc.want.Escrow || got.Coins != tc.want.Coins {
				t.Fatalf("after one read: Chips %d / Escrow %d / Coins %d, want %d / %d / %d",
					got.Chips, got.Escrow, got.Coins, tc.want.Chips, tc.want.Escrow, tc.want.Coins)
			}
		})
	}
}

// TestCreditChipsCapped_HandEditedMaxInt64DoesNotWrapTheHeadroom is the
// overflow regression the normalisation exists for. creditChipsCappedLocked
// computes MaxChips - Escrow - Chips; on Chips = Escrow = math.MaxInt64 that
// subtraction wraps to a large POSITIVE headroom, the credit is let through,
// and the account lands on a NEGATIVE balance — chips created out of a cap
// that was supposed to refuse them. Reading through ensureAccountLocked
// first is what makes the expression's operands small enough to be safe, so
// the fix is pinned here rather than at the subtraction.
func TestCreditChipsCapped_HandEditedMaxInt64DoesNotWrapTheHeadroom(t *testing.T) {
	st, path := newTempStore(t)
	const guildID, userID = "guild1", "u1"
	seedAccounts(t, st, guildID, map[string]UserAccount{
		userID: {Chips: math.MaxInt64, Escrow: math.MaxInt64, Coins: math.MaxInt64},
	})

	var credited int64
	if err := st.Update(func(d *Data) error {
		account := ensureAccountLocked(ensureGuildLocked(d, guildID), userID)
		credited = creditChipsCappedLocked(account, 1000)
		return nil
	}); err != nil {
		t.Fatalf("crediting the hand-edited account: %v", err)
	}

	if credited != 0 {
		t.Fatalf("credited %d chips to an account already at the cap, want 0", credited)
	}
	got := readAccount(t, path, guildID, userID)
	if got.Chips < 0 || got.Escrow < 0 {
		t.Fatalf("balances went negative: Chips %d / Escrow %d — the headroom subtraction wrapped", got.Chips, got.Escrow)
	}
	if got.Chips != MaxChips || got.Escrow != MaxChips {
		t.Fatalf("balances = Chips %d / Escrow %d, want both %d (the cap)", got.Chips, got.Escrow, MaxChips)
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
	// The third case needs its own clock: a guild that has never drawn is on
	// the "" branch of nextLotteryDrawAt, and 08:59:59 is the instant where
	// the two clocks can disagree.
	neverDrawnNow := time.Date(2026, 9, 14, 8, 59, 59, 0, jst)
	neverDrawnWant := time.Date(2026, 9, 14, 9, 0, 0, 0, jst) // nextRunAt(neverDrawnNow)

	tests := []struct {
		name         string
		now          time.Time
		lastDrawDate string
		want         time.Time
	}{
		// The bug: the 11th's 09:00 draw ran while this request was in
		// flight, so the tickets are in the 12th's.
		{name: "the 09:00 draw ran while the request was in flight", now: justBeforeNine, lastDrawDate: "2026-07-11", want: tomorrow},
		// The ordinary path, which must not move: the last draw was
		// yesterday's, so today's 09:00 is still ahead.
		{name: "the last draw was yesterday", now: justBeforeNine, lastDrawDate: "2026-07-10", want: today},
		// A guild nobody has ever bought into: DrawDate is "" and the
		// purchase's OWN rollover stamps it — with the 13th, because at
		// 08:59:59 the draw day has not turned over yet. Reading DrawDate
		// after that write must still answer this morning's 09:00, not push
		// the buyer to the 15th.
		{name: "the guild has never drawn", now: neverDrawnNow, lastDrawDate: "", want: neverDrawnWant},
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

			purchase, err := st.BuyLotteryTickets(guildID, userID, 2, tc.now)
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

			view, err := st.LotteryStatus(guildID, userID, tc.now)
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

// TestLotteryStatus_NeverDrawnGuildNamesThisMorningsDraw isolates the "" branch
// of nextLotteryDrawAt from any purchase. The table case above reaches that
// branch too, but only through BuyLotteryTickets — and the purchase's own
// rollover has already STAMPED DrawDate by the time the status call reads it,
// so the case that passes through it never asks a genuinely never-drawn guild
// anything. Here nothing has been bought: DrawDate is still "", and 08:59:59
// is the instant where the draw day (yesterday's, via the Hour() < 9
// correction) and the calendar day disagree by nine hours. The answer must be
// this morning's 09:00, not tomorrow's.
func TestLotteryStatus_NeverDrawnGuildNamesThisMorningsDraw(t *testing.T) {
	st, _ := newTempStore(t)
	// The pot is empty, so the rollover this call performs picks no winner and
	// never reads the rng — an empty script fails loudly if it ever does.
	st.rng = &lotteryRand{t: t, float: 0.5}
	const guildID, userID = "guild1", "u1"
	now := time.Date(2026, 9, 14, 8, 59, 59, 0, jst)
	want := time.Date(2026, 9, 14, 9, 0, 0, 0, jst)

	view, err := st.LotteryStatus(guildID, userID, now)
	if err != nil {
		t.Fatalf("LotteryStatus returned error: %v", err)
	}
	if !view.NextDrawAt.Equal(want) {
		t.Fatalf("NextDrawAt = %s, want %s — a guild that has never drawn was pushed past this morning's draw",
			view.NextDrawAt, want)
	}
	if view.LastDraw != nil {
		t.Fatalf("LastDraw = %+v, want nil: an empty pot rolls over without a draw to report", view.LastDraw)
	}
	if view.TicketsSold != 0 || view.UserTickets != 0 || view.Prize != 0 {
		t.Fatalf("view = %+v, want an empty pot (TicketsSold/UserTickets/Prize all 0)", view)
	}
}

// TestLotteryDraw_UnannouncedQueueKeepsOnlyTheLatestSeven pins the bound on
// the announcement backlog. A guild whose announce channel was deleted never
// calls MarkAnnounced, so without the cap every draw it ever runs would stay
// in casino.json forever. Overflow costs the ANNOUNCEMENT only — the prizes
// below are already in the winners' accounts.
func TestLotteryDraw_UnannouncedQueueKeepsOnlyTheLatestSeven(t *testing.T) {
	st, path := newTempStore(t)
	const guildID = "guild1"
	// Nine purchases, one per day, each by a different buyer. The purchase on
	// day n rolls over first, settling day n-1's single-buyer pot: day 0's
	// draw finds an empty pot (no winner, nothing queued) and days 1..8 each
	// name the previous day's buyer — eight queued draws for a cap of seven.
	chips := map[string]int64{}
	for day := 0; day <= 8; day++ {
		chips[fmt.Sprintf("u%d", day)] = 1000
	}
	seedLotteryChips(t, st, guildID, chips)
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0, 0, 0, 0, 0, 0, 0, 0}} // one buyer per draw

	for day := 0; day <= 8; day++ {
		if _, err := st.BuyLotteryTickets(guildID, fmt.Sprintf("u%d", day), 1, daysAfter(day)); err != nil {
			t.Fatalf("BuyLotteryTickets on day %d: %v", day, err)
		}
	}

	queue := readGuild(t, path, guildID).Lottery.Unannounced
	// 7 is written out rather than read from lotteryUnannouncedLimit: the
	// bound is the contract (設計書 §3「最大 7 件」), and a test that quotes
	// the constant back to itself passes for any value somebody types there.
	if len(queue) != 7 {
		t.Fatalf("Unannounced holds %d draws, want 7: %+v", len(queue), queue)
	}
	// Eight draws ran (days 1..8); the oldest, day 1's, is the one dropped.
	if got, want := queue[0].Date, jstDate(daysAfter(2)); got != want {
		t.Fatalf("the queue starts at %s, want %s — the OLDEST entry must be the one dropped", got, want)
	}
	if got, want := queue[len(queue)-1].Date, jstDate(daysAfter(8)); got != want {
		t.Fatalf("the queue ends at %s, want %s — the newest draw must always be kept", got, want)
	}
	for i, draw := range queue {
		if draw.WinnerID != fmt.Sprintf("u%d", i+1) {
			t.Fatalf("queue[%d] was won by %q, want u%d — the surviving entries are out of order: %+v",
				i, draw.WinnerID, i+1, queue)
		}
	}
}

// TestLotteryDraw_CarryoverOverTheCapGoesToThePoolNotTheFloor is the C3-17
// regression, and it is a leak of a different shape from the hand-edited
// files above: every number going IN is in range, so no clamp is repairing
// anything. A pot sitting at the MaxChips ceiling with sales on top produces
// a prize ABOVE the ceiling, the no-winner path used to store that prize in
// Carryover whole, and normalizeLotteryLocked then truncated it at the very
// next read — the implementation creating a number and its own normalisation
// point deleting it, one call apart. The draw now decides where the excess
// goes at the moment it writes: down the same road as the house cut, into
// the pool. 設計書 C-3a §8 — the cap on the POOL is the only place a chip
// leaves the system; an account's or the carryover's ceiling redirects, it
// does not destroy.
func TestLotteryDraw_CarryoverOverTheCapGoesToThePoolNotTheFloor(t *testing.T) {
	st, path := newTempStore(t)
	// No roll is scripted: nobody holds a ticket, so PickLotteryWinner
	// returns before it asks for one. A draw that named somebody here would
	// fail the test loudly instead of quietly.
	st.rng = &lotteryRand{t: t, float: 0.5}
	const guildID, userID = "guild1", "u1"
	seedLotteryChips(t, st, guildID, map[string]int64{userID: 1_000})
	if err := st.Update(func(d *Data) error {
		lottery := &ensureGuildLocked(d, guildID).Lottery
		lottery.DrawDate = jstDate(fixedNow) // today is settled; tomorrow draws
		lottery.Sales = 100
		lottery.Carryover = MaxChips // AT the ceiling, not over it
		return nil
	}); err != nil {
		t.Fatalf("seeding the pot: %v", err)
	}
	seedJackpot(t, st, guildID, 5_000, 0)

	// prize = floor(100*90/100) + 1e12 = MaxChips+90, house = 10. The 90 that
	// Carryover has no room for joins the cut in the pool: 5000 + 10 + 90.
	const wantPool = int64(5_100)
	if _, err := st.LotteryStatus(guildID, userID, daysAfter(1)); err != nil {
		t.Fatalf("LotteryStatus (first read, runs the draw): %v", err)
	}
	drawn := readGuild(t, path, guildID)
	if drawn.Lottery.LastDraw != nil {
		t.Fatalf("LastDraw = %+v, want nil — nobody entered, so no result may be recorded", drawn.Lottery.LastDraw)
	}
	if drawn.Lottery.Carryover != MaxChips {
		t.Fatalf("Carryover = %d, want MaxChips %d — the draw must persist a carryover already inside its ceiling",
			drawn.Lottery.Carryover, MaxChips)
	}
	if drawn.Jackpot != wantPool {
		t.Fatalf("pool = %d, want %d (5000 + the 10 house cut + the 90 the carryover had no room for)", drawn.Jackpot, wantPool)
	}
	// Conservation across the draw: the pot held Sales 100 + Carryover
	// MaxChips, and all of it is still somewhere.
	if got := drawn.Lottery.Carryover + drawn.Jackpot - 5_000; got != MaxChips+100 {
		t.Fatalf("carryover + pool gained = %d, want %d (sales + carryover) — the draw destroyed chips", got, MaxChips+100)
	}

	// The second read is where the loss used to happen: the draw does not run
	// again (the day is stamped), so normalizeLotteryLocked is the only thing
	// that touches the pot — and it must find nothing to repair.
	if _, err := st.LotteryStatus(guildID, userID, daysAfter(1)); err != nil {
		t.Fatalf("LotteryStatus (second read): %v", err)
	}
	after := readGuild(t, path, guildID)
	if after.Lottery.Carryover != drawn.Lottery.Carryover || after.Jackpot != drawn.Jackpot {
		t.Fatalf("second read moved the money: carryover %d -> %d, pool %d -> %d — a clamp may only repair what the FILE brought in, never a value this package wrote",
			drawn.Lottery.Carryover, after.Lottery.Carryover, drawn.Jackpot, after.Jackpot)
	}
	if got := totalChips(after); got != 1_000 {
		t.Fatalf("player chips = %d, want 1000 untouched — no ticket was bought and no prize was paid", got)
	}
}

// TestSpin_HandEditedJackpotAccumCannotWrapTheAccrual covers the carry that
// seedJackpotLocked used to leave alone. JackpotAccum is in [0, 100) after
// every real accrual, so anything larger is a hand edit — and math.MaxInt64
// wrapped `JackpotAccum += bet*percent` NEGATIVE, after which the division
// fed the pool nothing and the negative carry persisted for the next spin to
// inherit. Normalising it at the same read point as the pool keeps the
// accrual on numbers the range analysis in types.go actually covers.
func TestSpin_HandEditedJackpotAccumCannotWrapTheAccrual(t *testing.T) {
	st, path := newTempStore(t)
	const guildID, userID = "guild1", "u1"
	seedAccount(t, st, guildID, userID, UserAccount{Chips: 50_000})
	seedJackpot(t, st, guildID, 5_000, math.MaxInt64)
	st.rng = &scriptedIntn{t: t, intns: losingReels(t)}

	result, err := st.Spin(guildID, userID, 1_000)
	if err != nil {
		t.Fatalf("Spin returned error: %v", err)
	}
	// MaxInt64 normalises to MaxInt64%100 = 7 sub-chip units; the 1000-chip
	// bet adds 2000 of them, so 20 whole chips reach the pool and 7 stay.
	const wantPool, wantAccum = int64(5_020), int64(7)
	if result.JackpotPool != wantPool {
		t.Fatalf("reported pool = %d, want %d — the 2%% accrual must land even on a corrupt carry", result.JackpotPool, wantPool)
	}
	economy := readGuild(t, path, guildID)
	if economy.Jackpot != wantPool {
		t.Fatalf("persisted pool = %d, want %d", economy.Jackpot, wantPool)
	}
	if economy.Jackpot < JackpotSeed || economy.Jackpot > MaxJackpot {
		t.Fatalf("pool = %d, outside [%d, %d] — a wrapped accrual is money the next 7️⃣7️⃣7️⃣ hands to a player",
			economy.Jackpot, JackpotSeed, MaxJackpot)
	}
	if economy.JackpotAccum != wantAccum {
		t.Fatalf("JackpotAccum = %d, want %d — the carry must be normalised into [0, %d) at the read point, not carried forward wrapped",
			economy.JackpotAccum, wantAccum, jackpotAccumScale)
	}
}

// --- duel (設計書 C-3b §4.5) ------------------------------------------------

// duelOpponent is the second seat at the table; the challenger reuses the
// escrow tests' escrowUser/escrowGuild so both families exercise the same
// account shape.
const duelOpponent = "user2"

// openDuelChallenge puts bet chips of the challenger's into escrow under the
// duel's game name — the state a pending challenge leaves behind.
func openDuelChallenge(t *testing.T, st *Store, bet int64) {
	t.Helper()
	if err := st.OpenGame(escrowGuild, escrowUser, string(GameDuel), bet, fixedNow); err != nil {
		t.Fatalf("OpenGame(duel) returned error: %v", err)
	}
}

func assertSeasonNet(t *testing.T, path, guildID, userID string, want int64) {
	t.Helper()
	if got := readAccount(t, path, guildID, userID).SeasonNet; got != want {
		t.Fatalf("%s SeasonNet = %d, want %d", userID, got, want)
	}
}

// The duel's whole contract is that it is a transfer, not a game against the
// house: 設計書 §2 forbids a cut here. So assert the SUM of the two accounts,
// not just each payout — a house rake, a lost stake and a double credit all
// show up as a change in the total, whichever side the coin landed on.
func TestAcceptDuel_MovesThePotToTheWinnerAndKeepsTheTwoAccountsSummedUnchanged(t *testing.T) {
	tests := []struct {
		name                             string
		challengerWins                   bool
		wantChallengerChips              int64
		wantOpponentChips                int64
		wantChallengerNet, wantOpponnNet int64
	}{
		{
			name: "the challenger calls it right", challengerWins: true,
			wantChallengerChips: 1200, wantOpponentChips: 800,
			wantChallengerNet: 200, wantOpponnNet: -200,
		},
		{
			name: "the opponent calls it right", challengerWins: false,
			wantChallengerChips: 800, wantOpponentChips: 1200,
			wantChallengerNet: -200, wantOpponnNet: 200,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, path := newTempStore(t)
			seedAccounts(t, st, escrowGuild, map[string]UserAccount{
				escrowUser:   {Chips: 1000},
				duelOpponent: {Chips: 1000},
			})
			openDuelChallenge(t, st, 200)

			got, err := st.AcceptDuel(escrowGuild, escrowUser, duelOpponent, 200, tc.challengerWins)
			if err != nil {
				t.Fatalf("AcceptDuel returned error: %v", err)
			}

			wantWinner := duelOpponent
			wantChallengerPayout, wantOpponentPayout := int64(0), int64(400)
			if tc.challengerWins {
				wantWinner, wantChallengerPayout, wantOpponentPayout = escrowUser, 400, 0
			}
			want := DuelSettlement{
				ChallengerWins:   tc.challengerWins,
				WinnerID:         wantWinner,
				ChallengerPayout: wantChallengerPayout,
				OpponentPayout:   wantOpponentPayout,
				ChallengerChips:  tc.wantChallengerChips,
				OpponentChips:    tc.wantOpponentChips,
			}
			if got != want {
				t.Fatalf("settlement = %+v, want %+v", got, want)
			}

			// Both escrows released and both game names cleared: a duel that
			// left either side staked would lock that player out of every
			// other game with no board able to settle them.
			assertHoldings(t, path, escrowGuild, escrowUser, tc.wantChallengerChips, 0, "")
			assertHoldings(t, path, escrowGuild, duelOpponent, tc.wantOpponentChips, 0, "")

			// Zero-sum, stated over the persisted file rather than over the
			// struct the call returned.
			challenger := readAccount(t, path, escrowGuild, escrowUser)
			opponent := readAccount(t, path, escrowGuild, duelOpponent)
			total := challenger.Chips + challenger.Escrow + opponent.Chips + opponent.Escrow
			if total != 2000 {
				t.Fatalf("the two accounts hold %d chips between them, want the 2000 they started with — a duel is zero-sum (設計書 §2)", total)
			}

			assertSeasonNet(t, path, escrowGuild, escrowUser, tc.wantChallengerNet)
			assertSeasonNet(t, path, escrowGuild, duelOpponent, tc.wantOpponnNet)
			// SeasonNet is a ranking figure, not currency: the two sides of a
			// duel cancel exactly, so a season's totals cannot drift.
			if challenger.SeasonNet+opponent.SeasonNet != 0 {
				t.Fatalf("SeasonNet sums to %d, want 0", challenger.SeasonNet+opponent.SeasonNet)
			}
		})
	}
}

// 設計書 §2: a winner already at the cap takes only the chips that fit, and
// SeasonNet counts what actually landed — not what was owed. Counting the
// owed amount would print a rank the balance beside it never earned.
func TestAcceptDuel_WinnerAtTheCapTakesOnlyWhatFitsAndSeasonNetCountsThat(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, escrowGuild, map[string]UserAccount{
		// 100 chips of headroom, with a 200-chip stake about to come out of
		// it: after staking, the account can take back 300 of the 400 pot.
		escrowUser:   {Chips: MaxChips - 100},
		duelOpponent: {Chips: 1000},
	})
	openDuelChallenge(t, st, 200)

	got, err := st.AcceptDuel(escrowGuild, escrowUser, duelOpponent, 200, true)
	if err != nil {
		t.Fatalf("AcceptDuel returned error: %v", err)
	}
	if got.ChallengerPayout != 300 {
		t.Fatalf("ChallengerPayout = %d, want the 300 that fit under MaxChips (not the 400 owed)", got.ChallengerPayout)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, MaxChips, 0, "")
	assertHoldings(t, path, escrowGuild, duelOpponent, 800, 0, "")

	// Received 300 against a 200 stake: +100, not the +200 an uncapped win
	// would have paid.
	assertSeasonNet(t, path, escrowGuild, escrowUser, 100)
	assertSeasonNet(t, path, escrowGuild, duelOpponent, -200)
}

// Every refusal must leave the challenge exactly as it found it. 設計書 §4.5
// is explicit that a failed acceptance does NOT refund the challenger: the
// board stays live so the opponent can find the chips, decline, or let it
// expire.
func TestAcceptDuel_RefusalsLeaveTheChallengersStakeExactlyWhereItWas(t *testing.T) {
	tests := []struct {
		name string
		// setUp runs after the challenger's 200-chip duel stake is in place.
		setUp     func(t *testing.T, st *Store)
		opponent  string
		bet       int64
		wantErr   error
		wantIsErr func(error) bool
	}{
		{
			name: "the opponent cannot cover the bet",
			setUp: func(t *testing.T, st *Store) {
				seedAccounts(t, st, escrowGuild, map[string]UserAccount{duelOpponent: {Chips: 50}})
			},
			opponent: duelOpponent, bet: 200,
			wantIsErr: func(err error) bool {
				var insufficient *ErrInsufficientChips
				return errors.As(err, &insufficient) && insufficient.Balance == 50
			},
		},
		{
			name: "the opponent is already mid-game",
			setUp: func(t *testing.T, st *Store) {
				seedAccounts(t, st, escrowGuild, map[string]UserAccount{duelOpponent: {Chips: 1000}})
				if err := st.OpenGame(escrowGuild, duelOpponent, "blackjack", 100, fixedNow); err != nil {
					t.Fatalf("seeding the opponent's blackjack hand: %v", err)
				}
			},
			opponent: duelOpponent, bet: 200, wantErr: ErrGameInProgress,
		},
		{
			// Both seats resolve to the SAME *UserAccount, so the settlement
			// would stake one bet and pay out two.
			name: "both seats are the same user", opponent: escrowUser, bet: 200, wantErr: ErrDuelSelf,
		},
		{name: "a bet of zero", opponent: duelOpponent, bet: 0, wantErr: ErrInvalidAmount},
		{name: "a bet above the cap", opponent: duelOpponent, bet: MaxChips + 1, wantErr: ErrInvalidAmount},
		{
			// 300 would leave the table for the 350 that was staked.
			name:     "the bet is not what the challenger staked",
			opponent: duelOpponent, bet: 150, wantErr: ErrDuelStakeMismatch,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, path := newTempStore(t)
			seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})
			openDuelChallenge(t, st, 200)
			if tc.setUp != nil {
				tc.setUp(t, st)
			}

			_, err := st.AcceptDuel(escrowGuild, escrowUser, tc.opponent, tc.bet, true)
			if tc.wantIsErr != nil {
				if !tc.wantIsErr(err) {
					t.Fatalf("AcceptDuel error = %v, which is not the expected refusal", err)
				}
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatalf("AcceptDuel error = %v, want %v", err, tc.wantErr)
			}

			// The challenge survives, untouched, in every case.
			assertHoldings(t, path, escrowGuild, escrowUser, 800, 200, string(GameDuel))
			assertSeasonNet(t, path, escrowGuild, escrowUser, 0)
		})
	}
}

// The store-level half of the double-settlement guard: the challenger must be
// holding a DUEL stake of this size. Anything else is a button from a board
// that is already spent.
func TestAcceptDuel_RefusesWhenTheChallengerHoldsNoDuelStake(t *testing.T) {
	tests := []struct {
		name  string
		setUp func(t *testing.T, st *Store)
	}{
		{name: "no game at all", setUp: func(t *testing.T, st *Store) {}},
		{
			// Without the EscrowGame check this would settle the blackjack
			// hand's stake against a duel nobody is playing.
			name: "some other game's stake",
			setUp: func(t *testing.T, st *Store) {
				if err := st.OpenGame(escrowGuild, escrowUser, "blackjack", 200, fixedNow); err != nil {
					t.Fatalf("seeding the blackjack hand: %v", err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, path := newTempStore(t)
			seedAccounts(t, st, escrowGuild, map[string]UserAccount{
				escrowUser:   {Chips: 1000},
				duelOpponent: {Chips: 1000},
			})
			tc.setUp(t, st)

			_, err := st.AcceptDuel(escrowGuild, escrowUser, duelOpponent, 200, true)
			if !errors.Is(err, ErrNoGameInProgress) {
				t.Fatalf("AcceptDuel error = %v, want ErrNoGameInProgress", err)
			}
			// A refused acceptance must not have staked the opponent.
			assertHoldings(t, path, escrowGuild, duelOpponent, 1000, 0, "")
		})
	}
}

func TestDeclineDuel_ReturnsEveryChipAndNeverTouchesTheOpponent(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})
	openDuelChallenge(t, st, 200)

	if err := st.DeclineDuel(escrowGuild, escrowUser); err != nil {
		t.Fatalf("DeclineDuel returned error: %v", err)
	}

	assertHoldings(t, path, escrowGuild, escrowUser, 1000, 0, "")
	// A declined challenge is not a game result (設計書 §3), so it must not
	// move the season ranking.
	assertSeasonNet(t, path, escrowGuild, escrowUser, 0)
	// The opponent staked nothing, so they must not even have an account
	// opened for them — an auto-created account carries a welcome bonus,
	// which would mint 1,000 chips for never pressing a button.
	if account := readGuild(t, path, escrowGuild).Users[duelOpponent]; account != nil {
		t.Fatalf("declining opened an account for the opponent: %+v", *account)
	}
}

// A hand-edited balance over the cap keeps every chip through a decline. That
// is why the refund is a move rather than a credit: a capped credit would
// destroy the excess, and a refused one would strand the stake forever.
func TestDeclineDuel_HandEditedOverTheCapKeepsEveryChip(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{
		Chips:          MaxChips,
		Escrow:         500,
		EscrowGame:     string(GameDuel),
		EscrowOpenedAt: fixedNow.In(jst).Format(time.RFC3339),
	})

	if err := st.DeclineDuel(escrowGuild, escrowUser); err != nil {
		t.Fatalf("DeclineDuel returned error: %v", err)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, MaxChips+500, 0, "")
}

func TestDeclineDuel_RefusesASecondRefundAndAnotherGamesStake(t *testing.T) {
	t.Run("a second decline", func(t *testing.T) {
		st, path := newTempStore(t)
		seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})
		openDuelChallenge(t, st, 200)
		if err := st.DeclineDuel(escrowGuild, escrowUser); err != nil {
			t.Fatalf("first DeclineDuel returned error: %v", err)
		}

		// Refunding again would mint 200 chips out of nothing.
		if err := st.DeclineDuel(escrowGuild, escrowUser); !errors.Is(err, ErrNoGameInProgress) {
			t.Fatalf("second DeclineDuel error = %v, want ErrNoGameInProgress", err)
		}
		assertHoldings(t, path, escrowGuild, escrowUser, 1000, 0, "")
	})

	t.Run("a stale duel button after another game opened", func(t *testing.T) {
		st, path := newTempStore(t)
		seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})
		if err := st.OpenGame(escrowGuild, escrowUser, "blackjack", 200, fixedNow); err != nil {
			t.Fatalf("OpenGame(blackjack) returned error: %v", err)
		}

		if err := st.DeclineDuel(escrowGuild, escrowUser); !errors.Is(err, ErrNoGameInProgress) {
			t.Fatalf("DeclineDuel error = %v, want ErrNoGameInProgress", err)
		}
		// The blackjack stake survives for its own board to settle.
		assertHoldings(t, path, escrowGuild, escrowUser, 800, 200, "blackjack")
	})
}

func TestAcceptDuel_ConcurrentAcceptancesSettleExactlyOnce(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, escrowGuild, map[string]UserAccount{
		escrowUser:   {Chips: 1000},
		duelOpponent: {Chips: 1000},
	})
	openDuelChallenge(t, st, 200)

	// 50 goroutines race to accept the SAME challenge — a button double-tap,
	// or the sweeper arriving while the opponent presses ⚔️. Exactly one may
	// get through: a second settlement pays another 400-chip pot against a
	// 200-chip stake that is no longer there.
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
			_, err := st.AcceptDuel(escrowGuild, escrowUser, duelOpponent, 200, true)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrNoGameInProgress):
			default:
				otherErrs = append(otherErrs, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(otherErrs) > 0 {
		t.Fatalf("unexpected errors from concurrent AcceptDuel: %v", otherErrs)
	}
	if succeeded != 1 {
		t.Fatalf("%d of %d concurrent AcceptDuel calls succeeded, want exactly 1", succeeded, goroutines)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, 1200, 0, "")
	assertHoldings(t, path, escrowGuild, duelOpponent, 800, 0, "")
	assertSeasonNet(t, path, escrowGuild, escrowUser, 200)
	assertSeasonNet(t, path, escrowGuild, duelOpponent, -200)
}

// --- SeasonNet: the month's 純利 across every game (設計書 C-3b §3) --------
//
// The one rule all of these pin: SeasonNet moves by (what the account
// actually RECEIVED − what it staked), once per settlement, and only for
// settlements. The duel's half of it lives with the duel tests above.

// TestSpin_SeasonNetAccumulatesEachSpinsNet covers the slot, where the stake
// and the credit happen in the same call. The final assertion is the one that
// makes it more than arithmetic: with no handout anywhere in the run, the
// season's 純利 must equal the whole change in the balance — that identity is
// what a missed or double-counted settlement breaks.
func TestSpin_SeasonNetAccumulatesEachSpinsNet(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, "guild1", "user-1", UserAccount{Chips: 1000})

	// 🍒🍒🍒 (×7) on a 100 bet: 700 in, 100 staked.
	st.rng = &scriptedIntn{t: t, intns: []int{symbolDraw(t, SymbolCherry)}}
	if _, err := st.Spin("guild1", "user-1", 100); err != nil {
		t.Fatalf("Spin (win) returned error: %v", err)
	}
	assertSeasonNet(t, path, "guild1", "user-1", 600)

	// 🍋🍇🔔 pays nothing: the whole stake is the loss.
	st.rng = &scriptedIntn{t: t, intns: losingReels(t)}
	if _, err := st.Spin("guild1", "user-1", 100); err != nil {
		t.Fatalf("Spin (loss) returned error: %v", err)
	}
	assertSeasonNet(t, path, "guild1", "user-1", 500)

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != 1500 {
		t.Fatalf("Chips = %d, want 1500 (1000 - 100 + 700 - 100)", account.Chips)
	}
	if account.SeasonNet != account.Chips-1000 {
		t.Fatalf("SeasonNet = %d but the balance moved by %d — with no handout in the run the two must agree", account.SeasonNet, account.Chips-1000)
	}
}

// A refused spin persists nothing, so it must not leave a loss on the season
// board either: the chips never left the account.
func TestSpin_RefusedSpinLeavesSeasonNetUntouched(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, "guild1", "user-1", UserAccount{Chips: 50})
	st.rng = forbiddenRand{t: t, reason: "an unaffordable bet is refused before the reels are drawn"}

	if _, err := st.Spin("guild1", "user-1", 100); err == nil {
		t.Fatal("Spin accepted a bet the balance cannot cover")
	}
	assertSeasonNet(t, path, "guild1", "user-1", 0)
}

// TestSettleGame_SeasonNetUsesTheWholeEscrowAsTheStake is the ハイ&ロー /
// ブラックジャック half. SettleGame is told the payout and never the bet, so
// the stake it books is the escrow it is about to release — which for a
// doubled hand is bet + ダブル, not bet. The 400-on-200 rows are the ones that
// fail if the raise is left out.
func TestSettleGame_SeasonNetUsesTheWholeEscrowAsTheStake(t *testing.T) {
	tests := []struct {
		name          string
		game          GameKind
		bet, raise    int64
		payout        int64
		wantNet       int64
		wantChipsLeft int64
	}{
		{"ハイ&ローの勝ち", GameHighLow, 100, 0, 200, 100, 1100},
		{"ハイ&ローの負け", GameHighLow, 100, 0, 0, -100, 900},
		{"引き分けは賭け金が戻るだけ", GameHighLow, 100, 0, 100, 0, 1000},
		{"ダブルした勝ち", GameBlackjack, 100, 100, 400, 200, 1200},
		{"ダブルした負け", GameBlackjack, 100, 100, 0, -200, 800},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, path := newTempStore(t)
			seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})
			if err := st.OpenGame(escrowGuild, escrowUser, string(tc.game), tc.bet, fixedNow); err != nil {
				t.Fatalf("OpenGame returned error: %v", err)
			}
			if tc.raise > 0 {
				if err := st.AddToEscrow(escrowGuild, escrowUser, tc.raise); err != nil {
					t.Fatalf("AddToEscrow returned error: %v", err)
				}
			}
			if _, err := st.SettleGame(escrowGuild, escrowUser, tc.payout); err != nil {
				t.Fatalf("SettleGame returned error: %v", err)
			}
			assertHoldings(t, path, escrowGuild, escrowUser, tc.wantChipsLeft, 0, "")
			assertSeasonNet(t, path, escrowGuild, escrowUser, tc.wantNet)
		})
	}
}

// A settlement that never happened must not be booked. Both refusals leave
// the escrow in place for a retry, so booking a stake here would count the
// same hand twice once that retry lands.
func TestSettleGame_RefusalsLeaveSeasonNetUntouched(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})

	// No game in flight at all.
	if _, err := st.SettleGame(escrowGuild, escrowUser, 200); !errors.Is(err, ErrNoGameInProgress) {
		t.Fatalf("SettleGame with no escrow returned %v, want ErrNoGameInProgress", err)
	}
	assertSeasonNet(t, path, escrowGuild, escrowUser, 0)

	// A negative payout aborts the whole transaction. (The chip cap is NOT on
	// this list since C3B-12: it caps the credit instead of refusing it, so
	// the 純利 it books is the landed amount rather than nothing — that is
	// TestSettleGame_PayoutOverTheChipCapKeepsOnlyWhatFits.)
	if err := st.OpenGame(escrowGuild, escrowUser, string(GameHighLow), 100, fixedNow); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}
	if _, err := st.SettleGame(escrowGuild, escrowUser, -1); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("SettleGame with a negative payout returned %v, want ErrInvalidAmount", err)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, 900, 100, string(GameHighLow))
	assertSeasonNet(t, path, escrowGuild, escrowUser, 0)
}

// Successive games accumulate rather than overwrite.
func TestSettleGame_SeasonNetAccumulatesAcrossGames(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})

	for _, step := range []struct {
		game           GameKind
		bet, payout    int64
		wantRunningNet int64
	}{
		{GameHighLow, 100, 200, 100},
		{GameBlackjack, 300, 0, -200},
		{GameHighLow, 50, 100, -150},
	} {
		if err := st.OpenGame(escrowGuild, escrowUser, string(step.game), step.bet, fixedNow); err != nil {
			t.Fatalf("OpenGame(%s) returned error: %v", step.game, err)
		}
		if _, err := st.SettleGame(escrowGuild, escrowUser, step.payout); err != nil {
			t.Fatalf("SettleGame(%s) returned error: %v", step.game, err)
		}
		assertSeasonNet(t, path, escrowGuild, escrowUser, step.wantRunningNet)
	}

	account := readAccount(t, path, escrowGuild, escrowUser)
	if account.SeasonNet != account.Chips-1000 {
		t.Fatalf("SeasonNet = %d but the balance moved by %d", account.SeasonNet, account.Chips-1000)
	}
}

// TestLottery_SeasonNetBooksTheTicketAtPurchaseAndThePrizeAtTheDraw pins the
// lottery's two halves, which happen on different DAYS. The final figure is
// the point: a sole buyer who wins their own pot back is still down the
// house's 10%, and that is what the season board must show — not 0.
func TestLottery_SeasonNetBooksTheTicketAtPurchaseAndThePrizeAtTheDraw(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0}}
	yesterday := fixedNow.AddDate(0, 0, -1)

	if _, err := st.BuyLotteryTickets("guild1", "u1", 2, yesterday); err != nil {
		t.Fatalf("BuyLotteryTickets returned error: %v", err)
	}
	// The ticket price is booked where the chips actually leave, not at the
	// draw: a buyer whose draw never runs is still out the money.
	assertSeasonNet(t, path, "guild1", "u1", -100)

	if _, err := st.LotteryStatus("guild1", "u1", fixedNow); err != nil {
		t.Fatalf("LotteryStatus returned error: %v", err)
	}
	draw := readGuild(t, path, "guild1").Lottery.LastDraw
	if draw == nil || draw.WinnerID != "u1" || draw.Prize != 90 {
		t.Fatalf("LastDraw = %+v, want u1 winning 90 of the 100 sales", draw)
	}
	assertSeasonNet(t, path, "guild1", "u1", -10)
}

// A refused purchase takes no chips, so it books nothing — the same rule the
// refused spin follows, on the one path in this package that COMMITS its
// transaction while refusing (危険地帯: the rollover must survive).
func TestLottery_RefusedPurchaseLeavesSeasonNetUntouched(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = &lotteryRand{t: t, float: 0.5}
	seedAccounts(t, st, "guild1", map[string]UserAccount{"u1": {Chips: 10}})

	if _, err := st.BuyLotteryTickets("guild1", "u1", 1, fixedNow); err == nil {
		t.Fatal("BuyLotteryTickets accepted a ticket the balance cannot cover")
	}
	assertSeasonNet(t, path, "guild1", "u1", 0)
}

// TestLottery_SeasonNetCountsWhatTheWinnerCouldActuallyTake is the cap rule
// (§2) on the lottery path: a winner already at MaxChips takes only what
// fits, the remainder goes to the jackpot pool rather than to them, and the
// season board must show the amount that landed. PickLotteryWinner walks the
// buyers in sorted order, so u1 holds the first two of the twelve tickets and
// roll 0 names them.
func TestLottery_SeasonNetCountsWhatTheWinnerCouldActuallyTake(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = &lotteryRand{t: t, float: 0.5, rolls: []int{0}}
	seedAccounts(t, st, "guild1", map[string]UserAccount{
		"u1": {Chips: MaxChips},
		"u2": {Chips: 1000},
	})
	yesterday := fixedNow.AddDate(0, 0, -1)

	if _, err := st.BuyLotteryTickets("guild1", "u1", 2, yesterday); err != nil {
		t.Fatalf("BuyLotteryTickets(u1) returned error: %v", err)
	}
	if _, err := st.BuyLotteryTickets("guild1", "u2", 10, yesterday); err != nil {
		t.Fatalf("BuyLotteryTickets(u2) returned error: %v", err)
	}
	if _, err := st.LotteryStatus("guild1", "u1", fixedNow); err != nil {
		t.Fatalf("LotteryStatus returned error: %v", err)
	}

	// 600 sales -> a 540 prize, but u1's headroom is only the 100 they spent.
	draw := readGuild(t, path, "guild1").Lottery.LastDraw
	if draw == nil || draw.WinnerID != "u1" || draw.Prize != 100 {
		t.Fatalf("LastDraw = %+v, want u1 credited 100 of the 540 prize", draw)
	}
	assertSeasonNet(t, path, "guild1", "u1", 0) // -100 staked, 100 received
	assertSeasonNet(t, path, "guild1", "u2", -500)
}

// TestSeasonNet_HandoutsAndExchangesAreExcluded is the negative half of §3:
// the board measures PLAYING, so every path that hands chips over or merely
// changes their denomination must leave it at 0. Without this, the top of the
// season board would be whoever claimed the most daily bonuses.
func TestSeasonNet_HandoutsAndExchangesAreExcluded(t *testing.T) {
	st, path := newTempStore(t)
	seedRate(t, st, "guild1", fixedNow, 100)
	// u1 starts with a 純利 already on the board, not at 0: "excluded" means
	// LEAVES IT ALONE, and a handout that RESET the figure would pass an
	// all-zero fixture while wiping out a month of play.
	seedAccounts(t, st, "guild1", map[string]UserAccount{"u1": {Coins: 1000, Chips: 1000, SeasonNet: -777}})
	st.rng = forbiddenRand{t: t, reason: "today's rate is already seeded, so no draw may happen"}

	// The welcome bonus, on an account that has never played.
	if _, err := st.ViewAccount("guild1", "newbie", fixedNow); err != nil {
		t.Fatalf("ViewAccount returned error: %v", err)
	}
	assertSeasonNet(t, path, "guild1", "newbie", 0)

	if _, err := st.at(fixedNow).ClaimDaily("guild1", "u1"); err != nil {
		t.Fatalf("ClaimDaily returned error: %v", err)
	}
	if _, err := st.ExchangeCoinToChip("guild1", "u1", 100, fixedNow); err != nil {
		t.Fatalf("ExchangeCoinToChip returned error: %v", err)
	}
	if _, err := st.ExchangeChipToCoin("guild1", "u1", 1000, fixedNow); err != nil {
		t.Fatalf("ExchangeChipToCoin returned error: %v", err)
	}
	if err := st.Mint("guild1", "u1", 500); err != nil {
		t.Fatalf("Mint returned error: %v", err)
	}

	account := readAccount(t, path, "guild1", "u1")
	if account.SeasonNet != -777 {
		t.Fatalf("SeasonNet = %d, want -777 unchanged — the daily bonus, both exchanges and mint are excluded from 純利 (§3)", account.SeasonNet)
	}
	if account.Chips == 1000 && account.Coins == 1000 {
		t.Fatal("no balance moved at all — the test proved nothing about exclusion")
	}
}

// A stake that is handed back rather than played out is not a result: a
// declined duel and a startup refund both leave the season board alone.
func TestSeasonNet_RefundedStakesAreNotResults(t *testing.T) {
	st, path := newTempStore(t)
	seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000})

	openDuelChallenge(t, st, 200)
	if err := st.DeclineDuel(escrowGuild, escrowUser); err != nil {
		t.Fatalf("DeclineDuel returned error: %v", err)
	}
	assertSeasonNet(t, path, escrowGuild, escrowUser, 0)

	if err := st.OpenGame(escrowGuild, escrowUser, string(GameBlackjack), 300, fixedNow); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}
	if _, err := st.RefundStaleEscrows(fixedNow); err != nil {
		t.Fatalf("RefundStaleEscrows returned error: %v", err)
	}
	assertHoldings(t, path, escrowGuild, escrowUser, 1000, 0, "")
	assertSeasonNet(t, path, escrowGuild, escrowUser, 0)
}

// TestSeasonNet_HandEditedExtremesAreNormalisedAtTheReadPoint is the file
// half. A season_net near math.MaxInt64 wraps the very next addition, and a
// wrapped 純利 puts the worst player at the top of the board. The clamp sits
// at ensureAccountLocked, so merely OPENING a game repairs it — before any
// arithmetic touches it.
func TestSeasonNet_HandEditedExtremesAreNormalisedAtTheReadPoint(t *testing.T) {
	tests := []struct {
		name                         string
		seeded                       int64
		wantAfterRead, wantAfterLoss int64
	}{
		// The loss comes off the clamped value, not the file's.
		{"上限を超えた値", math.MaxInt64, MaxChips, MaxChips - 100},
		// Already saturated at the floor: a further loss cannot wrap it positive.
		{"下限を下回った値", math.MinInt64, -MaxChips, -MaxChips},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, path := newTempStore(t)
			seedAccount(t, st, escrowGuild, escrowUser, UserAccount{Chips: 1000, SeasonNet: tc.seeded})

			if err := st.OpenGame(escrowGuild, escrowUser, string(GameHighLow), 100, fixedNow); err != nil {
				t.Fatalf("OpenGame returned error: %v", err)
			}
			assertSeasonNet(t, path, escrowGuild, escrowUser, tc.wantAfterRead)

			if _, err := st.SettleGame(escrowGuild, escrowUser, 0); err != nil {
				t.Fatalf("SettleGame returned error: %v", err)
			}
			assertSeasonNet(t, path, escrowGuild, escrowUser, tc.wantAfterLoss)
		})
	}
}

// --- 月次シーズンの切り替え (設計書 C-3b §4) ---------------------------------

const seasonGuild = "season-guild"

// The two instants every season test crosses: the last day of July and the
// first half-hour of August. The pair is chosen on purpose — the switch is a
// MIDNIGHT boundary, not the 09:00 one the lottery and the announcement use,
// so a rollover wired to 09:00 instead would still be sitting in July at
// 00:30 on the 1st.
var (
	seasonJuly   = time.Date(2026, 7, 31, 12, 0, 0, 0, jst)
	seasonAugust = time.Date(2026, 8, 1, 0, 30, 0, 0, jst)
)

// openSeason runs the rollover once so the guild's season is OPEN at
// seasonJuly, and asserts that this first pass paid nobody: an unstarted
// guild has no previous month to rank.
func openSeason(t *testing.T, st *Store, path string) {
	t.Helper()
	if _, err := st.EnsureTodayRate(seasonGuild, seasonJuly); err != nil {
		t.Fatalf("EnsureTodayRate (opening the season) returned error: %v", err)
	}
	economy := readGuild(t, path, seasonGuild)
	if economy.SeasonMonth != "2026-07" {
		t.Fatalf("SeasonMonth = %q after the first pass, want %q", economy.SeasonMonth, "2026-07")
	}
	if economy.LastSeason != nil {
		t.Fatalf("LastSeason = %+v after merely OPENING the first season, want nil", economy.LastSeason)
	}
}

// assertSeason checks the whole closed-season record at once: which month
// closed, how many players it counted, and the exact podium including the
// bonus each row was actually credited.
func assertSeason(t *testing.T, path, wantMonth string, wantPlayers int, wantRanks []SeasonRank) {
	t.Helper()
	last := readGuild(t, path, seasonGuild).LastSeason
	if last == nil {
		t.Fatalf("LastSeason = nil, want the closed season %q", wantMonth)
	}
	if last.Month != wantMonth {
		t.Fatalf("LastSeason.Month = %q, want %q", last.Month, wantMonth)
	}
	if last.Players != wantPlayers {
		t.Fatalf("LastSeason.Players = %d, want %d: %+v", last.Players, wantPlayers, last.Ranks)
	}
	if len(last.Ranks) != len(wantRanks) {
		t.Fatalf("len(LastSeason.Ranks) = %d, want %d: %+v", len(last.Ranks), len(wantRanks), last.Ranks)
	}
	for i := range wantRanks {
		if last.Ranks[i] != wantRanks[i] {
			t.Fatalf("LastSeason.Ranks[%d] = %+v, want %+v (full: %+v)", i, last.Ranks[i], wantRanks[i], last.Ranks)
		}
	}
}

// assertChips reads the balances back through a fresh Store and checks every
// one named.
func assertChips(t *testing.T, path string, want map[string]int64) {
	t.Helper()
	for userID, wantChips := range want {
		if got := readAccount(t, path, seasonGuild, userID).Chips; got != wantChips {
			t.Fatalf("%s Chips = %d, want %d", userID, got, wantChips)
		}
	}
}

// The whole switch in one pass: the podium is paid in rank order, the tie is
// broken on UserID, the 純利 0 account is neither ranked nor counted, and
// EVERY account — ranked, unranked, idle — comes out of the boundary at 0.
//
// The last of those is the one that is easy to get wrong by only zeroing the
// rows the ranking returned: last month's losses would then be carried into
// this month's board, and a player who ended July at -300 would need to win
// 300 chips in August just to reach even.
func TestStore_SeasonRollover_ClosesMonthPaysPodiumAndZeroesEveryAccount(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"winner": {Chips: 1_000, SeasonNet: 1_000},
		// "second" and "third" are tied on 純利; the podium order between them
		// is decided by UserID ascending, and 5,000 vs 2,500 chips rides on it.
		"second": {Chips: 1_000, SeasonNet: 500},
		"third":  {Chips: 1_000, SeasonNet: 500},
		"fourth": {Chips: 1_000, SeasonNet: 100},
		"loser":  {Chips: 1_000, SeasonNet: -300},
		"idle":   {Chips: 1_000, SeasonNet: 0},
	})
	openSeason(t, st, path)

	if _, err := st.EnsureTodayRate(seasonGuild, seasonAugust); err != nil {
		t.Fatalf("EnsureTodayRate (crossing into August) returned error: %v", err)
	}

	assertSeason(t, path, "2026-07", 5, []SeasonRank{
		{UserID: "winner", Net: 1_000, Bonus: 10_000},
		{UserID: "second", Net: 500, Bonus: 5_000},
		{UserID: "third", Net: 500, Bonus: 2_500},
	})
	assertChips(t, path, map[string]int64{
		"winner": 11_000, "second": 6_000, "third": 3_500,
		// Off the podium: 4th place and the loser are ranked but unpaid, and
		// the idle account is not even counted.
		"fourth": 1_000, "loser": 1_000, "idle": 1_000,
	})
	economy := readGuild(t, path, seasonGuild)
	if economy.SeasonMonth != "2026-08" {
		t.Fatalf("SeasonMonth = %q after the switch, want %q", economy.SeasonMonth, "2026-08")
	}
	for userID, account := range economy.Users {
		if account.SeasonNet != 0 {
			t.Fatalf("%s SeasonNet = %d after the switch, want 0 for every account", userID, account.SeasonNet)
		}
	}
}

// A month closes ONCE. Every command in the guild reaches the rollover, so
// the second, third and hundredth pass of the same month must find nothing
// to do — a switch keyed on "is LastSeason older than today" rather than on
// SeasonMonth would mint a fresh podium on every single command.
func TestStore_SeasonRollover_SecondPassInTheSameMonth_DoesNothing(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"winner": {Chips: 1_000, SeasonNet: 1_000},
	})
	openSeason(t, st, path)
	if _, err := st.EnsureTodayRate(seasonGuild, seasonAugust); err != nil {
		t.Fatalf("EnsureTodayRate (crossing into August) returned error: %v", err)
	}

	// Somebody plays in the new month, then the day turns over again — still
	// August, so still the same season.
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"winner": {Chips: 11_000, SeasonNet: 777},
	})
	if _, err := st.EnsureTodayRate(seasonGuild, seasonAugust.AddDate(0, 0, 1)); err != nil {
		t.Fatalf("EnsureTodayRate (next day, same month) returned error: %v", err)
	}

	assertSeason(t, path, "2026-07", 1, []SeasonRank{{UserID: "winner", Net: 1_000, Bonus: 10_000}})
	assertChips(t, path, map[string]int64{"winner": 11_000})
	assertSeasonNet(t, path, seasonGuild, "winner", 777)
}

// The clock going BACKWARDS must not re-close a month that has already paid
// out — the discipline C3-19 settled for the announcement boundary and
// drawLotteryLocked's DrawDate. An NTP correction or a hand-set host clock
// makes August -> July -> August, and a != comparison mints the podium
// twice: on the way back it closes an AUGUST whose 純利 belongs to whoever
// happened to play in between.
func TestStore_SeasonRollover_ClockGoingBackwards_DoesNotCloseAgain(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"winner": {Chips: 1_000, SeasonNet: 1_000},
	})
	openSeason(t, st, path)
	if _, err := st.EnsureTodayRate(seasonGuild, seasonAugust); err != nil {
		t.Fatalf("EnsureTodayRate (crossing into August) returned error: %v", err)
	}
	// A non-zero 純利 in the new month is what a wrongly-closing rewind would
	// pay a second prize for. Starting from 0 here would let the bug through:
	// the ranking would be empty and only SeasonMonth would move.
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"winner": {Chips: 11_000, SeasonNet: 999},
	})

	if _, err := st.EnsureTodayRate(seasonGuild, seasonJuly); err != nil {
		t.Fatalf("EnsureTodayRate (clock rewound to July) returned error: %v", err)
	}

	economy := readGuild(t, path, seasonGuild)
	if economy.SeasonMonth != "2026-08" {
		t.Fatalf("SeasonMonth = %q after the rewind, want it to stay at %q", economy.SeasonMonth, "2026-08")
	}
	assertSeason(t, path, "2026-07", 1, []SeasonRank{{UserID: "winner", Net: 1_000, Bonus: 10_000}})
	assertChips(t, path, map[string]int64{"winner": 11_000})
	assertSeasonNet(t, path, seasonGuild, "winner", 999)
}

// A bot that was down through the whole of August comes back in September:
// the season that closes is the one that was RUNNING (July), not the month
// in between, and it closes exactly once.
func TestStore_SeasonRollover_SkippedMonth_ClosesTheRunningSeasonOnce(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"winner": {Chips: 1_000, SeasonNet: 1_000},
	})
	openSeason(t, st, path)

	september := time.Date(2026, 9, 3, 12, 0, 0, 0, jst)
	if _, err := st.EnsureTodayRate(seasonGuild, september); err != nil {
		t.Fatalf("EnsureTodayRate (jumping to September) returned error: %v", err)
	}

	assertSeason(t, path, "2026-07", 1, []SeasonRank{{UserID: "winner", Net: 1_000, Bonus: 10_000}})
	assertChips(t, path, map[string]int64{"winner": 11_000})
	if got := readGuild(t, path, seasonGuild).SeasonMonth; got != "2026-09" {
		t.Fatalf("SeasonMonth = %q, want %q", got, "2026-09")
	}
}

// An unstarted guild (SeasonMonth "" — every guild in a pre-C-3b file) only
// OPENS the month. Paying here would hand prizes for 純利 accumulated before
// any season existed, and the running total is NOT zeroed either: the month
// it belongs to is the one just opened.
func TestStore_SeasonRollover_UnstartedGuild_OpensWithoutPayingOrZeroing(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"veteran": {Chips: 1_000, SeasonNet: 5_000},
	})

	openSeason(t, st, path) // asserts LastSeason stays nil

	assertChips(t, path, map[string]int64{"veteran": 1_000})
	assertSeasonNet(t, path, seasonGuild, "veteran", 5_000)
}

// The prize obeys MaxChips like every other payout, and SeasonRank.Bonus
// records what LANDED rather than what the table offers (設計書 C-3b §2) —
// otherwise the season table would announce chips the winner's balance never
// received. Escrow counts against the headroom because it is chips the
// account still owns.
func TestStore_SeasonRollover_BonusCappedAtMaxChips_RecordsWhatLanded(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"a-partly-full": {Chips: MaxChips - 3_000, SeasonNet: 1_000}, // 10,000 offered, 3,000 fits
		"b-at-the-cap":  {Chips: MaxChips, SeasonNet: 500},           // 5,000 offered, nothing fits
		"c-escrowed":    {Chips: MaxChips - 5_000, Escrow: 4_000, SeasonNet: 100},
	})
	openSeason(t, st, path)

	if _, err := st.EnsureTodayRate(seasonGuild, seasonAugust); err != nil {
		t.Fatalf("EnsureTodayRate (crossing into August) returned error: %v", err)
	}

	assertSeason(t, path, "2026-07", 3, []SeasonRank{
		{UserID: "a-partly-full", Net: 1_000, Bonus: 3_000},
		{UserID: "b-at-the-cap", Net: 500, Bonus: 0},
		// 2,500 offered; headroom is MaxChips - 4,000 escrowed - (MaxChips
		// - 5,000) = 1,000.
		{UserID: "c-escrowed", Net: 100, Bonus: 1_000},
	})
	assertChips(t, path, map[string]int64{
		"a-partly-full": MaxChips,
		"b-at-the-cap":  MaxChips,
		"c-escrowed":    MaxChips - 4_000,
	})
}

// A hand-edited {"users":{"someone":null}} reaches the rollover's loops the
// same way it reaches topAssetsLocked's: ensureAccountLocked repairs only the
// ONE user a write path touches. A nil dereference here panics inside a
// discordgo handler goroutine (no recover), killing the bot and leaving the
// bad file on disk — a permanent crash loop on the 1st of the month.
func TestStore_SeasonRollover_NilAccountDoesNotPanic(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"real": {Chips: 1_000, SeasonNet: 400},
	})
	openSeason(t, st, path)
	if err := st.Update(func(d *Data) error {
		ensureGuildLocked(d, seasonGuild).Users["ghost"] = nil
		return nil
	}); err != nil {
		t.Fatalf("seeding the null account: %v", err)
	}

	if _, err := st.EnsureTodayRate(seasonGuild, seasonAugust); err != nil {
		t.Fatalf("EnsureTodayRate (crossing into August) returned error: %v", err)
	}

	assertSeason(t, path, "2026-07", 1, []SeasonRank{{UserID: "real", Net: 400, Bonus: 10_000}})
	assertChips(t, path, map[string]int64{"real": 11_000})
}

// A 純利 outside the cap can only come from a hand edit or a partial write,
// and it is copied STRAIGHT into the persisted SeasonResult — so without a
// normalisation pass before the ranking, math.MaxInt64 would be written back
// into the file as that month's record.
func TestStore_SeasonRollover_HandEditedNetIsClampedBeforeItIsRecorded(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"inflated": {Chips: 1_000, SeasonNet: math.MaxInt64},
		"deflated": {Chips: 1_000, SeasonNet: math.MinInt64},
	})
	openSeason(t, st, path)

	if _, err := st.EnsureTodayRate(seasonGuild, seasonAugust); err != nil {
		t.Fatalf("EnsureTodayRate (crossing into August) returned error: %v", err)
	}

	assertSeason(t, path, "2026-07", 2, []SeasonRank{
		{UserID: "inflated", Net: MaxChips, Bonus: 10_000},
		{UserID: "deflated", Net: -MaxChips, Bonus: 5_000},
	})
}

// A data/casino.json written before C-3b has no "season_month" key at all.
// It must read back as an UNSTARTED guild — the month opens, nothing is paid,
// no balance moves — rather than as a season whose month is "" closing into
// one that is not.
func TestStore_SeasonRollover_PreC3bFile_OpensCurrentMonthAndPaysNothing(t *testing.T) {
	st, path := newTempStore(t)
	st.rng = forbiddenRand{t: t, reason: "today's rate is already in the file, so no draw may happen"}
	writeHandEditedFile(t, path, fmt.Sprintf(
		`{"guild1":{"rates":[{"date":%q,"rate":100,"trend":"flat","event":"none"}],`+
			`"users":{"user-a":{"chips":100,"coins":1}}}}`,
		jstDate(fixedNow)))

	if _, err := st.EnsureTodayRate("guild1", fixedNow); err != nil {
		t.Fatalf("EnsureTodayRate returned error: %v", err)
	}

	economy := readGuild(t, path, "guild1")
	if economy.SeasonMonth != jstMonth(fixedNow) {
		t.Fatalf("SeasonMonth = %q, want the current month %q", economy.SeasonMonth, jstMonth(fixedNow))
	}
	if economy.LastSeason != nil {
		t.Fatalf("LastSeason = %+v on a file that never had a season, want nil", economy.LastSeason)
	}
	account := readAccount(t, path, "guild1", "user-a")
	if account.Chips != 100 || account.Coins != 1 || account.SeasonNet != 0 {
		t.Fatalf("user-a = %+v, want the file's balances untouched and SeasonNet 0", account)
	}
	// LastSeason is omitempty: a guild that has closed nothing must not start
	// writing an empty result into every file.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the data file: %v", err)
	}
	if strings.Contains(string(raw), "last_season") {
		t.Fatalf("data file carries a last_season key with nothing closed: %s", raw)
	}
}

// The boundary is MIDNIGHT JST and it is computed in JST regardless of the
// host's zone. A rollover that read the host clock's own month would switch a
// day early or a day late on any machine west of Japan, which on the 1st is
// the difference between paying the podium and not.
func TestStore_SeasonRollover_MonthBoundaryIsJSTMidnight(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"winner": {Chips: 1_000, SeasonNet: 1_000},
	})
	openSeason(t, st, path)

	// 2026-08-01 00:00:00 JST is 2026-07-31 15:00 UTC: still July everywhere
	// west of Japan, but a new season here.
	midnight := time.Date(2026, 8, 1, 0, 0, 0, 0, jst).UTC()
	if got := jstMonth(midnight); got != "2026-08" {
		t.Fatalf("jstMonth(%s) = %q, want %q", midnight, got, "2026-08")
	}
	if _, err := st.EnsureTodayRate(seasonGuild, midnight); err != nil {
		t.Fatalf("EnsureTodayRate at the boundary returned error: %v", err)
	}

	assertSeason(t, path, "2026-07", 1, []SeasonRank{{UserID: "winner", Net: 1_000, Bonus: 10_000}})
	if got := readGuild(t, path, seasonGuild).SeasonMonth; got != "2026-08" {
		t.Fatalf("SeasonMonth = %q, want %q", got, "2026-08")
	}
}

// The season switch runs BEFORE the lottery draw inside the same rollover.
// Both live in ensureTodayRateIndexLocked, the switch zeroes every SeasonNet
// and the draw CREDITS one — so the other order silently erases the 純利 of
// whichever draw shares a call with a month boundary, i.e. one draw every
// month, forever. Running first books that payout into the month that has
// just opened, which is a reading the player can see on the board.
func TestStore_SeasonRollover_RunsBeforeTheLotteryDraw(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"buyer": {Chips: 1_000, SeasonNet: -100},
	})
	openSeason(t, st, path) // also stamps the lottery's DrawDate at 2026-07-31

	// One ticket holder waiting for the next draw.
	if err := st.Update(func(d *Data) error {
		economy := ensureGuildLocked(d, seasonGuild)
		economy.Lottery.Tickets = map[string]int{"buyer": 1}
		economy.Lottery.Sales = 1_000
		return nil
	}); err != nil {
		t.Fatalf("seeding the lottery pot: %v", err)
	}

	// 2026-08-01 after 09:00 JST: the season closes and the draw for that day
	// runs, in one transaction.
	if _, err := st.EnsureTodayRate(seasonGuild, time.Date(2026, 8, 1, 12, 0, 0, 0, jst)); err != nil {
		t.Fatalf("EnsureTodayRate (crossing into August) returned error: %v", err)
	}

	assertSeason(t, path, "2026-07", 1, []SeasonRank{{UserID: "buyer", Net: -100, Bonus: 10_000}})
	prize, _ := LotteryPrize(1_000, 0)
	assertSeasonNet(t, path, seasonGuild, "buyer", prize)
	assertChips(t, path, map[string]int64{"buyer": 1_000 + 10_000 + prize})
}

// seasonJulyLate is the last minute of July JST — the instant a game is
// STARTED at in the boundary regressions below, so that only the settlement
// falls in August.
var seasonJulyLate = time.Date(2026, 7, 31, 23, 59, 0, 0, jst)

// A settlement that lands on the far side of a month boundary belongs to the
// month it landed in, not to the one that has just ended.
//
// The daily rollover is a READ path: SettleGame takes no instant from the
// caller and never reaches it. Without the switch inside the settlement's own
// transaction, a hand that was dealt on 31 July and paid at 00:01 on 1 August
// is added to July's 純利 — and the very next display then closes July with
// August's result on the podium and opens August at zero, i.e. the result is
// counted for the wrong month AND lost from the right one.
func TestStore_SeasonRollover_SettleGameAcrossTheBoundaryBooksIntoTheNewMonth(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"player": {Chips: 1_000, SeasonNet: 500},
	})
	openSeason(t, st, path)

	// Dealt in July...
	if err := st.OpenGame(seasonGuild, "player", string(GameHighLow), 100, seasonJulyLate); err != nil {
		t.Fatalf("OpenGame returned error: %v", err)
	}
	// ...paid in August, with no display in between.
	if _, err := st.at(seasonAugust).SettleGame(seasonGuild, "player", 200); err != nil {
		t.Fatalf("SettleGame returned error: %v", err)
	}

	// July closed on the 純利 it had BEFORE the hand was paid.
	assertSeason(t, path, "2026-07", 1, []SeasonRank{{UserID: "player", Net: 500, Bonus: 10_000}})
	// ...and the hand's +100 opens August rather than vanishing with the zeroing.
	assertSeasonNet(t, path, seasonGuild, "player", 100)
	assertChips(t, path, map[string]int64{"player": 1_000 - 100 + 200 + 10_000})
	if month := readGuild(t, path, seasonGuild).SeasonMonth; month != "2026-08" {
		t.Fatalf("SeasonMonth = %q after the settlement, want %q", month, "2026-08")
	}
}

// The slot books its 純利 in the same transaction as the spin and, like
// SettleGame, never passes the display's rollover.
func TestStore_SeasonRollover_SpinAcrossTheBoundaryBooksIntoTheNewMonth(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"player": {Chips: 1_000, SeasonNet: 500},
	})
	openSeason(t, st, path)

	result, err := st.at(seasonAugust).Spin(seasonGuild, "player", 100)
	if err != nil {
		t.Fatalf("Spin returned error: %v", err)
	}

	assertSeason(t, path, "2026-07", 1, []SeasonRank{{UserID: "player", Net: 500, Bonus: 10_000}})
	// The expectation comes from the spin's own report, not from a copy of
	// the paytable: what is under test is WHICH MONTH the net lands in.
	assertSeasonNet(t, path, seasonGuild, "player", result.Payout-100)
	if month := readGuild(t, path, seasonGuild).SeasonMonth; month != "2026-08" {
		t.Fatalf("SeasonMonth = %q after the spin, want %q", month, "2026-08")
	}
}

// The duel settles BOTH sides in one transaction, so a boundary crossed
// there would misfile two players' results at once.
func TestStore_SeasonRollover_AcceptDuelAcrossTheBoundaryBooksIntoTheNewMonth(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"player": {Chips: 1_000, SeasonNet: 500},
		"rival":  {Chips: 1_000, SeasonNet: 300},
	})
	openSeason(t, st, path)

	if err := st.OpenGame(seasonGuild, "player", string(GameDuel), 200, seasonJulyLate); err != nil {
		t.Fatalf("OpenGame(duel) returned error: %v", err)
	}
	if _, err := st.at(seasonAugust).AcceptDuel(seasonGuild, "player", "rival", 200, true); err != nil {
		t.Fatalf("AcceptDuel returned error: %v", err)
	}

	assertSeason(t, path, "2026-07", 2, []SeasonRank{
		{UserID: "player", Net: 500, Bonus: 10_000},
		{UserID: "rival", Net: 300, Bonus: 5_000},
	})
	assertSeasonNet(t, path, seasonGuild, "player", 200)
	assertSeasonNet(t, path, seasonGuild, "rival", -200)
	assertChips(t, path, map[string]int64{
		"player": 1_000 - 200 + 400 + 10_000,
		"rival":  1_000 - 200 + 5_000,
	})
}

// --- 未掲示シーズンの待ち行列 (C3B-08) ---------------------------------------

// closeMonthsWithAPodium runs one rollover per month, each closing the
// previous one with a real podium — the setup every queue assertion below
// needs, since a month nobody placed in is deliberately never queued.
func closeMonthsWithAPodium(economy *GuildEconomy, months ...string) {
	for _, month := range months {
		for _, account := range economy.Users {
			account.SeasonNet = 100
		}
		rolloverSeasonLocked(economy, month)
	}
}

// The queue is bounded: a podium months late is the entry least worth
// posting, so the oldest is the one that goes when a fourth result arrives.
func TestRolloverSeason_TheQueueKeepsTheThreeNewestResults(t *testing.T) {
	economy := &GuildEconomy{SeasonMonth: "2026-01", Users: map[string]*UserAccount{"leader": {Chips: 1_000}}}

	closeMonthsWithAPodium(economy, "2026-02", "2026-03", "2026-04", "2026-05")

	want := []string{"2026-02", "2026-03", "2026-04"}
	if len(economy.UnannouncedSeasons) != len(want) {
		t.Fatalf("UnannouncedSeasons = %+v, want %d entries", economy.UnannouncedSeasons, len(want))
	}
	for i, month := range want {
		if got := economy.UnannouncedSeasons[i].Month; got != month {
			t.Errorf("UnannouncedSeasons[%d].Month = %q, want %q — the queue drops from the front", i, got, month)
		}
	}
}

// A month nobody placed in has no podium to ping, so it must not spend one
// of the three slots — doing so would push a real podium off the front.
func TestRolloverSeason_AQuietMonthDoesNotEnterTheQueue(t *testing.T) {
	economy := &GuildEconomy{SeasonMonth: "2026-01", Users: map[string]*UserAccount{"idle": {Chips: 1_000}}}
	closeMonthsWithAPodium(economy, "2026-02")

	rolloverSeasonLocked(economy, "2026-03") // nobody's SeasonNet moved in February

	if len(economy.UnannouncedSeasons) != 1 || economy.UnannouncedSeasons[0].Month != "2026-01" {
		t.Fatalf("UnannouncedSeasons = %+v, want only the January result", economy.UnannouncedSeasons)
	}
	if economy.LastSeason == nil || economy.LastSeason.Month != "2026-02" {
		t.Fatalf("LastSeason = %+v, want the quiet February result /season still shows", economy.LastSeason)
	}
}

// The queue entry and LastSeason are two independent records of one month:
// retiring the queue entry must not disturb what /season reads, and a later
// edit of one must not show up in the other.
func TestRolloverSeason_TheQueueEntryIsACopyOfLastSeason(t *testing.T) {
	economy := &GuildEconomy{SeasonMonth: "2026-01", Users: map[string]*UserAccount{"leader": {Chips: 1_000}}}
	closeMonthsWithAPodium(economy, "2026-02")

	if len(economy.UnannouncedSeasons) != 1 || economy.LastSeason == nil {
		t.Fatalf("setup: queue = %+v / LastSeason = %+v", economy.UnannouncedSeasons, economy.LastSeason)
	}
	economy.UnannouncedSeasons[0].Ranks[0].Bonus = 1
	if got := economy.LastSeason.Ranks[0].Bonus; got != SeasonBonus(1) {
		t.Fatalf("LastSeason's podium followed the queue entry: Bonus = %d, want %d", got, SeasonBonus(1))
	}
}

// A pre-C3B-08 file holds its one pending result in LastSeason and has no
// queue at all. The upgrade must lift it into the queue BEFORE the next
// rollover overwrites it — and must not queue it twice.
func TestMigrateUnannouncedSeasons_LiftsAPreQueueFilesPendingResult(t *testing.T) {
	pending := func() *GuildEconomy {
		return &GuildEconomy{
			SeasonMonth: "2026-07",
			LastSeason: &SeasonResult{Month: "2026-06", Players: 1,
				Ranks: []SeasonRank{{UserID: "leader", Net: 1_200, Bonus: SeasonBonus(1)}}},
			Users: map[string]*UserAccount{},
		}
	}

	economy := pending()
	rolloverSeasonLocked(economy, "2026-08") // the very rollover that used to eat it
	if len(economy.UnannouncedSeasons) != 1 || economy.UnannouncedSeasons[0].Month != "2026-06" {
		t.Fatalf("UnannouncedSeasons = %+v, want the migrated June result", economy.UnannouncedSeasons)
	}
	// Idempotent: the entry it migrated is why the next pass does nothing.
	rolloverSeasonLocked(economy, "2026-09")
	if len(economy.UnannouncedSeasons) != 1 {
		t.Fatalf("UnannouncedSeasons = %+v, want June alone — it was migrated twice", economy.UnannouncedSeasons)
	}

	// Already celebrated: LastSeason survives MarkSeasonAnnounced's pruning,
	// and re-deriving from it would ping the podium a second time.
	announced := pending()
	announced.LastSeasonAnnounced = "2026-06"
	rolloverSeasonLocked(announced, "2026-08")
	if len(announced.UnannouncedSeasons) != 0 {
		t.Fatalf("UnannouncedSeasons = %+v, want empty — 2026-06 was already posted", announced.UnannouncedSeasons)
	}
}

// --- /season の読み取り (設計書 C-3b §5) -----------------------------------

// The table is truncated to SeasonTopLimit but the CALLER's place is not:
// somebody in 12th must be told "12位", not quietly dropped off the board
// they are trying to climb. Players counts everyone with a result, table or
// no table.
func TestStore_SeasonStatus_TruncatesTheTableButNotTheCallersPlace(t *testing.T) {
	st, _ := newTempStore(t)
	users := map[string]UserAccount{}
	// 12 players, each 100 chips of 純利 apart: p01 leads, p12 is last.
	for place := 1; place <= 12; place++ {
		users[fmt.Sprintf("p%02d", place)] = UserAccount{Chips: 1_000, SeasonNet: int64(1_300 - 100*place)}
	}
	users["idle"] = UserAccount{Chips: 1_000, SeasonNet: 0}
	seedAccounts(t, st, seasonGuild, users)

	view, err := st.SeasonStatus(seasonGuild, "p12", seasonJuly)
	if err != nil {
		t.Fatalf("SeasonStatus returned error: %v", err)
	}

	if view.Month != "2026-07" {
		t.Errorf("Month = %q, want %q — the read must open the season", view.Month, "2026-07")
	}
	if len(view.Ranks) != SeasonTopLimit {
		t.Fatalf("len(Ranks) = %d, want %d", len(view.Ranks), SeasonTopLimit)
	}
	if view.Ranks[0].UserID != "p01" || view.Ranks[0].Net != 1_200 {
		t.Errorf("Ranks[0] = %+v, want p01 on +1200", view.Ranks[0])
	}
	if view.Ranks[SeasonTopLimit-1].UserID != "p10" {
		t.Errorf("Ranks[9] = %+v, want p10 — the table ends at the 10th place", view.Ranks[SeasonTopLimit-1])
	}
	if view.SelfRank != 12 {
		t.Errorf("SelfRank = %d, want 12 — the caller's place is their place in the WHOLE ranking", view.SelfRank)
	}
	if view.Self.Net != 100 {
		t.Errorf("Self.Net = %d, want 100", view.Self.Net)
	}
	if view.Players != 12 {
		t.Errorf("Players = %d, want 12 — the idle account has no result to count", view.Players)
	}
	if view.DaysLeft != 1 {
		t.Errorf("DaysLeft = %d, want 1 — 7月31日 is the last day of the season", view.DaysLeft)
	}
}

// 純利 exactly 0 is unranked, not last: SelfRank 0 is what /season renders as
// 圏外. Reading the season must not invent a place for someone who has not
// played, and must not open a season it then reports as somebody's.
func TestStore_SeasonStatus_UnplayedCallerIsUnranked(t *testing.T) {
	st, _ := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"player": {Chips: 1_000, SeasonNet: 500},
	})

	view, err := st.SeasonStatus(seasonGuild, "newcomer", seasonJuly)
	if err != nil {
		t.Fatalf("SeasonStatus returned error: %v", err)
	}

	if view.SelfRank != 0 {
		t.Errorf("SelfRank = %d, want 0 (圏外) for an account with no result", view.SelfRank)
	}
	if view.Self.Net != 0 {
		t.Errorf("Self.Net = %d, want 0", view.Self.Net)
	}
	if view.Players != 1 {
		t.Errorf("Players = %d, want 1 — only the one account with a result counts", view.Players)
	}
	if view.Last != nil {
		t.Errorf("Last = %+v, want nil — no season has closed yet", view.Last)
	}
}

// The read goes through the daily rollover, so /season on the 1st shows the
// NEW month and reports the old one as 前シーズン — rather than presenting
// July's table as if it were still being played for, until some other
// command happens to close it.
func TestStore_SeasonStatus_ClosesAnElapsedSeasonBeforeReporting(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"winner": {Chips: 1_000, SeasonNet: 1_000},
		"loser":  {Chips: 1_000, SeasonNet: -300},
	})
	openSeason(t, st, path)

	view, err := st.SeasonStatus(seasonGuild, "winner", seasonAugust)
	if err != nil {
		t.Fatalf("SeasonStatus returned error: %v", err)
	}

	if view.Month != "2026-08" {
		t.Errorf("Month = %q, want %q", view.Month, "2026-08")
	}
	if len(view.Ranks) != 0 || view.Players != 0 || view.SelfRank != 0 {
		t.Errorf("the new season is not empty: %+v", view)
	}
	if view.Last == nil {
		t.Fatal("Last = nil, want July's closed season")
	}
	if view.Last.Month != "2026-07" || view.Last.Players != 2 {
		t.Errorf("Last = %+v, want 2026-07 with 2 players", view.Last)
	}
	want := []SeasonRank{{UserID: "winner", Net: 1_000, Bonus: 10_000}, {UserID: "loser", Net: -300, Bonus: 5_000}}
	if len(view.Last.Ranks) != len(want) {
		t.Fatalf("Last.Ranks = %+v, want %+v", view.Last.Ranks, want)
	}
	for i := range want {
		if view.Last.Ranks[i] != want[i] {
			t.Errorf("Last.Ranks[%d] = %+v, want %+v", i, view.Last.Ranks[i], want[i])
		}
	}
	// The prize actually landed: the view is a report of a committed switch,
	// not a preview of one.
	assertChips(t, path, map[string]int64{"winner": 11_000, "loser": 6_000})
}

// The view is handed to a display, and a display must not be able to edit the
// economy. Last is a copy down to its slice — sharing the stored pointer
// would let a caller's slice write reach the next Update's snapshot.
func TestStore_SeasonStatus_LastSeasonIsACopy(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{"winner": {Chips: 1_000, SeasonNet: 1_000}})
	openSeason(t, st, path)

	view, err := st.SeasonStatus(seasonGuild, "winner", seasonAugust)
	if err != nil {
		t.Fatalf("SeasonStatus returned error: %v", err)
	}
	if view.Last == nil || len(view.Last.Ranks) != 1 {
		t.Fatalf("Last = %+v, want July with one row", view.Last)
	}
	view.Last.Month = "hacked"
	view.Last.Ranks[0] = SeasonRank{UserID: "thief", Net: 99, Bonus: 99}

	stored := readGuild(t, path, seasonGuild).LastSeason
	if stored.Month != "2026-07" {
		t.Errorf("stored Month = %q after the caller edited its copy, want 2026-07", stored.Month)
	}
	if stored.Ranks[0].UserID != "winner" {
		t.Errorf("stored podium = %+v after the caller edited its copy, want winner's row", stored.Ranks[0])
	}
}

// A hand-edited 純利 outside the cap must not be able to take first place:
// the read normalises every account it ranks, the same discipline
// topAssetsLocked applies to balances.
func TestStore_SeasonStatus_NormalisesHandEditedNets(t *testing.T) {
	st, _ := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"edited": {Chips: 1_000, SeasonNet: math.MaxInt64},
		"honest": {Chips: 1_000, SeasonNet: 500},
	})

	view, err := st.SeasonStatus(seasonGuild, "honest", seasonJuly)
	if err != nil {
		t.Fatalf("SeasonStatus returned error: %v", err)
	}

	if view.Ranks[0].UserID != "edited" || view.Ranks[0].Net != MaxChips {
		t.Errorf("Ranks[0] = %+v, want the edited account clamped to %d", view.Ranks[0], MaxChips)
	}
}

// Two reads that straddle midnight can arrive in the reverse order of the
// instants they carry (each reads its clock before taking the lock), and the
// month is persisted while the days left are computed. The late arrival is
// shown the season the earlier one opened, so it must be shown THAT month's
// remaining days — not 「8月・残り1日」, a month that has just begun reported
// as ending tonight.
func TestStore_SeasonStatus_LateArrivalCountsTheMonthItIsShown(t *testing.T) {
	st, _ := newTempStore(t)
	seedAccounts(t, st, seasonGuild, map[string]UserAccount{
		"player": {Chips: 1_000, SeasonNet: 500},
	})

	if _, err := st.SeasonStatus(seasonGuild, "player", time.Date(2026, 8, 1, 0, 0, 0, 0, jst)); err != nil {
		t.Fatalf("SeasonStatus (opening August) returned error: %v", err)
	}
	view, err := st.SeasonStatus(seasonGuild, "player", time.Date(2026, 7, 31, 23, 59, 59, 0, jst))
	if err != nil {
		t.Fatalf("SeasonStatus returned error: %v", err)
	}

	if view.Month != "2026-08" {
		t.Fatalf("Month = %q, want %q — the rollover is forward only", view.Month, "2026-08")
	}
	if view.DaysLeft != 31 {
		t.Errorf("DaysLeft = %d, want 31 (August in full), not July's last day counted against August", view.DaysLeft)
	}
}

// The ordinary case is unchanged: a reader inside the month it is shown gets
// that day's own count, and a season_month no one can parse falls back to the
// caller's instant rather than to a guessed length.
func TestSeasonDaysLeftIn_InsideTheMonthAndOnAnUnreadableOne(t *testing.T) {
	cases := []struct {
		name  string
		month string
		now   time.Time
		want  int
	}{
		{"last day of the month", "2026-07", seasonJuly, 1},
		{"first day of the month", "2026-08", time.Date(2026, 8, 1, 0, 30, 0, 0, jst), 31},
		{"february in a leap year", "2028-02", time.Date(2028, 2, 10, 12, 0, 0, 0, jst), 20},
		{"hand-edited month", "not-a-month", seasonJuly, 1},
		{"never opened", "", seasonJuly, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := seasonDaysLeftIn(tc.month, tc.now); got != tc.want {
				t.Fatalf("seasonDaysLeftIn(%q, %s) = %d, want %d", tc.month, tc.now.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}
