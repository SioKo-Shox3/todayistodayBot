package casino

// TrendState is a guild's hidden day-to-day rate momentum ("隠れトレンド").
type TrendState string

const (
	TrendBull TrendState = "bull"
	TrendBear TrendState = "bear"
	TrendFlat TrendState = "flat"
)

// EventKind records whether a day's rate move came from the 5% 暴騰/暴落
// draw (設計書 §日次レート) rather than the normal drift+noise path. It is
// PERSISTED rather than re-derived from the day-over-day percentage,
// because the 70/140 clamp can swallow the evidence: a +15% surge from 130
// clamps to 140, i.e. only +7.7% day-over-day, so a threshold-based guess
// would silently drop 🚀 on a genuine surge day (計算: 130*1.15 = 149.5 →
// clamp 140 → (140-130)/130 = 7.69% < 10%). Display code must read this
// field, never guess.
type EventKind string

const (
	EventNone  EventKind = "none"
	EventSurge EventKind = "surge" // 🚀 +15..30%
	EventCrash EventKind = "crash" // 💥 -15..30%
)

// Data is the root persisted structure: guild ID -> that guild's economy
// state. Top-level map, exactly as specified by the design doc.
type Data map[string]*GuildEconomy

// GuildEconomy is one guild's entire casino state: where the 9am
// announcement goes, the recent daily-rate history, the last announced JST
// date, and every account in that guild.
// The jackpot pair is deliberately NOT omitempty: 0 is the one value that
// carries meaning — "this guild has never been seeded" — and it is exactly
// what a pre-C-3a data/casino.json unmarshals to. seedJackpotLocked turns
// that 0 into JackpotSeed at the first spin or daily-rate generation, so an
// omitempty that erased a genuine 0 would be indistinguishable from a pool
// that had just been won and reset (also JackpotSeed, never 0).
type GuildEconomy struct {
	AnnounceChannelID string                  `json:"announce_channel_id"`
	Rates             []DailyRate             `json:"rates"`          // oldest..newest, capped at 30 entries
	LastAnnounced     string                  `json:"last_announced"` // JST calendar date "2026-07-10"
	Jackpot           int64                   `json:"jackpot"`        // slot jackpot pool in chips; >= JackpotSeed once seeded
	JackpotAccum      int64                   `json:"jackpot_accum"`  // carry of the 2% accrual, in 1/100 chips; always [0, 100)
	Lottery           Lottery                 `json:"lottery"`        // the daily 9:00 JST draw's pot (設計書 C-3a §3)
	Users             map[string]*UserAccount `json:"users"`
}

// Lottery is one guild's daily-draw state: the pot being sold into right
// now, plus when it was last drawn and what that draw produced.
//
// It is a VALUE, not a pointer: a nil *Lottery would have to be repaired at
// every one of the read paths that touch it (every command goes through the
// daily rollover), and a pre-C-3a data/casino.json — which has no "lottery"
// key at all — unmarshals to exactly the right zero: DrawDate "" (never
// drawn), no tickets, no sales, no carryover. The first rollover after the
// upgrade therefore performs a buyer-less draw that only stamps DrawDate,
// which is the intended behaviour, not a migration step.
//
// Tickets is nil until someone buys; every reader must treat a nil map as
// "no buyers" rather than indexing a repaired copy (reading a nil map is
// legal in Go — only assignment panics, and only BuyLotteryTickets assigns).
type Lottery struct {
	DrawDate  string         `json:"draw_date"` // JST date of the last draw; "" = never drawn
	Tickets   map[string]int `json:"tickets"`   // userID -> tickets held for the NEXT draw
	Sales     int64          `json:"sales"`     // chips taken in for the NEXT draw
	Carryover int64          `json:"carryover"` // prize rolled forward from draws nobody entered
	// LastDraw is the most recent draw that actually had a winner — it is
	// omitempty and left untouched by a buyer-less draw, so /lottery status
	// keeps showing the last real result instead of blanking out on the first
	// quiet day.
	LastDraw *LotteryDraw `json:"last_draw,omitempty"`
	// Unannounced is every drawn-with-a-winner result that the 9am posting
	// has not delivered yet, oldest first. LastDraw answers 「直近の結果は」
	// for /lottery status and is therefore a single slot; this queue answers
	// 「まだ知らせていない回は」, which is NOT the same question. A bot that
	// is down at 09:00 settles the missed day at startup and can settle the
	// NEXT day before the posting goes out, and a single slot would let the
	// second draw overwrite the first — the missed winner would never be
	// announced or celebrated at all.
	//
	// Entries are appended by drawLotteryLocked and removed by MarkAnnounced,
	// i.e. only by a CONFIRMED send: a failed posting leaves the queue intact
	// and the next pass retries it. The length is capped at
	// lotteryUnannouncedLimit so an announce channel that is broken for
	// months cannot grow the file without bound — the prize was already paid
	// at draw time, so a dropped entry costs the announcement, never chips.
	//
	// omitempty + a nil zero value is what makes a pre-C3-12 casino.json
	// (which has no "unannounced" key) read back as an empty queue.
	Unannounced []LotteryDraw `json:"unannounced,omitempty"`
}

