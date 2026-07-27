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
type GuildEconomy struct {
	AnnounceChannelID string                  `json:"announce_channel_id"`
	Rates             []DailyRate             `json:"rates"`          // oldest..newest, capped at 30 entries
	LastAnnounced     string                  `json:"last_announced"` // JST calendar date "2026-07-10"
	Users             map[string]*UserAccount `json:"users"`
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
// daily bonus streak needs.
type UserAccount struct {
	Coins         int64  `json:"coins"`
	Chips         int64  `json:"chips"`
	LastDailyDate string `json:"last_daily_date"`
	StreakDays    int    `json:"streak_days"`
}

// MaxChips / MaxCoins bound every account balance. They are NOT arbitrary:
// they are chosen so that the widest intermediate this package ever
// computes stays far inside int64. Total assets peak at
// MaxChips + MaxCoins*140 = 1.41e14 (the 140 is the rate clamp ceiling) and
// the exchange intermediate at MaxCoins*140*97 = 1.358e16, i.e. 65,413x and
// 679x below math.MaxInt64 (9.223e18) respectively. As long as every credit
// path enforces these caps, no arithmetic in this package can wrap.
const (
	MaxChips int64 = 1_000_000_000_000 // 1e12
	MaxCoins int64 = 1_000_000_000_000 // 1e12
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
