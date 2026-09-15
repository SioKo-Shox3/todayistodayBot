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

// ErrSessionIDTaken means OpenWithID was handed an ID that is already live.
// With NewSessionID values it is unreachable; it exists so that a caller who
// passes a fixed string cannot silently adopt somebody else's board.
var ErrSessionIDTaken = errors.New("casino: that session ID is already in use")

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
// Touch, or on an Expired session that Sweep handed to the sweeper — it stays
// in the manager until Remove after a successful settlement).
type Session struct {
	ID           string
	GuildID      string
	UserID       string
	Game         GameKind
	State        any
	LastActionAt time.Time
	Ref          MessageRef

	// Expired marks a board the idle sweep has taken over. A timeout does
	// NOT delete the board any more: the payout only becomes real once
	// casino.Store.SettleGame has persisted it, so a board whose settlement
	// failed has to stay somewhere the next sweep can find it. An expired
	// board is refused by every caller-facing path (Get, Hold, WithSession,
	// Touch, Close) and keeps its owner's "one game at a time" slot, which
	// is what stops a new game from opening on top of an escrow that is
	// still held. Only Remove drops it.
	Expired bool

	// PendingPayout is what AutoResolve produced for this expired board, and
	// PayoutResolved says whether it ran at all — a losing hand pays 0, so
	// the amount alone cannot answer that. A retried settlement pays exactly
	// this number instead of resolving the board a second time: AutoResolve
	// draws cards, so a second call would decide a different game.
	//
	// MarkRefundPending is the other writer: a refund the command layer
	// could not persist fills them in before it leaves the board standing,
	// so the sweep gives the stake back instead of playing the board out.
	PendingPayout  int64
	PayoutResolved bool

	// NeedsRedraw marks a board whose last message edit never reached
	// Discord: the hand moved on, the picture in the channel did not. The
	// next press must NOT play against that picture — the player would be
	// choosing from odds, or a total, that no longer exist — so the command
	// layer redraws the board instead of taking the move and lowers the flag
	// only once the redraw has landed.
	NeedsRedraw bool

	// busy counts the operations currently running on this board (Hold /
	// Release). It starts at 1, not 0: the OPEN is the first such operation
	// (C3B-P3), and it ends when the caller that registered the board
	// Releases it. Guarded by the manager's mutex like every other mutable
	// field, and deliberately unexported: it is the manager's bookkeeping,
	// not part of the board a caller reads out of Get.
	busy int
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

// NewSessionID returns 16 crypto/rand bytes as hex. It is not a counter and
// not derived from the user: the ID travels in a custom_id that anybody can
// read off a message, so a guessable ID would let a third party address
// somebody else's board (the owner check in requireSessionOwner is the
// other half of that defence).
//
// It is exported because the command layer needs the ID BEFORE either half
// of an open exists (設計書 C-3b): the board is registered first (C3B-P2) and
// the stake Store.OpenGame then writes has to carry that same ID. Minting it
// here is what removes the window in which a stake carried a provisional mark
// that no board could present.
func NewSessionID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// Open registers a new board for guild+user. It returns ErrGameInProgress if
// that pair already has one — and because the command layer now registers the
// board BEFORE it stakes anything (C3B-P2), this refusal arrives while the
// chips have not moved, so there is nothing to hand back (see Store.OpenGame's
// ErrGameInProgress, the persisted half of the same rule, which still has the
// final say for an escrow whose board died with an earlier process).
//
// An EXPIRED board counts as one in progress: its stake is still in escrow
// until the sweeper's settlement lands, and Store.OpenGame would refuse the
// new bet anyway. The two halves of the rule must agree, or the player gets
// a board here that the store then refuses to stake.
//
// Like OpenWithID it hands back a HELD board: pair it with Release.
func (m *SessionManager) Open(guildID, userID string, game GameKind, state any, ref MessageRef) (*Session, error) {
	id, err := NewSessionID()
	if err != nil {
		return nil, err
	}
	return m.OpenWithID(id, guildID, userID, game, state, ref)
}

// OpenWithID is Open for a caller that already has the board's ID — every
// caller in the command layer, because the stake staked right after this call
// is marked with that ID (設計書 C-3b). Open is this function plus a
// NewSessionID call, and everything it promises holds here too.
//
// Registering the board is memory only: no chips move here, which is what
// makes it safe to go FIRST (C3B-P2). A caller that gets an error from this
// function has nothing to refund, so the state "an escrow exists that no
// board names" can no longer be built by an open.
//
// Returns ErrSessionIDTaken if the ID is already live. That cannot happen
// with a NewSessionID value (16 random bytes), so it is a caller passing a
// constant or reusing an ID — refusing is what stops the second caller from
// taking over the first one's board, and its stake with it.
//
// A board is born HELD (C3B-P3), exactly as if the caller had taken a Hold on
// it, and the caller MUST pair the open with Release — normally
// `defer m.Release(session.ID)` over the whole of the open. Registering and
// holding in the SAME critical section is what makes the rest of the open
// (Store.OpenGame and the first reply) run on a board the sweeper cannot take:
// the board is memory only until the stake lands, so a sweep that removed it
// in that gap cost nothing by itself, but the open then carried on and wrote
// an escrow that no board named — the one state 設計書 C-3b forbids. A Hold
// taken as a separate call after the open could not close that gap, because
// the gap is exactly where it would be taken. It also answers "what if the
// hold cannot be taken": on this path it cannot fail, because no other caller
// has ever seen the ID.
func (m *SessionManager) OpenWithID(id, guildID, userID string, game GameKind, state any, ref MessageRef) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, taken := m.sessions[id]; taken {
		return nil, ErrSessionIDTaken
	}

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
		// Held from its first instant: the open is an operation on this
		// board like any press, and Sweep skips a busy board.
		busy: 1,
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
	if !ok || session.Expired {
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
	if !ok || session.Expired {
		// Expired: the sweep goroutine owns this board and is settling it.
		// Letting fn run here would move a hand the payout was already
		// decided from, and the press would be paid out of an escrow the
		// sweeper is about to close.
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

// Hold marks a board as being operated on and returns the same bookkeeping
// copy Get does. It is the other half of "a press and the sweeper must not
// both own one board": Sweep skips a held board entirely, so the gaps a
// press unavoidably leaves outside the manager's lock — the ⏫ double stakes
// its second bet with Store.AddToEscrow, which is disk I/O and therefore
// forbidden inside WithSession — can no longer be swept into an auto-resolve
// that settles the smaller hand and drops the extra stake.
//
// The deadline test and the removal both happen under mu, and so does this,
// so a Hold that succeeds cannot be raced by the sweep that was about to
// take the board. false means the board is already gone (settled, swept, or
// lost to a restart) and the caller must answer "この盤面はもう終了しています".
//
// Every successful Hold must be paired with Release, normally via defer.
func (m *SessionManager) Hold(id string) (Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok || session.Expired {
		return Session{}, false
	}
	session.busy++
	return *session, true
}

// Release ends the operation a successful Hold started. A board that the
// press itself finished is already out of the maps, and its counter goes
// with it — IDs are 16 random bytes, so the lookup below cannot land on a
// different board.
func (m *SessionManager) Release(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, ok := m.sessions[id]; ok && session.busy > 0 {
		session.busy--
	}
}

// Touch extends a session's life without changing the board — for the
// interactions that redraw a message but take no game action. Unknown IDs
// are ignored (the session may have just been settled).
func (m *SessionManager) Touch(id string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, ok := m.sessions[id]; ok && !session.Expired {
		session.LastActionAt = now
	}
}

// SetNeedsRedraw raises or lowers the stale-picture flag of a live board.
// It is a manager method rather than a write inside WithSession because both
// of its callers sit on the OTHER side of a Discord call: the flag goes up
// when an edit has already failed, and comes down only when the redraw that
// replaced it has already succeeded. Unknown and expired boards are ignored
// — an expired board's message is the sweeper's to rewrite.
//
// It does not touch LastActionAt: raising the flag is the bot's bookkeeping,
// not the player's activity, and must not keep an abandoned board alive.
func (m *SessionManager) SetNeedsRedraw(id string, needed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, ok := m.sessions[id]; ok && !session.Expired {
		session.NeedsRedraw = needed
	}
}

// MarkRefundPending fixes the amount the idle sweep must pay for a board whose
// own refund failed, and takes the board's game out of the decision (C3B-P3).
//
// It is what lets a refund that could not be persisted stay a REFUND. The
// sweeper settles a board it takes over by asking the board to AutoResolve
// itself first, and for blackjack that STANDS the hand — a hand the player
// never saw, against a dealer who may beat it, so the retry could hand back
// nothing where the caller meant to hand back the whole stake. A marked board
// skips that: PayoutResolved is already true, so settleSweptBoard pays exactly
// amount (設計書 §5 pays PendingPayout on every retry).
//
// The caller must be holding the board — every caller is, because a refund
// runs under a Hold — which is also what makes writing these two fields safe:
// an expired board's fields belong to the sweep goroutine alone, and a held
// board cannot expire. An expired or unknown board is refused (false): it is
// somebody else's, and its payout has already been decided.
func (m *SessionManager) MarkRefundPending(id string, amount int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok || session.Expired {
		return false
	}
	session.PendingPayout = amount
	session.PayoutResolved = true
	return true
}

// Sweep marks every session idle for longer than the TTL as Expired and
// returns it, together with every board an earlier sweep already expired —
// those are the ones whose settlement failed, and the sweep interval is their
// retry interval. The boards a press is holding (Hold) are skipped.
//
// An expired board STAYS in the maps, unreachable: Get, Hold, WithSession,
// Touch and Close all refuse it, so the sweep goroutine owns it alone and may
// read and mutate its State and PendingPayout outside the lock — the same
// licence removal used to give, without the hole removal left. Removing the
// board before Store.SettleGame had persisted the payout LOST that payout and
// stranded the stake in escrow, where it also blocked every new game until
// the next restart. The caller removes the board with Remove, once the
// settlement has landed (or the store has said there is nothing left to pay).
func (m *SessionManager) Sweep(now time.Time) []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	var expired []*Session
	for _, session := range m.sessions {
		if session.Expired {
			// Handed out before and still here: its settlement did not land.
			// Hand it over again rather than wait out another TTL — the
			// player's chips are held until this board is paid.
			expired = append(expired, session)
			continue
		}
		if session.busy > 0 {
			// A press owns this board right now. Its LastActionAt is only
			// refreshed when WithSession returns, so the board can look idle
			// in the middle of an operation — sweeping it there would settle
			// a hand the press is still adding chips to.
			continue
		}
		if now.Sub(session.LastActionAt) >= m.ttl {
			// Expiring inside the same critical section as the deadline test
			// is what hands the board to exactly one caller: a press arriving
			// a moment later gets ErrSessionNotFound, not a second settlement.
			session.Expired = true
			expired = append(expired, session)
		}
	}
	return expired
}

// Remove drops a board for good. It is the sweeper's half of the expiry
// protocol — called only once Store.SettleGame has persisted the payout, or
// has reported that there is no escrow left to pay — so a board that is gone
// from here is a board whose chips have already moved. Unlike Close it takes
// an expired board too: the sweeper is the caller that owns those.
//
// Reports whether the board was still there, so a caller can tell "I removed
// it" from "somebody else already did".
func (m *SessionManager) Remove(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return false
	}
	m.removeLocked(session)
	return true
}

// Close removes a LIVE session without running the board, for the paths that
// settle outside WithSession (a cancelled /highlow, a failed Discord edit
// that leaves nothing to press). It reports whether the session was still
// live, so the caller can tell "I closed it" from "somebody else already
// did" and avoid paying twice. An expired board counts as somebody else's
// (Remove is the sweeper's door).
func (m *SessionManager) Close(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok || session.Expired {
		// An expired board belongs to the sweep until its settlement lands.
		// Reporting false is what keeps the undelivered-board refund in
		// /highlow and /blackjack from handing back a stake the sweeper is
		// about to settle — the same "somebody else owns it now" answer a
		// board that is already gone gives.
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
