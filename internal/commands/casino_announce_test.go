package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
	"github.com/bwmarrin/discordgo"
)

// announceJob builds an AnnouncementJob whose Today is history's last entry.
func announceJob(history []casino.DailyRate, top []casino.RankEntry) casino.AnnouncementJob {
	return casino.AnnouncementJob{
		GuildID:     "g1",
		ChannelID:   "c1",
		Today:       history[len(history)-1],
		RecentRates: history,
		TopAssets:   top,
	}
}

func TestBuildAnnouncementEmbed_FirstDayNoChangeLine(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	embed := buildAnnouncementEmbed(announceJob(history, nil))
	if embed.Title != "💱 本日のコインレート" {
		t.Fatalf("unexpected title: %q", embed.Title)
	}
	want := "1コイン = 100チップ\n本日が最初の記録です。\n直近1日: ▁\n凪1日目。動かない相場も相場だ。"
	if embed.Description != want {
		t.Fatalf("unexpected description:\n got: %q\nwant: %q", embed.Description, want)
	}
}

func TestBuildAnnouncementEmbed_NormalDayNoEventTag(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendBull, Event: casino.EventNone},
		{Rate: 110, Trend: casino.TrendBull, Event: casino.EventNone},
	}
	embed := buildAnnouncementEmbed(announceJob(history, nil))
	if !strings.Contains(embed.Description, "📈▲10.0%(前日比 +10)") {
		t.Fatalf("missing the day-over-day line: %q", embed.Description)
	}
	if strings.Contains(embed.Description, "🚀") || strings.Contains(embed.Description, "💥") {
		t.Fatalf("a normal day must carry no event tag: %q", embed.Description)
	}
}

func TestBuildAnnouncementEmbed_BoomDayShowsRocket(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendBull, Event: casino.EventNone},
		{Rate: 130, Trend: casino.TrendBull, Event: casino.EventSurge},
	}
	embed := buildAnnouncementEmbed(announceJob(history, nil))
	want := "1コイン = 130チップ\n🚀📈▲30.0%(前日比 +30)\n直近2日: ▁█\n🚀 暴騰デー! コインが跳ねた。売り抜けるなら今日だ。"
	if embed.Description != want {
		t.Fatalf("unexpected description:\n got: %q\nwant: %q", embed.Description, want)
	}
}

func TestBuildAnnouncementEmbed_CrashDayShowsExplosion(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendBear, Event: casino.EventNone},
		{Rate: 75, Trend: casino.TrendBear, Event: casino.EventCrash},
	}
	embed := buildAnnouncementEmbed(announceJob(history, nil))
	if !strings.Contains(embed.Description, "💥📉▼-25.0%(前日比 -25)") {
		t.Fatalf("missing the crash tag: %q", embed.Description)
	}
	if !strings.Contains(embed.Description, "💥 暴落デー!") {
		t.Fatalf("missing the crash commentary: %q", embed.Description)
	}
}

func TestBuildAnnouncementEmbed_SurgeClampedToCeiling_StillShowsRocket(t *testing.T) {
	// 130 → 140 is only +7.7% because the surge hit the clamp ceiling; the
	// persisted EventKind must still produce 🚀 (BL-5).
	history := []casino.DailyRate{
		{Rate: 130, Trend: casino.TrendBull, Event: casino.EventNone},
		{Rate: 140, Trend: casino.TrendBull, Event: casino.EventSurge},
	}
	embed := buildAnnouncementEmbed(announceJob(history, nil))
	if !strings.Contains(embed.Description, "🚀📈▲7.7%(前日比 +10)") {
		t.Fatalf("a clamped surge must still show 🚀: %q", embed.Description)
	}
}

func TestBuildAnnouncementEmbed_FlatDayShowsDash(t *testing.T) {
	// Regression: /rate and the embed share rateChangeLine, so diff == 0 must
	// render identically in both.
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone},
		{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone},
	}
	embed := buildAnnouncementEmbed(announceJob(history, nil))
	if !strings.Contains(embed.Description, "➖0.0%(前日比 +0)") {
		t.Fatalf("unexpected description for a flat day: %q", embed.Description)
	}
	if !strings.Contains(buildRateMessage(history), "➖0.0%(前日比 +0)") {
		t.Fatal("/rate and the announcement embed disagree on the flat-day rendering")
	}
}

func TestBuildAnnouncementEmbed_IncludesMarketCommentary(t *testing.T) {
	history := []casino.DailyRate{
		{Rate: 100, Trend: casino.TrendBull, Event: casino.EventNone},
		{Rate: 110, Trend: casino.TrendBull, Event: casino.EventNone},
		{Rate: 120, Trend: casino.TrendBull, Event: casino.EventNone},
	}
	embed := buildAnnouncementEmbed(announceJob(history, nil))
	if !strings.Contains(embed.Description, "コイン強気3日目。天井はどこだ?") {
		t.Fatalf("the 4th required element (相場コメント) is missing: %q", embed.Description)
	}
}

