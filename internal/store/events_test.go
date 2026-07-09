package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStore_Save_PersistsAndIsReadableByFreshInstance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chosei-events.json")

	record := EventRecord{
		ChannelID:  "channel-1",
		EventID:    "event-1",
		PublicSlug: "abc123",
		OwnerToken: "owner-tok",
		CreatedAt:  time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC),
		CreatedBy:  "user-1",
	}

	if err := New(path).Save(record); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file to exist after Save: %v", err)
	}
	var records []EventRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
	if len(records) != 1 || records[0].EventID != "event-1" || records[0].OwnerToken != "owner-tok" {
		t.Fatalf("unexpected persisted records: %+v", records)
	}

	fresh, found, err := New(path).FindLatestByChannel("channel-1")
	if err != nil {
		t.Fatalf("FindLatestByChannel error: %v", err)
	}
	if !found || fresh.EventID != "event-1" {
		t.Fatalf("expected a fresh Store instance to read back the saved record, got found=%v record=%+v", found, fresh)
	}
}

func TestStore_Save_ReplacesExistingRecordWithSameEventID(t *testing.T) {
	dir := t.TempDir()
	st := New(filepath.Join(dir, "chosei-events.json"))

	first := EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "old-tok", CreatedAt: time.Now(), CreatedBy: "user-1"}
	if err := st.Save(first); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	second := first
	second.OwnerToken = "new-tok"
	if err := st.Save(second); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	record, found, err := st.FindLatestByChannel("channel-1")
	if err != nil || !found {
		t.Fatalf("FindLatestByChannel: found=%v err=%v", found, err)
	}
	if record.OwnerToken != "new-tok" {
		t.Fatalf("expected replaced record with new-tok, got %q", record.OwnerToken)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "chosei-events.json"))
	var records []EventRecord
	_ = json.Unmarshal(data, &records)
	if len(records) != 1 {
		t.Fatalf("expected exactly 1 record after re-saving the same EventID, got %d", len(records))
	}
}

func TestStore_FindLatestByChannel_MissingFile_ReturnsNotFoundNotError(t *testing.T) {
	dir := t.TempDir()
	st := New(filepath.Join(dir, "does-not-exist.json"))

	_, found, err := st.FindLatestByChannel("channel-1")
	if err != nil {
		t.Fatalf("expected no error for a missing file, got: %v", err)
	}
	if found {
		t.Fatal("expected found=false for a missing file")
	}
}

func TestDefault_ReturnsSameInstanceAcrossCalls(t *testing.T) {
	a := Default()
	b := Default()
	if a != b {
		t.Fatal("expected Default() to return the same *Store pointer on every call, so every caller shares one sync.Mutex")
	}
}

func TestStore_FindByChannelAndRef_MatchesEventIDOrPublicSlug(t *testing.T) {
	dir := t.TempDir()
	st := New(filepath.Join(dir, "chosei-events.json"))
	record := EventRecord{ChannelID: "channel-1", EventID: "event-1", PublicSlug: "abc123", OwnerToken: "tok", CreatedAt: time.Now(), CreatedBy: "user-1"}
	if err := st.Save(record); err != nil {
		t.Fatalf("Save: %v", err)
	}

	byID, ok, err := st.FindByChannelAndRef("channel-1", "event-1")
	if err != nil || !ok || byID.PublicSlug != "abc123" {
		t.Fatalf("expected match by EventID, got ok=%v err=%v record=%+v", ok, err, byID)
	}

	bySlug, ok, err := st.FindByChannelAndRef("channel-1", "abc123")
	if err != nil || !ok || bySlug.EventID != "event-1" {
		t.Fatalf("expected match by PublicSlug, got ok=%v err=%v record=%+v", ok, err, bySlug)
	}

	_, ok, err = st.FindByChannelAndRef("channel-2", "event-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected no match when ref exists but in a different channel — refs must be channel-scoped")
	}
}

func TestStore_Save_ConcurrentWritesDoNotCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chosei-events.json")
	st := New(path)

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := st.Save(EventRecord{
				ChannelID:  "channel-1",
				EventID:    fmt.Sprintf("event-%d", i),
				PublicSlug: fmt.Sprintf("slug-%d", i),
				OwnerToken: "tok",
				CreatedAt:  time.Now(),
				CreatedBy:  "user-1",
			})
			if err != nil {
				t.Errorf("Save() goroutine %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file after concurrent Save: %v", err)
	}
	var records []EventRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("file is not valid JSON after concurrent Save (mutex failed to serialize writes): %v", err)
	}
	if len(records) != n {
		t.Fatalf("expected %d records after %d concurrent Save calls, got %d — a lost update means the mutex isn't actually serializing writes", n, n, len(records))
	}
}
