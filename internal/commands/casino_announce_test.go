package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if len(embed.Fields) != 4 {
		t.Fatalf("expected the ranking, jackpot, lottery and season fields, got %d", len(embed.Fields))
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
	if len(embed.Fields) != 4 {
		t.Fatalf("expected the ranking, jackpot, lottery and season fields, got %d", len(embed.Fields))
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
	sendText := func(channelID, content string) error {
		t.Errorf("no lottery was drawn, so no celebration must be posted; got %q to %q", content, channelID)
		return nil
	}
	wait := startAnnounceScheduler(ctx, store, send, sendText, now, sleep)
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
	if len(embed.Fields) != 4 {
		t.Fatalf("expected the ranking, jackpot, lottery and season fields, got %d", len(embed.Fields))
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

// lotteryField returns the 🎟️ 宝くじ field, failing if the embed does not
// carry it — 設計書 C-3a §3 makes it a required part of the 9am posting.
func lotteryField(t *testing.T, embed *discordgo.MessageEmbed) *discordgo.MessageEmbedField {
	t.Helper()
	for _, f := range embed.Fields {
		if f.Name == "🎟️ 宝くじ" {
			return f
		}
	}
	t.Fatalf("the embed carries no 🎟️ 宝くじ field: %+v", embed.Fields)
	return nil
}

func TestBuildAnnouncementEmbed_LotteryFieldShowsTheWinnerUnderTheDrawsOwnDate(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	job := announceJob(history, nil)
	job.LotteryDraws = []casino.LotteryDraw{{Date: "2026-07-10", WinnerID: "u1", Prize: 900, TicketsSold: 20, Buyers: 3}}
	// The pot the draw just emptied and reopened reads 0/0 on a normal morning.
	embed := buildAnnouncementEmbed(job)
	want := "🏆 7/10 の当選: <@u1> が 900チップ 獲得(20枚 / 3人)\n💰 本日の賞金: 0チップ / 🎫 売れた枚数: 0枚"
	if got := lotteryField(t, embed).Value; got != want {
		t.Fatalf("unexpected lottery field:\n got: %q\nwant: %q", got, want)
	}
}

// TestBuildAnnouncementEmbed_LotteryFieldDatesACaughtUpDraw covers the job a
// restart produces after the bot missed 09:00: the draw it carries is NOT
// yesterday's relative to the posting, so the heading must name the day the
// draw actually settled instead of claiming 「昨日」.
func TestBuildAnnouncementEmbed_LotteryFieldDatesACaughtUpDraw(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	job := announceJob(history, nil)
	job.LotteryDraws = []casino.LotteryDraw{{Date: "2026-09-03", WinnerID: "u1", Prize: 900, TicketsSold: 20, Buyers: 3}}
	job.LotteryPrize, job.LotteryTickets = 120, 1
	embed := buildAnnouncementEmbed(job)
	want := "🏆 9/3 の当選: <@u1> が 900チップ 獲得(20枚 / 3人)"
	if got := lotteryField(t, embed).Value; !strings.Contains(got, want) {
		t.Fatalf("unexpected lottery field:\n got: %q\nwant it to contain: %q", got, want)
	}
}

func TestBuildAnnouncementEmbed_LotteryFieldNoWinnerRollsOver(t *testing.T) {
	// 購入者 0 の日: drawLotteryLocked leaves LastDraw untouched (nil here)
	// and rolls the whole prize forward, so the field must say 繰り越し and
	// still show the carried-over amount against 0 tickets.
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	job := announceJob(history, nil)
	job.LotteryDraws = nil
	job.LotteryPrize, job.LotteryTickets = 500, 0
	embed := buildAnnouncementEmbed(job)
	want := "🏆 前回の当選: 該当者なし — 賞金は繰り越し\n💰 本日の賞金: 500チップ / 🎫 売れた枚数: 0枚"
	if got := lotteryField(t, embed).Value; got != want {
		t.Fatalf("unexpected lottery field:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildAnnouncementEmbed_LotteryFieldShowsTodaysOpenPot(t *testing.T) {
	// A LATE catch-up posting (a 23:00 restart, announce.go's "no upper
	// bound"): people have already bought into the pot that reopened this
	// morning, so the second line must report those live numbers rather than
	// the 0/0 a punctual 09:00 posting shows.
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	job := announceJob(history, nil)
	job.LotteryDraws = []casino.LotteryDraw{{Date: "2026-07-10", WinnerID: "u1", Prize: 900, TicketsSold: 20, Buyers: 3}}
	job.LotteryPrize, job.LotteryTickets = 315, 7
	got := lotteryField(t, buildAnnouncementEmbed(job)).Value
	if !strings.Contains(got, "💰 本日の賞金: 315チップ / 🎫 売れた枚数: 7枚") {
		t.Fatalf("the open pot is not reported: %q", got)
	}
}

func TestLotteryAnnounceCelebration_OnlyForAnActualWinner(t *testing.T) {
	cases := []struct {
		name string
		draw *casino.LotteryDraw
		want string
	}{
		{"nobody entered", nil, ""},
		{"no winner named", &casino.LotteryDraw{Date: "2026-07-10", WinnerID: "", Prize: 900}, ""},
		// A winner already at MaxChips is credited nothing by
		// creditChipsCappedLocked and the remainder goes to the jackpot pool,
		// not to the next draw, but the draw still named them: the ping is
		// owed either way.
		{
			"winner credited nothing",
			&casino.LotteryDraw{Date: "2026-07-10", WinnerID: "u1", Prize: 0},
			"🎉🎉🎉 <@u1> が 🎟️ 宝くじに当選!! 0 チップ 獲得!! 🎉🎉🎉",
		},
		{
			"real winner",
			&casino.LotteryDraw{Date: "2026-07-10", WinnerID: "u1", Prize: 900, TicketsSold: 20, Buyers: 3},
			"🎉🎉🎉 <@u1> が 🎟️ 宝くじに当選!! 900 チップ 獲得!! 🎉🎉🎉",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lotteryAnnounceCelebration(tc.draw); got != tc.want {
				t.Fatalf("lotteryAnnounceCelebration() = %q, want %q", got, tc.want)
			}
		})
	}
}

// runOneAnnouncePass drives startAnnounceScheduler through exactly ONE pass
// at `at` and returns everything the two fake senders recorded. The stub
// sleeper returns false, which stops the scheduler after that single pass, so
// wait() returning is also the happens-before edge that makes reading the
// recorded slices race-free.
func runOneAnnouncePass(t *testing.T, store *casino.Store, at time.Time) (embeds []*discordgo.MessageEmbed, texts, channels []string) {
	t.Helper()
	now := func() time.Time { return at }
	sleep := func(ctx context.Context, until time.Time) bool { return false }
	send := func(channelID string, embed *discordgo.MessageEmbed) error {
		embeds = append(embeds, embed)
		return nil
	}
	sendText := func(channelID, content string) error {
		texts = append(texts, content)
		channels = append(channels, channelID)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startAnnounceScheduler(ctx, store, send, sendText, now, sleep)()
	return embeds, texts, channels
}

// announceAt10 is 10:00 JST on 2026-07-10 — past runAnnouncePass's 09:00
// lower bound, so a pass driven with it posts whatever time of day the suite
// itself runs at.
func announceAt10() time.Time {
	return time.Date(2026, 7, 10, 10, 0, 0, 0, time.FixedZone("JST", 9*3600))
}

func TestStartAnnounceScheduler_CelebratesTheLotteryWinnerInASeparateMessage(t *testing.T) {
	// 設計書 C-3a §3: a won draw gets a public, @mentioning message of its
	// own — the same treatment slot.go gives a jackpot — not merely an embed
	// field. Driven through a REAL store, so the draw under test is the one
	// this morning's rollover actually settled.
	store := newTestCasinoStore(t)
	if err := store.SetAnnounceChannel("g1", "c1"); err != nil {
		t.Fatalf("SetAnnounceChannel: %v", err)
	}
	at := announceAt10()
	// Bought the day before, so this morning's 09:00 draw settles it. A
	// single buyer wins whatever the rng rolls (PickLotteryWinner walks a
	// one-entry cumulative sum), so this does not depend on the store's rng.
	if _, err := store.BuyLotteryTickets("g1", "u1", 2, at.AddDate(0, 0, -1)); err != nil {
		t.Fatalf("BuyLotteryTickets: %v", err)
	}

	embeds, texts, channels := runOneAnnouncePass(t, store, at)

	if len(embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(embeds))
	}
	if got := lotteryField(t, embeds[0]).Value; !strings.Contains(got, "🏆 7/10 の当選: <@u1> が 90チップ 獲得(2枚 / 1人)") {
		t.Fatalf("the embed field does not report the settled draw: %q", got)
	}
	if len(texts) != 1 {
		t.Fatalf("got %d celebration messages, want exactly 1: %q", len(texts), texts)
	}
	if want := "🎉🎉🎉 <@u1> が 🎟️ 宝くじに当選!! 90 チップ 獲得!! 🎉🎉🎉"; texts[0] != want {
		t.Fatalf("unexpected celebration:\n got: %q\nwant: %q", texts[0], want)
	}
	if channels[0] != "c1" {
		t.Fatalf("the celebration went to %q, want the announce channel", channels[0])
	}
}

func TestStartAnnounceScheduler_NoWinnerPostsNoCelebration(t *testing.T) {
	// Nobody bought a ticket: the embed still goes up (with the 繰り越し
	// wording) and not one extra message is posted.
	store := newTestCasinoStore(t)
	if err := store.SetAnnounceChannel("g1", "c1"); err != nil {
		t.Fatalf("SetAnnounceChannel: %v", err)
	}

	embeds, texts, _ := runOneAnnouncePass(t, store, announceAt10())

	if len(embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(embeds))
	}
	if got := lotteryField(t, embeds[0]).Value; !strings.Contains(got, "該当者なし") {
		t.Fatalf("a drawless morning must say so: %q", got)
	}
	if len(texts) != 0 {
		t.Fatalf("no winner, yet %d celebration(s) were posted: %q", len(texts), texts)
	}
}

func TestStartAnnounceScheduler_CelebrationFailureStillMarksTheDayAnnounced(t *testing.T) {
	// 「送信失敗はログのみで掲示と抽選には影響しない」: a celebration that
	// cannot be delivered must not undo the posting. Were the job failed
	// instead, LastAnnounced would stay unset and the SECOND pass below would
	// post the whole embed all over again.
	store := newTestCasinoStore(t)
	if err := store.SetAnnounceChannel("g1", "c1"); err != nil {
		t.Fatalf("SetAnnounceChannel: %v", err)
	}
	at := announceAt10()
	if _, err := store.BuyLotteryTickets("g1", "u1", 2, at.AddDate(0, 0, -1)); err != nil {
		t.Fatalf("BuyLotteryTickets: %v", err)
	}

	now := func() time.Time { return at }
	sleep := func(ctx context.Context, until time.Time) bool { return false }
	var embeds, attempts int
	send := func(channelID string, embed *discordgo.MessageEmbed) error { embeds++; return nil }
	sendText := func(channelID, content string) error {
		attempts++
		return errors.New("discord is down")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startAnnounceScheduler(ctx, store, send, sendText, now, sleep)()
	if embeds != 1 || attempts != 1 {
		t.Fatalf("first pass: %d embeds / %d celebration attempts, want 1/1", embeds, attempts)
	}

	// A second pass on the same day must find the guild already announced.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	startAnnounceScheduler(ctx2, store, send, sendText, now, sleep)()
	if embeds != 1 {
		t.Fatalf("the failed celebration re-opened the day: %d embeds posted in total, want 1", embeds)
	}
}

func TestStartAnnounceScheduler_CelebratesAWinnerWhoWasCreditedNothing(t *testing.T) {
	// The draw pays through creditChipsCappedLocked, so a winner sitting at
	// MaxChips is recorded with Prize 0 and the unpaid prize goes to the
	// jackpot pool, not to the next draw. The draw still named them, so the
	// @mentioning message is still posted — the amount it prints is the 0
	// that was actually credited.
	store := newTestCasinoStore(t)
	if err := store.SetAnnounceChannel("g1", "c1"); err != nil {
		t.Fatalf("SetAnnounceChannel: %v", err)
	}
	at := announceAt10()
	// A single buyer wins whatever the rng rolls, so this does not depend on
	// the store's rng (see the sibling test).
	if _, err := store.BuyLotteryTickets("g1", "u1", 2, at.AddDate(0, 0, -1)); err != nil {
		t.Fatalf("BuyLotteryTickets: %v", err)
	}
	// Fill the account to the cap AFTER buying: the purchase itself has to
	// be affordable, and only the credit at draw time must hit the ceiling.
	if err := store.Update(func(d *casino.Data) error {
		(*d)["g1"].Users["u1"].Chips = casino.MaxChips
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	_, texts, channels := runOneAnnouncePass(t, store, at)

	if len(texts) != 1 {
		t.Fatalf("got %d celebration messages, want exactly 1: %q", len(texts), texts)
	}
	if want := "🎉🎉🎉 <@u1> が 🎟️ 宝くじに当選!! 0 チップ 獲得!! 🎉🎉🎉"; texts[0] != want {
		t.Fatalf("unexpected celebration:\n got: %q\nwant: %q", texts[0], want)
	}
	if channels[0] != "c1" {
		t.Fatalf("the celebration went to %q, want the announce channel", channels[0])
	}
}

// TestBuildAnnouncementEmbed_LotteryFieldListsEveryQueuedDraw is the embed
// half of 反復 2 の指摘: when an outage left one morning unposted, the next
// posting owes the channel BOTH winners, each under its own date. One line per
// draw, then the open pot.
func TestBuildAnnouncementEmbed_LotteryFieldListsEveryQueuedDraw(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	job := announceJob(history, nil)
	job.LotteryDraws = []casino.LotteryDraw{
		{Date: "2026-09-13", WinnerID: "u1", Prize: 900, TicketsSold: 20, Buyers: 3},
		{Date: "2026-09-14", WinnerID: "u2", Prize: 450, TicketsSold: 10, Buyers: 2},
	}
	job.LotteryPrize, job.LotteryTickets = 120, 1
	want := strings.Join([]string{
		"🏆 9/13 の当選: <@u1> が 900チップ 獲得(20枚 / 3人)",
		"🏆 9/14 の当選: <@u2> が 450チップ 獲得(10枚 / 2人)",
		"💰 本日の賞金: 120チップ / 🎫 売れた枚数: 1枚",
	}, "\n")
	if got := lotteryField(t, buildAnnouncementEmbed(job)).Value; got != want {
		t.Fatalf("unexpected lottery field:\n got: %q\nwant: %q", got, want)
	}
}

// TestStartAnnounceScheduler_CelebratesEveryQueuedWinner is the celebration
// half: two winners were queued, so two @mentioning messages go out. Driven
// through a REAL store so the queue under test is the one the rollovers
// actually built.
func TestStartAnnounceScheduler_CelebratesEveryQueuedWinner(t *testing.T) {
	store := newTestCasinoStore(t)
	if err := store.SetAnnounceChannel("g1", "c1"); err != nil {
		t.Fatalf("SetAnnounceChannel: %v", err)
	}
	at := announceAt10()
	// u1 buys the day before: the 7/10 09:00 draw is theirs. u2 buys at 7/10
	// 10:00, whose rollover settles that draw WITHOUT anybody announcing it
	// (the outage), and joins the 7/11 pot.
	if _, err := store.BuyLotteryTickets("g1", "u1", 2, at.AddDate(0, 0, -1)); err != nil {
		t.Fatalf("BuyLotteryTickets(u1): %v", err)
	}
	if _, err := store.BuyLotteryTickets("g1", "u2", 2, at); err != nil {
		t.Fatalf("BuyLotteryTickets(u2): %v", err)
	}

	embeds, texts, channels := runOneAnnouncePass(t, store, at.AddDate(0, 0, 1))

	if len(embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(embeds))
	}
	field := lotteryField(t, embeds[0]).Value
	for _, want := range []string{
		"🏆 7/10 の当選: <@u1> が 90チップ 獲得(2枚 / 1人)",
		"🏆 7/11 の当選: <@u2> が 90チップ 獲得(2枚 / 1人)",
	} {
		if !strings.Contains(field, want) {
			t.Fatalf("the embed field does not report every queued draw:\n got: %q\nwant it to contain: %q", field, want)
		}
	}
	want := []string{
		"🎉🎉🎉 <@u1> が 🎟️ 宝くじに当選!! 90 チップ 獲得!! 🎉🎉🎉",
		"🎉🎉🎉 <@u2> が 🎟️ 宝くじに当選!! 90 チップ 獲得!! 🎉🎉🎉",
	}
	if len(texts) != len(want) {
		t.Fatalf("got %d celebration messages, want %d — every queued winner is owed their ping: %q", len(texts), len(want), texts)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("celebration %d:\n got: %q\nwant: %q", i, texts[i], want[i])
		}
		if channels[i] != "c1" {
			t.Fatalf("celebration %d went to %q, want the announce channel", i, channels[i])
		}
	}
}

// --- 🏆 シーズン(今月) と結果の祝い (設計書 C-3b §4 掲示) -------------------

func seasonField(t *testing.T, embed *discordgo.MessageEmbed) *discordgo.MessageEmbedField {
	t.Helper()
	for _, f := range embed.Fields {
		if f.Name == "🏆 シーズン(今月)" {
			return f
		}
	}
	t.Fatalf("the embed carries no 🏆 シーズン(今月) field: %+v", embed.Fields)
	return nil
}

func TestBuildAnnouncementEmbed_SeasonFieldShowsThePodiumWithMedals(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	job := announceJob(history, nil)
	job.SeasonMonth = "2026-07"
	// A loser on the podium is not a rendering accident: in a month where
	// everybody lost, somebody lost the least, and the sign is what keeps
	// that readable.
	job.SeasonTop = []casino.SeasonRank{
		{UserID: "u1", Net: 1_200}, {UserID: "u2", Net: 800}, {UserID: "u3", Net: -50},
	}

	got := seasonField(t, buildAnnouncementEmbed(job)).Value

	want := "2026-07 の純利ランキング\n🥇 <@u1> — 純利 +1200\n🥈 <@u2> — 純利 +800\n🥉 <@u3> — 純利 -50"
	if got != want {
		t.Fatalf("season field =\n%q\nwant\n%q", got, want)
	}
}

// A month nobody has played is the ordinary state of the 1st, not a broken
// table, and it says so in the same words /season uses.
func TestBuildAnnouncementEmbed_SeasonFieldEmptyMonth(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	job := announceJob(history, nil)
	job.SeasonMonth = "2026-08"

	got := seasonField(t, buildAnnouncementEmbed(job)).Value

	want := "2026-08 — まだ誰も勝敗を記録していません。"
	if got != want {
		t.Fatalf("season field = %q, want %q", got, want)
	}
}

func TestSeasonAnnounceCelebration_OnlyForAClosedSeasonWithAPodium(t *testing.T) {
	podium := []casino.SeasonRank{
		{UserID: "u1", Net: 5_000, Bonus: 10_000},
		{UserID: "u2", Net: 3_000, Bonus: 5_000},
		{UserID: "u3", Net: 1_000, Bonus: 2_500},
	}
	cases := []struct {
		name   string
		result *casino.SeasonResult
		want   string
	}{
		{"no season has closed", nil, ""},
		{"a month nobody played", &casino.SeasonResult{Month: "2026-06"}, ""},
		{
			"the podium",
			&casino.SeasonResult{Month: "2026-06", Ranks: podium, Players: 7},
			"🎉🎉🎉 2026-06 のシーズンが終了!! 🎉🎉🎉\n" +
				"🥇 <@u1> — 純利 +5000 / 賞与 10000チップ\n" +
				"🥈 <@u2> — 純利 +3000 / 賞与 5000チップ\n" +
				"🥉 <@u3> — 純利 +1000 / 賞与 2500チップ\n" +
				"参加者: 7人 — 新しいシーズンが始まりました!",
		},
		{
			// A winner already at the chip cap was credited less than the
			// table offers, and the message prints what landed.
			"a winner credited nothing",
			&casino.SeasonResult{Month: "2026-06", Ranks: []casino.SeasonRank{{UserID: "u1", Net: 5_000, Bonus: 0}}, Players: 1},
			"🎉🎉🎉 2026-06 のシーズンが終了!! 🎉🎉🎉\n" +
				"🥇 <@u1> — 純利 +5000 / 賞与 0チップ\n" +
				"参加者: 1人 — 新しいシーズンが始まりました!",
		},
		{
			// A hand-edited last_season with more rows than the rollover
			// ever writes must not become a wall of mentions.
			"a hand-edited overlong podium",
			&casino.SeasonResult{Month: "2026-06", Ranks: append(append([]casino.SeasonRank(nil), podium...),
				casino.SeasonRank{UserID: "u4", Net: 900, Bonus: 999}), Players: 9},
			"🎉🎉🎉 2026-06 のシーズンが終了!! 🎉🎉🎉\n" +
				"🥇 <@u1> — 純利 +5000 / 賞与 10000チップ\n" +
				"🥈 <@u2> — 純利 +3000 / 賞与 5000チップ\n" +
				"🥉 <@u3> — 純利 +1000 / 賞与 2500チップ\n" +
				"参加者: 9人 — 新しいシーズンが始まりました!",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := seasonAnnounceCelebration(tc.result); got != tc.want {
				t.Fatalf("seasonAnnounceCelebration() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// seededCasinoStore opens a store on a casino.json written by hand. A CLOSED
// season only exists once the month has moved, and internal/commands cannot
// reach the store's clock to move it (the rate/season rollover reads it under
// the lock), so the file is the seam these wiring tests seed through — the
// same way oldFormatStore seeds a pre-queue lottery file in internal/casino.
func seededCasinoStore(t *testing.T, content string) *casino.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "casino.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the seed file: %v", err)
	}
	return casino.New(path)
}

// season_month is JULY, so a pass driven at 2026-07-10 does not roll the
// month again: what is under test is the JUNE result already on file, and
// last_season_announced being absent is what makes it pending.
const closedJuneSeasonFile = `{"g1":{"announce_channel_id":"c1","rates":[],"last_announced":"",` +
	`"season_month":"2026-07","last_season":{"month":"2026-06","players":7,"ranks":[` +
	`{"user_id":"u1","net":5000,"bonus":10000},{"user_id":"u2","net":3000,"bonus":5000},` +
	`{"user_id":"u3","net":1000,"bonus":2500}]},"users":{}}}`

func TestStartAnnounceScheduler_CelebratesAClosedSeasonInASeparateMessage(t *testing.T) {
	// 設計書 C-3b §4 掲示: the closed month's result is its own public
	// message, so the three winners are actually pinged.
	store := seededCasinoStore(t, closedJuneSeasonFile)

	embeds, texts, channels := runOneAnnouncePass(t, store, announceAt10())

	if len(embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(embeds))
	}
	if len(texts) != 1 {
		t.Fatalf("got %d separate messages, want 1 (the season result): %q", len(texts), texts)
	}
	if channels[0] != "c1" {
		t.Errorf("the result went to %q, want the announcement channel c1", channels[0])
	}
	for _, want := range []string{"2026-06", "<@u1>", "<@u2>", "<@u3>", "賞与 10000チップ", "参加者: 7人"} {
		if !strings.Contains(texts[0], want) {
			t.Errorf("the season result message %q is missing %q", texts[0], want)
		}
	}
	// The embed's field describes the RUNNING month, which the closed one
	// zeroed — the two must not be confused for each other.
	if got := seasonField(t, embeds[0]).Value; got != "2026-07 — まだ誰も勝敗を記録していません。" {
		t.Errorf("season field = %q, want the freshly opened month", got)
	}
}

// 結果の祝いは閉じた回だけ: a month whose result has already been posted is
// not posted again, however many mornings go by.
func TestStartAnnounceScheduler_AnAlreadyAnnouncedSeasonIsNotCelebratedAgain(t *testing.T) {
	store := seededCasinoStore(t, strings.Replace(closedJuneSeasonFile,
		`"season_month":"2026-07"`, `"season_month":"2026-07","last_season_announced":"2026-06"`, 1))

	embeds, texts, _ := runOneAnnouncePass(t, store, announceAt10())

	if len(embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(embeds))
	}
	if len(texts) != 0 {
		t.Fatalf("the June result was celebrated again: %q", texts)
	}
}

// 送信失敗なら次の巡回でまた送られる: the result is kept until a send is
// confirmed, and the embed that already landed is NOT re-posted for it —
// which is why the mark is taken separately from LastAnnounced.
func TestStartAnnounceScheduler_AFailedSeasonResultIsSentAgainNextPass(t *testing.T) {
	store := seededCasinoStore(t, closedJuneSeasonFile)

	var embeds int
	var texts []string
	fail := true
	send := func(channelID string, embed *discordgo.MessageEmbed) error { embeds++; return nil }
	sendText := func(channelID, content string) error {
		if fail {
			return errors.New("discord is down")
		}
		texts = append(texts, content)
		return nil
	}
	pass := func(at time.Time) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		startAnnounceScheduler(ctx, store, send, sendText,
			func() time.Time { return at }, func(context.Context, time.Time) bool { return false })()
	}

	day1 := announceAt10()
	pass(day1)
	if embeds != 1 || len(texts) != 0 {
		t.Fatalf("first pass: %d embeds / %d results delivered, want 1/0", embeds, len(texts))
	}

	// The next morning: the result is still owed, and now it gets through.
	fail = false
	pass(day1.AddDate(0, 0, 1))
	if embeds != 2 {
		t.Fatalf("second pass: %d embeds in total, want 2 (one per day)", embeds)
	}
	if len(texts) != 1 || !strings.Contains(texts[0], "2026-06") {
		t.Fatalf("second pass delivered %q, want the June result", texts)
	}

	// And having landed, it is not owed a third time.
	pass(day1.AddDate(0, 0, 2))
	if len(texts) != 1 {
		t.Fatalf("the delivered result was posted again: %q", texts)
	}
}

// twoClosedSeasonsFile is the C3B-08 state a month boundary produces while
// the channel is unreachable: June was never posted and July closed on top
// of it. LastSeason holds only the newer one — the queue is what still owes
// both.
const twoClosedSeasonsFile = `{"g1":{"announce_channel_id":"c1","rates":[],"last_announced":"",` +
	`"season_month":"2026-08","last_season":{"month":"2026-07","players":1,"ranks":[` +
	`{"user_id":"u9","net":400,"bonus":2500}]},"unannounced_seasons":[` +
	`{"month":"2026-06","players":7,"ranks":[{"user_id":"u1","net":5000,"bonus":10000}]},` +
	`{"month":"2026-07","players":1,"ranks":[{"user_id":"u9","net":400,"bonus":2500}]}],"users":{}}}`

// 一度に複数の月が溜まりうる: each queued month is its own message, oldest
// first, because each named a different podium and each is owed its ping.
func TestStartAnnounceScheduler_CelebratesEveryQueuedSeason(t *testing.T) {
	store := seededCasinoStore(t, twoClosedSeasonsFile)

	embeds, texts, channels := runOneAnnouncePass(t, store, announceAt10())

	if len(embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(embeds))
	}
	if len(texts) != 2 {
		t.Fatalf("got %d season messages, want 2 — both owed months: %q", len(texts), texts)
	}
	for i, want := range []string{"2026-06", "2026-07"} {
		if !strings.Contains(texts[i], want) {
			t.Fatalf("season message %d = %q, want the %s result (oldest first)", i, texts[i], want)
		}
		if channels[i] != "c1" {
			t.Errorf("season message %d went to %q, want the announcement channel", i, channels[i])
		}
	}

	// Both landed, so neither is owed again.
	if _, texts, _ := runOneAnnouncePass(t, store, announceAt10().AddDate(0, 0, 1)); len(texts) != 0 {
		t.Fatalf("a delivered result was posted again: %q", texts)
	}
}

// 送信に成功した月だけ取り除く: the first message got through and the second
// did not, so the next pass owes exactly the second — and does not ping the
// podium that already landed.
func TestStartAnnounceScheduler_AFailedSeasonDoesNotRetireTheOnesBeforeIt(t *testing.T) {
	store := seededCasinoStore(t, twoClosedSeasonsFile)

	var texts []string
	failFrom := 1
	send := func(channelID string, embed *discordgo.MessageEmbed) error { return nil }
	sendText := func(channelID, content string) error {
		if len(texts) >= failFrom {
			return errors.New("discord is down")
		}
		texts = append(texts, content)
		return nil
	}
	pass := func(at time.Time) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		startAnnounceScheduler(ctx, store, send, sendText,
			func() time.Time { return at }, func(context.Context, time.Time) bool { return false })()
	}

	day1 := announceAt10()
	pass(day1)
	if len(texts) != 1 || !strings.Contains(texts[0], "2026-06") {
		t.Fatalf("first pass delivered %q, want the June result alone", texts)
	}

	failFrom = 2 // the next message gets through
	pass(day1.AddDate(0, 0, 1))
	if len(texts) != 2 {
		t.Fatalf("second pass: delivered %q, want July added", texts)
	}
	if !strings.Contains(texts[1], "2026-07") {
		t.Fatalf("second pass delivered %q, want the July result — June was already posted", texts[1])
	}
}

// TestStartAnnounceScheduler_ResendsOnlyTheSeasonResultLaterTheSameDay is the
// regression for 反復 2 の所見 2. The 10:00 pass gets the embed out and loses
// the result behind it; the 11:00 pass of the SAME day must deliver the
// result and nothing else.
//
// 「同じ日の次の巡回」 is the whole point: LastAnnounced now reads today, and
// before C3B-09 that mark dropped the guild from the sweep entirely, so the
// owed result could not move again until 7/11. The embed count is the other
// half of the assertion — carrying the result must not cost the channel a
// second copy of the morning's posting.
func TestStartAnnounceScheduler_ResendsOnlyTheSeasonResultLaterTheSameDay(t *testing.T) {
	store := seededCasinoStore(t, closedJuneSeasonFile)

	var embeds int
	var texts []string
	fail := true
	send := func(channelID string, embed *discordgo.MessageEmbed) error { embeds++; return nil }
	sendText := func(channelID, content string) error {
		if fail {
			return errors.New("discord is down")
		}
		texts = append(texts, content)
		return nil
	}
	pass := func(at time.Time) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		startAnnounceScheduler(ctx, store, send, sendText,
			func() time.Time { return at }, func(context.Context, time.Time) bool { return false })()
	}

	tenAM := announceAt10()
	pass(tenAM)
	if embeds != 1 || len(texts) != 0 {
		t.Fatalf("10:00 pass: %d embeds / %d results delivered, want 1/0", embeds, len(texts))
	}

	// 11:00, the same day: sending is back and the result is still owed.
	fail = false
	pass(tenAM.Add(time.Hour))
	if embeds != 1 {
		t.Fatalf("11:00 pass: %d embeds in total, want 1 — the morning's posting must not be repeated", embeds)
	}
	if len(texts) != 1 || !strings.Contains(texts[0], "2026-06") {
		t.Fatalf("11:00 pass delivered %q, want the June result", texts)
	}

	// Delivered, so no later pass of the day owes it again.
	pass(tenAM.Add(2 * time.Hour))
	if embeds != 1 || len(texts) != 1 {
		t.Fatalf("12:00 pass: %d embeds / %q results, want 1 embed and the one result", embeds, texts)
	}
}
