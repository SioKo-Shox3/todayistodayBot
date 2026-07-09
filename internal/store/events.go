package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EventRecord is the local bookkeeping record the bot keeps for a
// chosei-sama event it created via /schedule: enough to resolve "the
// latest event in this channel" (or an explicit ref) and to
// re-authenticate as the event's owner for /schedule-result and
// /reminder — chosei-sama deliberately has no list-events API, so this
// is the bot's own index.
type EventRecord struct {
	ChannelID  string `json:"channel_id"`
	EventID    string `json:"event_id"`
	PublicSlug string `json:"public_slug"`
	// OwnerToken is chosei-sama's owner-auth secret for this event. Never
	// log it, never include it in a Discord response — treat it like
	// config.json's discord_token (CLAUDE.md's non-negotiable #1).
	OwnerToken string    `json:"owner_token"`
	CreatedAt  time.Time `json:"created_at"`
	CreatedBy  string    `json:"created_by"`
}

// DefaultPath is the production data file location, relative to the
// working directory the bot is launched from — mirrors config.json's
// "./config.json" convention (internal/config/config.go's defaultConfigPath).
const DefaultPath = "data/chosei-events.json"

// Store is a mutex-guarded single-writer JSON file store for EventRecord.
// Every writer of the SAME underlying file must share one *Store instance
// (one sync.Mutex) — see Default() for the production singleton.
type Store struct {
	mu   sync.Mutex
	path string
}

// New returns a Store backed by path. Production code should use Default()
// instead, to guarantee every command shares the same mutex; New is for
// tests, where each test's own t.TempDir()-backed file legitimately needs
// its own, independent Store.
func New(path string) *Store {
	return &Store{path: path}
}

var (
	defaultOnce  sync.Once
	defaultStore *Store
)

// Default returns the process-wide singleton Store for DefaultPath. Every
// command that persists or reads chosei-sama EventRecords must call
// Default() — not New(DefaultPath) — so every read/write to
// data/chosei-events.json goes through the same sync.Mutex.
func Default() *Store {
	defaultOnce.Do(func() { defaultStore = New(DefaultPath) })
	return defaultStore
}

// Save appends record, or replaces the existing record with the same
// EventID if one is already stored.
func (s *Store) Save(record EventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.readLocked()
	if err != nil {
		return err
	}

	replaced := false
	for i, r := range records {
		if r.EventID == record.EventID {
			records[i] = record
			replaced = true
			break
		}
	}
	if !replaced {
		records = append(records, record)
	}

	return s.writeLocked(records)
}

// FindLatestByChannel returns the most recently created EventRecord for
// channelID (ok=false if channelID has no stored records).
func (s *Store) FindLatestByChannel(channelID string) (EventRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.readLocked()
	if err != nil {
		return EventRecord{}, false, err
	}

	var latest EventRecord
	found := false
	for _, r := range records {
		if r.ChannelID != channelID {
			continue
		}
		if !found || r.CreatedAt.After(latest.CreatedAt) {
			latest = r
			found = true
		}
	}
	return latest, found, nil
}

// FindByChannelAndRef returns the EventRecord in channelID whose EventID or
// PublicSlug equals ref (ok=false if none matches). Scoped to channelID so
// a ref guessed/copied from a different channel cannot be used to read
// that channel's stored owner token from this one.
func (s *Store) FindByChannelAndRef(channelID, ref string) (EventRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.readLocked()
	if err != nil {
		return EventRecord{}, false, err
	}

	for _, r := range records {
		if r.ChannelID == channelID && (r.EventID == ref || r.PublicSlug == ref) {
			return r, true, nil
		}
	}
	return EventRecord{}, false, nil
}

// readLocked reads and parses the JSON file. Caller must hold mu. A
// missing file is treated as an empty store (not an error) — the
// pre-first-/schedule state.
func (s *Store) readLocked() ([]EventRecord, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: reading %q: %w", s.path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var records []EventRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("store: parsing %q: %w", s.path, err)
	}
	return records, nil
}

// writeLocked serializes records to a temp file in the same directory and
// renames it over the destination path, so a crash mid-write cannot leave
// a truncated/corrupt data/chosei-events.json (os.Rename is atomic within
// the same filesystem on both POSIX and Windows NTFS). Caller must hold mu.
func (s *Store) writeLocked(records []EventRecord) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("store: creating directory %q: %w", dir, err)
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshaling records: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".chosei-events-*.json.tmp")
	if err != nil {
		return fmt.Errorf("store: creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("store: writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("store: closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("store: renaming temp file to %q: %w", s.path, err)
	}
	return nil
}
