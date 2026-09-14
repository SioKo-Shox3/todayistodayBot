package commands

import (
	"context"
	"errors"
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
	if len(embed.Fields) != 3 {
		t.Fatalf("expected the ranking, jackpot and lottery fields, got %d", len(embed.Fields))
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
	if len(embed.Fields) != 3 {
		t.Fatalf("expected the ranking, jackpot and lottery fields, got %d", len(embed.Fields))
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
	if len(embed.Fields) != 3 {
		t.Fatalf("expected the ranking, jackpot and lottery fields, got %d", len(embed.Fields))
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

func TestBuildAnnouncementEmbed_LotteryFieldShowsYesterdaysWinner(t *testing.T) {
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	job := announceJob(history, nil)
	job.LotteryDraw = &casino.LotteryDraw{Date: "2026-07-10", WinnerID: "u1", Prize: 900, TicketsSold: 20, Buyers: 3}
	// The pot the draw just emptied and reopened reads 0/0 on a normal morning.
	embed := buildAnnouncementEmbed(job)
	want := "🏆 昨日の当選: <@u1> が 900チップ 獲得(20枚 / 3人)\n💰 本日の賞金: 0チップ / 🎫 売れた枚数: 0枚"
	if got := lotteryField(t, embed).Value; got != want {
		t.Fatalf("unexpected lottery field:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildAnnouncementEmbed_LotteryFieldNoWinnerRollsOver(t *testing.T) {
	// 購入者 0 の日: drawLotteryLocked leaves LastDraw untouched (nil here)
	// and rolls the whole prize forward, so the field must say 繰り越し and
	// still show the carried-over amount against 0 tickets.
	history := []casino.DailyRate{{Rate: 100, Trend: casino.TrendFlat, Event: casino.EventNone}}
	job := announceJob(history, nil)
	job.LotteryDraw = nil
	job.LotteryPrize, job.LotteryTickets = 500, 0
	embed := buildAnnouncementEmbed(job)
	want := "🏆 昨日の当選: 該当者なし — 賞金は繰り越し\n💰 本日の賞金: 500チップ / 🎫 売れた枚数: 0枚"
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
	job.LotteryDraw = &casino.LotteryDraw{Date: "2026-07-10", WinnerID: "u1", Prize: 900, TicketsSold: 20, Buyers: 3}
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
		// creditChipsCappedLocked and the pot rolls forward, but the draw
		// still named them: the ping is owed either way.
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
	if got := lotteryField(t, embeds[0]).Value; !strings.Contains(got, "🏆 昨日の当選: <@u1> が 90チップ 獲得(2枚 / 1人)") {
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
	// MaxChips is recorded with Prize 0 and the pot rolls forward. The draw
	// still named them, so the @mentioning message is still posted — the
	// amount it prints is the 0 that was actually credited.
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
