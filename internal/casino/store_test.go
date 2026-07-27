package casino

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	result, err := st.ClaimDaily("guild1", "user-1", fixedNow)
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
	if _, err := st.ClaimDaily("guild1", "user-1", fixedNow); err != nil {
		t.Fatalf("first ClaimDaily returned error: %v", err)
	}

	// Later the same JST calendar day.
	if _, err := st.ClaimDaily("guild1", "user-1", fixedNow.Add(8*time.Hour)); !errors.Is(err, ErrAlreadyClaimedToday) {
		t.Fatalf("second ClaimDaily on the same JST day = %v, want ErrAlreadyClaimedToday", err)
	}

	account := readAccount(t, path, "guild1", "user-1")
	if account.Chips != welcomeBonusChips+200 || account.StreakDays != 1 {
		t.Fatalf("a rejected claim must persist nothing, got %+v", account)
	}
}

func TestStore_ClaimDaily_ConsecutiveDay(t *testing.T) {
	st, path := newTempStore(t)
	if _, err := st.ClaimDaily("guild1", "user-1", fixedNow); err != nil {
		t.Fatalf("day 1 ClaimDaily returned error: %v", err)
	}

	result, err := st.ClaimDaily("guild1", "user-1", daysAfter(1))
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
	if _, err := st.ClaimDaily("guild1", "user-1", fixedNow); err != nil {
		t.Fatalf("day 1 ClaimDaily returned error: %v", err)
	}
	if _, err := st.ClaimDaily("guild1", "user-1", daysAfter(1)); err != nil {
		t.Fatalf("day 2 ClaimDaily returned error: %v", err)
	}

	// Day 3 skipped entirely; claim again on day 4.
	result, err := st.ClaimDaily("guild1", "user-1", daysAfter(3))
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
		result, err := st.ClaimDaily("guild1", "user-1", daysAfter(day))
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

func TestStore_ClaimDaily_ChipCapExceeded_NoMutation(t *testing.T) {
	st, path := newTempStore(t)
	seedAccounts(t, st, "guild1", map[string]UserAccount{"user-1": {Chips: MaxChips, Coins: 7}})

	if _, err := st.ClaimDaily("guild1", "user-1", fixedNow); !errors.Is(err, ErrChipCapExceeded) {
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

			first, err := st.ClaimDaily("guild1", "user-1", tc.first)
			if err != nil {
				t.Fatalf("first ClaimDaily returned error: %v", err)
			}
			if first.NewStreak != 1 {
				t.Fatalf("first claim NewStreak = %d, want 1", first.NewStreak)
			}

			second, err := st.ClaimDaily("guild1", "user-1", tc.second)
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
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			userID := fmt.Sprintf("user-%d", i)
			if _, err := st.ClaimDaily(guildID, userID, now); err != nil {
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
