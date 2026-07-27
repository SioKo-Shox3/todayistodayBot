package casino

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

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
