package casino

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// --- in-memory game sessions (設計書 §5) -----------------------------------
//
// A session is the live board of a button game: the hand or the card row
// that /highlow and /blackjack draw, keyed by an opaque ID that travels in
// the buttons' custom_id. Sessions are deliberately NOT persisted — the
// stake lives in the account's escrow (Store.OpenGame), the board does not.
// A restart therefore loses no chips: RefundStaleEscrows returns every
// escrow whose board died with the process.
//
// This file is discordgo-free. MessageRef carries the two IDs the command
// layer needs to edit the original message; nothing here calls Discord.

// ErrSessionNotFound means the ID is unknown: the session was settled, swept
// as stale, or lost to a restart. The command layer turns it into the
// "⌛ この盤面はもう終了しています" reply for a button pressed on an old message.
var ErrSessionNotFound = errors.New("casino: session not found")

// GameKind names the game a session is running. It is part of the custom_id
// namespace, so the values are stable strings, not iota.
type GameKind string

const (
	GameHighLow   GameKind = "highlow"
	GameBlackjack GameKind = "blackjack"
)

// DefaultSessionTTL is the idle timeout from 設計書 §5: a board untouched for
// three minutes is auto-resolved by the sweeper (C2-08).
const DefaultSessionTTL = 3 * time.Minute

// MessageRef locates the message whose components drive a session, so the
// sweeper can edit it to "⌛ 時間切れ — 自動決着" without an interaction token
// (tokens expire in 15 minutes; the bot token used by ChannelMessageEdit
// does not).
type MessageRef struct {
	ChannelID string
	MessageID string
}

// Session is one in-flight board. ID, GuildID, UserID, Game and Ref are
// immutable after Open; State and LastActionAt are mutable and may only be
// touched while the manager's lock is held (i.e. from inside WithSession,
// Touch, or on a session already removed from the manager by Sweep).
type Session struct {
	ID           string
	GuildID      string
	UserID       string
	Game         GameKind
	State        any
	LastActionAt time.Time
	Ref          MessageRef
}

// AutoResolver is the single method the sweeper needs from a game board:
// settle it the least-favourable-to-nobody way and report the payout to
// hand to Store.SettleGame. *HighLowGame (cash out what is on the table)
// and *BlackjackGame (stand) both implement it.
type AutoResolver interface {
	AutoResolve() int64
}

// SessionManager owns every live board in the process. It guards two maps
// with one mutex: id -> session, and guild+user -> id, which is what makes
// "one game per person per guild" checkable in O(1) (設計書 §5).
//
// RE-ENTRANCY CONTRACT (危険地帯 — violating this deadlocks the bot):
// mu is a plain, NON-reentrant sync.Mutex, and WithSession holds it for the
// whole of fn. fn must therefore NOT call any method on the manager (Open,
// Get, WithSession, Sweep, Touch, Close) and must not perform Discord calls,
// disk I/O, or sleeps — every other button press in the process waits on it.
// Persisting the settlement (casino.Store.SettleGame) belongs AFTER
// WithSession returns, not inside fn.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	byUser   map[string]string
	now      func() time.Time
	ttl      time.Duration
}

// NewSessionManager returns an empty manager. now may be nil (time.Now) and
// ttl may be zero (DefaultSessionTTL); tests pin both.
func NewSessionManager(now func() time.Time, ttl time.Duration) *SessionManager {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	return &SessionManager{
		sessions: make(map[string]*Session),
		byUser:   make(map[string]string),
		now:      now,
		ttl:      ttl,
	}
}

var (
	defaultSessionsOnce sync.Once
	defaultSessions     *SessionManager
)

// DefaultSessions returns the process-wide singleton manager. Every casino
// command and the sweep goroutine must call DefaultSessions() — not
// NewSessionManager — so that "one game per person" and the auto-resolve
// sweep see the same map, exactly as casino.Default() does for the stakes.
func DefaultSessions() *SessionManager {
	defaultSessionsOnce.Do(func() {
		defaultSessions = NewSessionManager(time.Now, DefaultSessionTTL)
	})
	return defaultSessions
}

// userKey joins the two IDs with a byte that cannot appear in a Discord
// snowflake, so no pair of (guild, user) can collide with another.
func userKey(guildID, userID string) string { return guildID + "\x00" + userID }

