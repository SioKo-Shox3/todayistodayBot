package casino

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fixedClock is a hand-cranked clock for the manager: tests move time
// forward explicitly rather than sleeping, so an idle-timeout test costs
// microseconds and never flakes on a slow machine.
type fixedClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFixedClock(t time.Time) *fixedClock { return &fixedClock{now: t} }

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fixedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// testRef is an arbitrary but non-empty message location; the manager only
// carries it, so its contents never matter beyond round-tripping.
var testRef = MessageRef{ChannelID: "chan-1", MessageID: "msg-1"}

func TestSessionOpenRefusesASecondGameForTheSamePlayer(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	m := NewSessionManager(clock.Now, time.Minute)

	first, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if first.ID == "" {
		t.Fatal("Open returned an empty session ID")
	}

	second, err := m.Open("guild-1", "user-1", GameBlackjack, &struct{}{}, testRef)
	if !errors.Is(err, ErrGameInProgress) {
		t.Fatalf("second Open for the same guild+user: got err=%v, want ErrGameInProgress", err)
	}
	if second != nil {
		t.Fatalf("refused Open returned a session: %+v", second)
	}
	if got := m.Len(); got != 1 {
		t.Fatalf("live sessions after a refused Open: got %d, want 1", got)
	}

	// The rule is per guild AND per user: the same person in another guild,
	// and another person in this guild, may both play.
	if _, err := m.Open("guild-2", "user-1", GameHighLow, &struct{}{}, testRef); err != nil {
		t.Fatalf("same user in another guild: %v", err)
	}
	if _, err := m.Open("guild-1", "user-2", GameHighLow, &struct{}{}, testRef); err != nil {
		t.Fatalf("another user in the same guild: %v", err)
	}
	if got := m.Len(); got != 3 {
		t.Fatalf("live sessions: got %d, want 3", got)
	}
}

func TestSessionOpenIssuesDistinctIDs(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	m := NewSessionManager(clock.Now, time.Minute)

	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		// A fresh user each time, so the one-game rule never interferes.
		s, err := m.Open("guild-1", string(rune('a'+i%26))+string(rune('a'+i/26)), GameHighLow, &struct{}{}, testRef)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		if len(s.ID) != 32 {
			t.Fatalf("session ID %q: got %d hex chars, want 32 (16 crypto/rand bytes)", s.ID, len(s.ID))
		}
		if seen[s.ID] {
			t.Fatalf("duplicate session ID %q at #%d", s.ID, i)
		}
		seen[s.ID] = true
	}
}