func TestBuildAnnouncementEmbed_EmptyTopAssetsShowsPlaceholder(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	embed := buildAnnouncementEmbed(announceJob(history, nil))
	if len(embed.Fields) != 2 {
		t.Fatalf("expected the ranking and the jackpot field, got %d", len(embed.Fields))
	}
	if embed.Fields[0].Name != "総資産ランキング TOP3" {
		t.Fatalf("unexpected field name: %q", embed.Fields[0].Name)
	}
	if embed.Fields[0].Value != "まだ誰も口座を持っていません。" {
		t.Fatalf("unexpected placeholder: %q", embed.Fields[0].Value)
	}
}

func TestBuildAnnouncementEmbed_TopAssetsRenderedWithMedals(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	top := []casino.RankEntry{
		{UserID: "u1", TotalAssets: 100},
		{UserID: "u2", TotalAssets: 90},
		{UserID: "u3", TotalAssets: 80},
		{UserID: "u4", TotalAssets: 70},
	}
	embed := buildAnnouncementEmbed(announceJob(history, top))
	if len(embed.Fields) != 2 {
		t.Fatalf("expected the ranking and the jackpot field, got %d", len(embed.Fields))
	}
	want := "🥇 <@u1> — 100\n🥈 <@u2> — 90\n🥉 <@u3> — 80\n4. <@u4> — 70"
	if embed.Fields[0].Value != want {
		t.Fatalf("unexpected ranking:\n got: %q\nwant: %q", embed.Fields[0].Value, want)
	}
}

func TestStartAnnounceScheduler_WaitBlocksUntilCallbackFinishes(t *testing.T) {
	// SF-3: the caller must be able to wait for an in-flight send before
	// closing the discordgo session.
	store := newTestCasinoStore(t)
	if err := store.SetAnnounceChannel("g1", "c1"); err != nil {
		t.Fatalf("SetAnnounceChannel: %v", err)
	}
	// FIXED clock at 10:00 JST: announceWindowOpen is true no matter what
	// time of day the suite runs, and the stub sleeper never waits on a real
	// timer, so this test is neither time-of-day dependent nor slow.
	now := func() time.Time { return time.Date(2026, 7, 10, 10, 0, 0, 0, time.FixedZone("JST", 9*3600)) }
	sleep := func(ctx context.Context, until time.Time) bool { return false }

	entered := make(chan struct{})
	release := make(chan struct{})
	var gotChannelID string
	var gotEmbed *discordgo.MessageEmbed
	send := func(channelID string, embed *discordgo.MessageEmbed) error {
		gotChannelID, gotEmbed = channelID, embed
		close(entered)
		<-release
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wait := startAnnounceScheduler(ctx, store, send, now, sleep)
	waitDone := make(chan struct{})
	go func() { wait(); close(waitDone) }()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("the send callback was never invoked for the configured guild")
	}

	cancel()
	select {
	case <-waitDone:
		close(release)
		t.Fatal("wait() returned while the send callback was still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("wait() did not return after the send callback finished")
	}

	if gotChannelID != "c1" {
		t.Fatalf("announcement posted to %q, want the configured channel", gotChannelID)
	}
	if gotEmbed == nil || gotEmbed.Title != "💱 本日のコインレート" {
		t.Fatalf("unexpected embed: %+v", gotEmbed)
	}
}

// 9 時掲示はプールが育つのを見せる場所(設計書 C-3a §2)。額は job から来る
// — この関数は純粋なままで、ストアを読み直さない。
func TestBuildAnnouncementEmbed_ShowsJackpotPool(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	job := announceJob(history, nil)
	job.JackpotPool = 12345
	embed := buildAnnouncementEmbed(job)
	if len(embed.Fields) != 2 {
		t.Fatalf("expected the ranking and the jackpot field, got %d", len(embed.Fields))
	}
	field := embed.Fields[1]
	if field.Name != "🎰 ジャックポット" {
		t.Fatalf("unexpected field name: %q", field.Name)
	}
	if field.Value != "12345 チップ" {
		t.Fatalf("unexpected jackpot value: %q", field.Value)
	}
}

func TestBuildAnnouncementEmbed_SeededJackpotPoolIsShownAsIs(t *testing.T) {
	// 種(1,000)のギルドでも 0 ではなく実額が出ること。
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	job := announceJob(history, nil)
	job.JackpotPool = casino.JackpotSeed
	embed := buildAnnouncementEmbed(job)
	if got := embed.Fields[1].Value; got != "1000 チップ" {
		t.Fatalf("unexpected jackpot value: %q", got)
	}
}