// newSessionID returns 16 crypto/rand bytes as hex. It is not a counter and
// not derived from the user: the ID travels in a custom_id that anybody can
// read off a message, so a guessable ID would let a third party address
// somebody else's board (the owner check in requireSessionOwner is the
// other half of that defence).
func newSessionID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// Open registers a new board for guild+user. It returns ErrGameInProgress if
// that pair already has one — the caller must refuse the bet BEFORE moving
// chips into escrow, because a second OpenGame would strand the first
// stake (see Store.OpenGame's ErrGameInProgress, the persisted half of the
// same rule).
func (m *SessionManager) Open(guildID, userID string, game GameKind, state any, ref MessageRef) (*Session, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := userKey(guildID, userID)
	if existing, ok := m.byUser[key]; ok {
		if _, live := m.sessions[existing]; live {
			return nil, ErrGameInProgress
		}
		// Index entry without a session cannot happen while every removal
		// goes through removeLocked; drop it rather than lock the user out.
		delete(m.byUser, key)
	}

	session := &Session{
		ID:           id,
		GuildID:      guildID,
		UserID:       userID,
		Game:         game,
		State:        state,
		LastActionAt: m.now(),
		Ref:          ref,
	}
	m.sessions[id] = session
	m.byUser[key] = id
	return session, nil
}

// Get returns a copy of the session's bookkeeping fields, so an ownership or
// routing check can read GuildID/UserID/Game/Ref without holding the lock.
// The copy's State still points at the SHARED board: read or mutate it only
// from inside WithSession.
func (m *SessionManager) Get(id string) (Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return Session{}, false
	}
	return *session, true
}

// WithSession applies fn to the live board under the manager's lock, which
// is what serializes two fast button presses (and a concurrent Sweep)
// against the same hand. fn reports whether the board is finished; a
// finished session is removed from the maps immediately, so the second
// press gets ErrSessionNotFound instead of settling the hand twice. fn's
// error is returned as-is; done alone decides removal, so a rejected move
// (e.g. ErrDoubleUnavailable, done=false) leaves the board playable while a
// board that finished AND reported an error still leaves the maps — a
// finished board must never stay addressable.
//
// LastActionAt is refreshed on every successful call, so an active player
// never trips the idle sweep.
func (m *SessionManager) WithSession(id string, fn func(*Session) (done bool, err error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}

	done, err := fn(session)
	if done {
		m.removeLocked(session)
		return err
	}
	if err != nil {
		return err
	}
	session.LastActionAt = m.now()
	return nil
}

// Touch extends a session's life without changing the board — for the
// interactions that redraw a message but take no game action. Unknown IDs
// are ignored (the session may have just been settled).
func (m *SessionManager) Touch(id string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, ok := m.sessions[id]; ok {
		session.LastActionAt = now
	}
}

// Sweep removes every session idle for longer than the TTL and returns them.
// Removal happens inside the same critical section as the deadline test, so
// a session is handed to exactly one caller, once: the sweep goroutine can
// settle it (AutoResolve → SettleGame → edit the message) outside the lock
// without racing a button press, because the presser now gets
// ErrSessionNotFound.
//
// The returned sessions are no longer reachable from the manager, so the
// caller may read and mutate their State freely.
func (m *SessionManager) Sweep(now time.Time) []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	var expired []*Session
	for _, session := range m.sessions {
		if now.Sub(session.LastActionAt) >= m.ttl {
			expired = append(expired, session)
		}
	}
	for _, session := range expired {
		m.removeLocked(session)
	}
	return expired
}

// Close removes a session without running the board, for the paths that
// settle outside WithSession (a cancelled /highlow, a failed Discord edit
// that leaves nothing to press). It reports whether the session was still
// live, so the caller can tell "I closed it" from "somebody else already
// did" and avoid paying twice.
func (m *SessionManager) Close(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return false
	}
	m.removeLocked(session)
	return true
}

// Len reports how many boards are live. For the sweeper's logs and tests.
func (m *SessionManager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// removeLocked drops a session from BOTH maps. The byUser entry is deleted
// only when it still points at this session: a user who opened a new board
// after this one ended must not have their live game unindexed.
func (m *SessionManager) removeLocked(session *Session) {
	delete(m.sessions, session.ID)
	key := userKey(session.GuildID, session.UserID)
	if id, ok := m.byUser[key]; ok && id == session.ID {
		delete(m.byUser, key)
	}
}