func TestSessionGetCopiesTheBookkeepingFields(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	m := NewSessionManager(clock.Now, time.Minute)

	opened, err := m.Open("guild-1", "user-1", GameBlackjack, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	got, ok := m.Get(opened.ID)
	if !ok {
		t.Fatal("Get on a live session: ok=false")
	}
	if got.GuildID != "guild-1" || got.UserID != "user-1" || got.Game != GameBlackjack || got.Ref != testRef {
		t.Fatalf("Get returned %+v, want the fields passed to Open", got)
	}
	if !got.LastActionAt.Equal(clock.Now()) {
		t.Fatalf("LastActionAt: got %v, want the Open instant %v", got.LastActionAt, clock.Now())
	}

	if _, ok := m.Get("no-such-session"); ok {
		t.Fatal("Get on an unknown ID: ok=true")
	}
}

func TestSessionWithSessionRemovesAFinishedBoard(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	m := NewSessionManager(clock.Now, time.Minute)

	opened, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// A move that does not finish the hand keeps the session addressable.
	applied := 0
	if err := m.WithSession(opened.ID, func(s *Session) (bool, error) {
		applied++
		if s.ID != opened.ID {
			t.Errorf("WithSession handed over session %q, want %q", s.ID, opened.ID)
		}
		return false, nil
	}); err != nil {
		t.Fatalf("WithSession (not done): %v", err)
	}
	if _, ok := m.Get(opened.ID); !ok {
		t.Fatal("session vanished after a move that returned done=false")
	}

	// A rejected move leaves the board playable: fn's error is returned
	// verbatim and the session stays.
	sentinel := errors.New("move rejected")
	if err := m.WithSession(opened.ID, func(*Session) (bool, error) {
		return false, sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("WithSession with a failing fn: got %v, want the fn's error", err)
	}
	if _, ok := m.Get(opened.ID); !ok {
		t.Fatal("session removed although fn returned done=false with an error")
	}

	// The finishing move drops it from both maps: pressing the button again
	// reports ErrSessionNotFound instead of settling the hand twice...
	if err := m.WithSession(opened.ID, func(*Session) (bool, error) {
		applied++
		return true, nil
	}); err != nil {
		t.Fatalf("WithSession (done): %v", err)
	}
	if applied != 2 {
		t.Fatalf("fn applications: got %d, want 2", applied)
	}
	if _, ok := m.Get(opened.ID); ok {
		t.Fatal("finished session is still addressable")
	}
	if err := m.WithSession(opened.ID, func(*Session) (bool, error) {
		t.Error("fn ran for a finished session")
		return true, nil
	}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("WithSession on a finished session: got %v, want ErrSessionNotFound", err)
	}

	// ...and the player may start a new game (the guild+user index was
	// cleared too, not just the id map).
	if _, err := m.Open("guild-1", "user-1", GameBlackjack, &struct{}{}, testRef); err != nil {
		t.Fatalf("Open after the previous game finished: %v", err)
	}
}

// A board can finish AND fail at the same time (the hand is settled, the
// follow-up reports an error). done alone decides removal: leaving such a
// session addressable would let a second press settle the same hand twice
// and would keep it in the sweeper's queue.
func TestSessionWithSessionRemovesAFinishedBoardThatAlsoErrored(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	m := NewSessionManager(clock.Now, time.Minute)

	opened, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	sentinel := errors.New("settled but reporting")
	if err := m.WithSession(opened.ID, func(*Session) (bool, error) {
		return true, sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("WithSession with done=true and an error: got %v, want the fn's error", err)
	}
	if _, ok := m.Get(opened.ID); ok {
		t.Fatal("finished session is still addressable after fn returned done=true with an error")
	}
	if swept := m.Sweep(clock.Now().Add(time.Hour)); len(swept) != 0 {
		t.Fatalf("Sweep still sees the finished session: got %d, want 0", len(swept))
	}
	// The guild+user index was cleared too, so the player is not locked out.
	if _, err := m.Open("guild-1", "user-1", GameBlackjack, &struct{}{}, testRef); err != nil {
		t.Fatalf("Open after the errored finish: %v", err)
	}
}

func TestSessionWithSessionRefreshesTheIdleDeadline(t *testing.T) {
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	clock := newFixedClock(start)
	m := NewSessionManager(clock.Now, 3*time.Minute)

	opened, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	clock.advance(2 * time.Minute)
	if err := m.WithSession(opened.ID, func(*Session) (bool, error) { return false, nil }); err != nil {
		t.Fatalf("WithSession: %v", err)
	}

	// 2 minutes after Open but 0 after the move: not stale.
	if swept := m.Sweep(clock.Now()); len(swept) != 0 {
		t.Fatalf("Sweep right after a move: got %d sessions, want 0", len(swept))
	}
	if got, _ := m.Get(opened.ID); !got.LastActionAt.Equal(start.Add(2 * time.Minute)) {
		t.Fatalf("LastActionAt: got %v, want the move instant %v", got.LastActionAt, start.Add(2*time.Minute))
	}
}

func TestSessionSweepReturnsOnlyIdleSessions(t *testing.T) {
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	clock := newFixedClock(start)
	m := NewSessionManager(clock.Now, 3*time.Minute)

	old, err := m.Open("guild-1", "user-old", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open old: %v", err)
	}

	clock.advance(2 * time.Minute)
	fresh, err := m.Open("guild-1", "user-fresh", GameBlackjack, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open fresh: %v", err)
	}

	// t+3m: the first session is exactly at the TTL (the deadline is
	// inclusive), the second has been idle for one minute.
	clock.advance(time.Minute)
	swept := m.Sweep(clock.Now())
	if len(swept) != 1 {
		t.Fatalf("Sweep at the TTL boundary: got %d sessions, want 1", len(swept))
	}
	if swept[0].ID != old.ID {
		t.Fatalf("Sweep returned session %q, want the idle one %q", swept[0].ID, old.ID)
	}
	if swept[0].Ref != testRef {
		t.Fatalf("swept session lost its MessageRef: %+v", swept[0].Ref)
	}
	if !swept[0].Expired {
		t.Error("the swept session is not marked Expired, so nothing refuses a press on it")
	}
	if _, ok := m.Get(old.ID); ok {
		t.Fatal("swept session is still addressable")
	}
	if _, ok := m.Get(fresh.ID); !ok {
		t.Fatal("Sweep expired a session that was not idle yet")
	}

	// The board is removed by its settlement, not by the timeout; only then
	// is the player free to start a new game. Until it lands the stake is
	// still in escrow, so a new board would have nothing to stake.
	if !m.Remove(old.ID) {
		t.Fatal("Remove did not take the expired board")
	}
	if _, err := m.Open("guild-1", "user-old", GameHighLow, &struct{}{}, testRef); err != nil {
		t.Fatalf("Open after the swept board was settled and removed: %v", err)
	}

	// Once the survivor goes past the TTL it is expired in turn.
	clock.advance(3 * time.Minute)
	swept = m.Sweep(clock.Now())
	ids := make(map[string]bool, len(swept))
	for _, s := range swept {
		ids[s.ID] = true
	}
	if !ids[fresh.ID] {
		t.Fatalf("Sweep did not return the now-idle session %q (got %d sessions)", fresh.ID, len(swept))
	}
}

// C2-10: a board is removed by its settlement, not by the timeout. Sweep
// therefore keeps handing back a board it already expired — that is the
// retry — and stops only once Remove says the chips have landed.
func TestSessionSweepKeepsReturningAnExpiredBoardUntilRemoved(t *testing.T) {
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	clock := newFixedClock(start)
	m := NewSessionManager(clock.Now, 3*time.Minute)

	opened, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	clock.advance(3 * time.Minute)
	if swept := m.Sweep(clock.Now()); len(swept) != 1 || swept[0].ID != opened.ID {
		t.Fatalf("first Sweep: got %d sessions, want the idle one", len(swept))
	}

	// The settlement failed, so nothing removed the board. The next pass —
	// one sweep interval later, not another TTL later — must hand back the
	// SAME session, still expired.
	clock.advance(30 * time.Second)
	again := m.Sweep(clock.Now())
	if len(again) != 1 || again[0].ID != opened.ID {
		t.Fatalf("the retry Sweep: got %d sessions, want the expired board back", len(again))
	}
	if !again[0].Expired {
		t.Error("the board handed back by the retry is not marked Expired")
	}

	// The payout the sweeper parks on the board survives to the retry: Sweep
	// hands out the board itself, not a copy, so the retry pays the number
	// the first pass resolved instead of resolving the hand again.
	again[0].PendingPayout = 173
	again[0].PayoutResolved = true
	retried := m.Sweep(clock.Now())
	if len(retried) != 1 || retried[0].PendingPayout != 173 || !retried[0].PayoutResolved {
		t.Fatalf("the resolved payout did not survive the retry: %+v", retried)
	}

	// Settled at last: Remove is what ends the retries.
	if !m.Remove(opened.ID) {
		t.Fatal("Remove did not take the expired board")
	}
	if swept := m.Sweep(clock.Now()); len(swept) != 0 {
		t.Fatalf("Sweep after Remove: got %d sessions, want 0 — a removed board must never be paid again", len(swept))
	}
	if m.Remove(opened.ID) {
		t.Error("Remove reported true twice for one board")
	}
}

// An expired board belongs to the sweep goroutine alone: it is settling that
// hand outside the manager's lock. Every press path must therefore refuse it
// (the command layer's ⌛「この盤面はもう終了しています」), and its owner stays
// blocked from a new game until the stake in escrow has actually been paid.
func TestSessionExpiredBoardIsRefusedByEveryPressPath(t *testing.T) {
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	clock := newFixedClock(start)
	m := NewSessionManager(clock.Now, 3*time.Minute)

	opened, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	clock.advance(3 * time.Minute)
	if swept := m.Sweep(clock.Now()); len(swept) != 1 {
		t.Fatalf("Sweep: got %d sessions, want the idle one", len(swept))
	}

	if _, ok := m.Get(opened.ID); ok {
		t.Error("Get answered for an expired board")
	}
	if _, ok := m.Hold(opened.ID); ok {
		t.Error("Hold accepted an expired board, so a press would play a hand the sweeper is settling")
	}
	if err := m.WithSession(opened.ID, func(*Session) (bool, error) {
		t.Error("WithSession ran fn on an expired board")
		return true, nil
	}); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("WithSession on an expired board: got %v, want ErrSessionNotFound", err)
	}
	if m.Close(opened.ID) {
		t.Error("Close claimed an expired board, which would refund a stake the sweeper is about to settle")
	}

	// Touch must not push back the deadline of a board the sweep already owns.
	m.Touch(opened.ID, clock.Now().Add(time.Hour))
	if swept := m.Sweep(clock.Now()); len(swept) != 1 {
		t.Fatalf("Sweep after a Touch on an expired board: got %d, want the same board back", len(swept))
	}

	// The stake is still in escrow, so the account still has a game in
	// progress: Store.OpenGame would refuse the next bet, and the manager
	// must give the same answer rather than deal a board nothing can stake.
	if _, err := m.Open("guild-1", "user-1", GameBlackjack, &struct{}{}, testRef); !errors.Is(err, ErrGameInProgress) {
		t.Errorf("Open while the previous board is expired: got %v, want ErrGameInProgress", err)
	}

	m.Remove(opened.ID)
	if _, err := m.Open("guild-1", "user-1", GameBlackjack, &struct{}{}, testRef); err != nil {
		t.Errorf("Open after the expired board was settled and removed: %v", err)
	}
}

func TestSessionTouchExtendsTheDeadline(t *testing.T) {
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	clock := newFixedClock(start)
	m := NewSessionManager(clock.Now, 3*time.Minute)

	opened, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	clock.advance(2 * time.Minute)
	m.Touch(opened.ID, clock.Now())

	// t+3m from Open, but only t+1m from the Touch: still alive.
	clock.advance(time.Minute)
	if swept := m.Sweep(clock.Now()); len(swept) != 0 {
		t.Fatalf("Sweep one minute after a Touch: got %d sessions, want 0", len(swept))
	}

	// t+5m from Open, t+3m from the Touch: now it goes.
	clock.advance(2 * time.Minute)
	swept := m.Sweep(clock.Now())
	if len(swept) != 1 || swept[0].ID != opened.ID {
		t.Fatalf("Sweep three minutes after the Touch: got %d sessions, want the touched one", len(swept))
	}

	// Touching an unknown ID is a no-op, not a panic: the board may have
	// been settled between the button press and the redraw.
	m.Touch(opened.ID, clock.Now())
	m.Touch("no-such-session", clock.Now())
}

func TestSessionCloseRemovesTheBoardOnce(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	m := NewSessionManager(clock.Now, time.Minute)

	opened, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if !m.Close(opened.ID) {
		t.Fatal("Close on a live session returned false")
	}
	if m.Close(opened.ID) {
		t.Fatal("Close on an already-closed session returned true (it would pay twice)")
	}
	if got := m.Len(); got != 0 {
		t.Fatalf("live sessions after Close: got %d, want 0", got)
	}
}

func TestSessionWithSessionSerializesConcurrentPresses(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	m := NewSessionManager(clock.Now, time.Minute)

	// The board is a plain counter: every applied move must be visible to
	// the next one, so a lost update shows up as a count below 100.
	type board struct{ moves int }
	opened, err := m.Open("guild-1", "user-1", GameHighLow, &board{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const presses = 100
	var (
		start sync.WaitGroup
		done  sync.WaitGroup
		errMu sync.Mutex
		errs  []error
	)
	start.Add(1)
	done.Add(presses)
	for i := 0; i < presses; i++ {
		go func() {
			defer done.Done()
			start.Wait()
			err := m.WithSession(opened.ID, func(s *Session) (bool, error) {
				b := s.State.(*board)
				b.moves++
				return false, nil
			})
			if err != nil {
				errMu.Lock()
				errs = append(errs, err)
				errMu.Unlock()
			}
		}()
	}
	start.Done()
	done.Wait()

	if len(errs) != 0 {
		t.Fatalf("WithSession failed %d times, first: %v", len(errs), errs[0])
	}

	var moves int
	if err := m.WithSession(opened.ID, func(s *Session) (bool, error) {
		moves = s.State.(*board).moves
		return true, nil
	}); err != nil {
		t.Fatalf("final WithSession: %v", err)
	}
	if moves != presses {
		t.Fatalf("applied moves: got %d, want %d", moves, presses)
	}
}

func TestSessionDefaultsMatchTheSpec(t *testing.T) {
	if DefaultSessionTTL != 3*time.Minute {
		t.Fatalf("DefaultSessionTTL: got %v, want 3m (設計書 §5)", DefaultSessionTTL)
	}

	m := DefaultSessions()
	if m == nil {
		t.Fatal("DefaultSessions returned nil")
	}
	if m != DefaultSessions() {
		t.Fatal("DefaultSessions returned two different managers (the sweep goroutine would not see the commands' boards)")
	}
	if m.ttl != DefaultSessionTTL {
		t.Fatalf("singleton TTL: got %v, want %v", m.ttl, DefaultSessionTTL)
	}
	if m.now == nil {
		t.Fatal("singleton clock is nil")
	}
	if delta := time.Since(m.now()); delta < 0 || delta > time.Minute {
		t.Fatalf("singleton clock is not time.Now: it reports %v away from now", delta)
	}

	// Zero values fall back to the production defaults rather than making
	// every session instantly stale.
	fallback := NewSessionManager(nil, 0)
	if fallback.ttl != DefaultSessionTTL {
		t.Fatalf("NewSessionManager(nil, 0) TTL: got %v, want %v", fallback.ttl, DefaultSessionTTL)
	}
	if fallback.now == nil {
		t.Fatal("NewSessionManager(nil, 0) left the clock nil")
	}
}

// The sweeper only needs AutoResolve from a board; pin that both games
// satisfy it, so C2-08 can settle a swept session without a type switch.
var (
	_ AutoResolver = (*HighLowGame)(nil)
	_ AutoResolver = (*BlackjackGame)(nil)
)

// --- 評価者の指摘(反復 1): 操作中の盤面は掃除しない -----------------------

// A press cannot hold the manager's lock across the disk I/O its move needs
// (Store.AddToEscrow for the ⏫ double), and LastActionAt is only refreshed
// when WithSession returns. Hold is what keeps the sweeper off the board in
// between, so the press — not the sweeper — settles the stake it just took.
func TestSessionSweepSkipsAHeldBoard(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	m := NewSessionManager(clock.Now, time.Minute)

	held, err := m.Open("guild-1", "user-1", GameBlackjack, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("opening the held board: %v", err)
	}
	idle, err := m.Open("guild-1", "user-2", GameBlackjack, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("opening the idle board: %v", err)
	}

	copied, ok := m.Hold(held.ID)
	if !ok {
		t.Fatal("Hold refused a live board")
	}
	if copied.ID != held.ID || copied.UserID != "user-1" {
		t.Errorf("Hold returned %+v, want the bookkeeping copy of the held board", copied)
	}

	clock.advance(time.Minute)
	expired := m.Sweep(clock.Now())

	if len(expired) != 1 || expired[0].ID != idle.ID {
		t.Fatalf("Sweep returned %d board(s), want only the unheld one", len(expired))
	}
	if _, live := m.Get(held.ID); !live {
		t.Error("the held board was swept away from the press that holds it")
	}

	// The sweeper settles the board it was handed and removes it; without
	// that the next pass hands the same one back (the C2-10 retry).
	if !m.Remove(idle.ID) {
		t.Fatal("Remove did not take the swept board")
	}

	// Released and still idle: the next pass takes it. A hold that outlived
	// its press would strand the board until the next restart.
	m.Release(held.ID)
	if expired := m.Sweep(clock.Now()); len(expired) != 1 || expired[0].ID != held.ID {
		t.Fatalf("after Release, Sweep returned %d board(s), want the formerly held one", len(expired))
	}
}

// Hold is the caller's "is this board still there?" as well as its guard, so
// a board that is already gone must refuse it rather than hand back a zero
// session the press would then work on.
func TestSessionHoldRefusesAGoneBoard(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	m := NewSessionManager(clock.Now, time.Minute)

	session, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !m.Close(session.ID) {
		t.Fatal("Close did not remove a live board")
	}

	if _, ok := m.Hold(session.ID); ok {
		t.Error("Hold accepted a board that is already gone")
	}
	m.Release(session.ID) // must not panic: the counter went with the board
}

// Two presses can be in flight on different boards at once, and each must
// hold only its own. The counter also has to survive nesting: one board held
// twice stays out of the sweep until BOTH holds end.
func TestSessionHoldCountsEveryOperation(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	m := NewSessionManager(clock.Now, time.Minute)

	session, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, ok := m.Hold(session.ID); !ok {
			t.Fatalf("Hold %d refused a live board", i+1)
		}
	}
	clock.advance(time.Minute)

	m.Release(session.ID)
	if expired := m.Sweep(clock.Now()); len(expired) != 0 {
		t.Fatalf("Sweep took a board that is still held once (%d returned)", len(expired))
	}
	m.Release(session.ID)
	if expired := m.Sweep(clock.Now()); len(expired) != 1 {
		t.Fatalf("Sweep returned %d board(s) after the last Release, want 1", len(expired))
	}
}

func TestSessionSetNeedsRedrawMarksOnlyALiveBoard(t *testing.T) {
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	clock := newFixedClock(start)
	m := NewSessionManager(clock.Now, 3*time.Minute)

	opened, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if session, _ := m.Get(opened.ID); session.NeedsRedraw {
		t.Fatal("a board is born needing a redraw")
	}

	m.SetNeedsRedraw(opened.ID, true)
	session, live := m.Get(opened.ID)
	if !live {
		t.Fatal("marking a board for redraw closed it")
	}
	if !session.NeedsRedraw {
		t.Error("the flag did not reach the board")
	}

	// Raising the flag is the bot's bookkeeping, not the player's activity:
	// a board nobody is playing must still expire on schedule.
	clock.advance(3 * time.Minute)
	if swept := m.Sweep(clock.Now()); len(swept) != 1 {
		t.Fatalf("Sweep of a flagged, idle board: got %d sessions, want 1", len(swept))
	}

	// An expired board belongs to the sweeper, which replaces the whole
	// message; an unknown one is gone. Neither takes the flag.
	m.SetNeedsRedraw(opened.ID, false)
	if !m.sessions[opened.ID].NeedsRedraw {
		t.Error("an expired board took a flag change from the command layer")
	}
	m.SetNeedsRedraw("no-such-session", true) // a no-op, not a panic
}

func TestSessionSetNeedsRedrawLowersTheFlag(t *testing.T) {
	m := NewSessionManager(time.Now, 3*time.Minute)
	opened, err := m.Open("guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	m.SetNeedsRedraw(opened.ID, true)
	m.SetNeedsRedraw(opened.ID, false)
	if session, _ := m.Get(opened.ID); session.NeedsRedraw {
		t.Error("the flag stayed up after the redraw landed, so every later press would only redraw")
	}
}

// --- the ID the caller brings (C3B-18) --------------------------------------

// OpenWithID exists so the command layer can mark a stake with its board
// BEFORE the board is published: the chips move first (C2-06), and the only
// way for them to name their real owner from that first write is for the ID
// to already exist. The board the manager publishes must therefore be the
// one the caller was given.
func TestOpenWithIDPublishesTheCallersOwnID(t *testing.T) {
	m := NewSessionManager(time.Now, 3*time.Minute)

	id, err := NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	opened, err := m.OpenWithID(id, "guild-1", "user-1", GameHighLow, &struct{}{}, testRef)
	if err != nil {
		t.Fatalf("OpenWithID: %v", err)
	}
	if opened.ID != id {
		t.Fatalf("the board was published as %q, want the ID the stake was marked with (%q)", opened.ID, id)
	}
	if found, ok := m.Get(id); !ok || found.ID != id {
		t.Fatalf("Get(%q) = %+v, %v — the board must be addressable by the caller's ID", id, found, ok)
	}

	// The one-game rule does not care where the ID came from.
	other, err := NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	if _, err := m.OpenWithID(other, "guild-1", "user-1", GameBlackjack, &struct{}{}, testRef); !errors.Is(err, ErrGameInProgress) {
		t.Fatalf("a second board for the same player returned %v, want ErrGameInProgress", err)
	}
}

// An ID that is already live must be refused rather than taken over: the
// second caller's stake is marked with it too, so overwriting the board would
// hand one player's chips to another player's game.
func TestOpenWithIDRefusesAnIDThatIsAlreadyLive(t *testing.T) {
	m := NewSessionManager(time.Now, 3*time.Minute)

	if _, err := m.OpenWithID("shared-id", "guild-1", "user-1", GameHighLow, &struct{}{}, testRef); err != nil {
		t.Fatalf("OpenWithID: %v", err)
	}
	if _, err := m.OpenWithID("shared-id", "guild-1", "user-2", GameBlackjack, &struct{}{}, testRef); !errors.Is(err, ErrSessionIDTaken) {
		t.Fatalf("reusing a live ID returned %v, want ErrSessionIDTaken", err)
	}
	if found, ok := m.Get("shared-id"); !ok || found.UserID != "user-1" || found.Game != GameHighLow {
		t.Fatalf("the live board is now %+v, %v — the refused open took it over", found, ok)
	}
	if m.Len() != 1 {
		t.Fatalf("%d boards are live, want 1", m.Len())
	}
}

// NewSessionID is exported for the command layer, so its shape is part of the
// contract now: 32 hex characters, and never the same twice.
func TestNewSessionIDIsUnguessableAndUnique(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		id, err := NewSessionID()
		if err != nil {
			t.Fatalf("NewSessionID: %v", err)
		}
		if len(id) != 32 {
			t.Fatalf("NewSessionID() = %q (%d chars), want 32 hex characters", id, len(id))
		}
		if seen[id] {
			t.Fatalf("NewSessionID() repeated %q", id)
		}
		seen[id] = true
	}
}