// LotteryDraw is one completed draw's result, kept for the 9am announcement
// and /lottery status. Prize is what the winner was actually CREDITED, which
// is the number both of those render.
type LotteryDraw struct {
	Date        string `json:"date"` // JST date the draw ran
	WinnerID    string `json:"winner_id"`
	Prize       int64  `json:"prize"`
	TicketsSold int    `json:"tickets_sold"`
	Buyers      int    `json:"buyers"`
}

// DailyRate is one JST calendar day's coin<->chip exchange rate, together
// with the hidden trend it was drawn under and whether it came from a
// 暴騰/暴落 event.
type DailyRate struct {
	Date  string     `json:"date"` // JST calendar date "2026-07-10"
	Rate  int        `json:"rate"`
	Trend TrendState `json:"trend"`
	Event EventKind  `json:"event"` // "" (legacy/hand-edited records) is treated as EventNone by all readers
}

// UserAccount is one user's balance in one guild, plus the bookkeeping the
// daily bonus streak needs and the chips currently held by an in-flight
// button game (設計書 §3).
//
// The escrow trio is omitempty so an account with no game in flight
// serializes exactly as it did before this field existed, and — the
// direction that actually matters — a data/casino.json written before C-2
// unmarshals with Escrow 0 / EscrowGame "" / EscrowOpenedAt "", i.e. "no
// game in progress". No migration step exists or is needed.
//
// Escrow is NOT a separate pot of money: OpenGame/AddToEscrow move chips
// out of Chips into Escrow and SettleGame moves them back, so the user's
// holdings are Chips + Escrow throughout. Every total-assets computation
// must therefore add Escrow, or a player would appear to lose their bet
// from the ranking for the duration of a hand.
type UserAccount struct {
	Coins          int64  `json:"coins"`
	Chips          int64  `json:"chips"`
	LastDailyDate  string `json:"last_daily_date"`
	StreakDays     int    `json:"streak_days"`
	Escrow         int64  `json:"escrow,omitempty"`           // chips staked on the in-flight game (bet + any double)
	EscrowGame     string `json:"escrow_game,omitempty"`      // "highlow" | "blackjack"
	EscrowOpenedAt string `json:"escrow_opened_at,omitempty"` // RFC3339 in JST
}

// MaxChips / MaxCoins bound every account balance. They are NOT arbitrary:
// they are chosen so that the widest intermediate this package ever
// computes stays far inside int64. Total assets peak at
// MaxChips + MaxCoins*140 = 1.41e14 (the 140 is the rate clamp ceiling) and
// the exchange intermediate at MaxCoins*140*97 = 1.358e16, i.e. 65,413x and
// 679x below math.MaxInt64 (9.223e18) respectively. As long as every credit
// path enforces these caps, no arithmetic in this package can wrap.
//
// MaxJackpot bounds the slot jackpot pool by the same reasoning, and the
// same value: the pool is house money rather than an account balance, but
// an unbounded pool is the one place left where a hand-edited casino.json
// could wrap int64 (the house's lottery cut and the slot accrual both add
// to it every day and nothing ever subtracted an upper bound). Because
// seedJackpotLocked pins the pool into [JackpotSeed, MaxJackpot] at every
// read, the headroom MaxJackpot-Jackpot is itself in [0, 1e12] and cannot
// wrap, and creditJackpotCappedLocked truncates every credit to that
// headroom BEFORE adding. So the pool stays in range whatever the addend
// is — even one that has already wrapped somewhere upstream. The overflow
// is not merely unlikely here, it is unreachable.
const (
	MaxChips   int64 = 1_000_000_000_000 // 1e12
	MaxCoins   int64 = 1_000_000_000_000 // 1e12
	MaxJackpot int64 = MaxChips          // the slot jackpot pool obeys the account cap
)

// randSource is the minimal randomness surface economy.go and slot.go need
// injected for deterministic tests. *rand.Rand (via rand.New) satisfies it
// structurally; production code always constructs a real *rand.Rand (never
// uses the global math/rand top-level functions, for reproducibility).
// Defined HERE (types.go), not in economy.go, because Store embeds it.
type randSource interface {
	Float64() float64
	Intn(n int) int
}
