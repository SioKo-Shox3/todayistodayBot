using Discord;
using Discord.WebSocket;
using TodayIsTodayBot.Commands;
using TodayIsTodayBot.Services;

namespace TodayIsTodayBot.Commands.Handlers;

/// <summary>
/// 日程調整アンケートを作成するコマンド
/// 使用例: /schedule +1 3 19:00 (明日から3日分、19:00で作成)
/// </summary>
public class ScheduleCommand : ICommandHandler
{
    private readonly ScheduleStorageService _storageService;
    private static readonly string[] _emojiNumbers = new[] 
    { 
        "1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", 
        "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟" 
    };

    public string CommandName => "schedule";
    public string Description => "日程調整アンケートを作成します（例: /schedule +1 3 19:00 → 明日から3日分、19:00で作成）";

    public ScheduleCommand(ScheduleStorageService storageService)
    {
        _storageService = storageService;
    }

    public async Task ExecuteAsync(SocketMessage message, string[] args)
    {
        // ヘルプ表示
        if (args.Length == 0 || args[0] == "help")
        {
            await message.Channel.SendMessageAsync(
                "📅 **日程調整アンケートの使い方**\n\n" +
                "**使用例:**\n" +
                "`/schedule +1 3 19:00` → 明日から3日分、各日19:00でアンケート作成\n" +
                "`/schedule 0 5 20:00` → 今日から5日分、各日20:00でアンケート作成\n" +
                "`/schedule -2 4 18:00` → 2日前から4日分、各日18:00でアンケート作成\n\n" +
                "**パラメータ:**\n" +
                "1. 基準日（+数字=○日後、-数字=○日前、0=今日）\n" +
                "2. 作成する日程の数（1〜10）\n" +
                "3. 時刻（HH:mm形式）\n\n" +
                "**説明:**\n" +
                "- 各日程には数字の絵文字リアクションが付きます\n" +
                "- 参加可能な日程にリアクションしてください\n" +
                "- 最大10個の日程まで指定できます\n\n" +
                "**結果確認:**\n" +
                "`/schedule-result [poll-id]` で全員が参加可能な日を確認できます");
            return;
        }

        // パラメータチェック: 基準日オフセット、日数、時刻
        if (args.Length != 3)
        {
            await message.Channel.SendMessageAsync(
                "❌ パラメータが不足しています。\n" +
                "正しい形式: `/schedule [基準日] [日数] [時刻]`\n" +
                "例: `/schedule +1 3 19:00` （明日から3日分、19:00で作成）");
            return;
        }

        // 基準日オフセットのパース
        if (!int.TryParse(args[0], out var dayOffset))
        {
            await message.Channel.SendMessageAsync(
                "❌ 基準日の形式が正しくありません。\n" +
                "例: `+1`（明日）、`0`（今日）、`-1`（昨日）");
            return;
        }

        // 日数のパース
        if (!int.TryParse(args[1], out var dayCount) || dayCount < 1 || dayCount > 10)
        {
            await message.Channel.SendMessageAsync(
                "❌ 日数は1〜10の範囲で指定してください。\n" +
                "例: `3`（3日分）");
            return;
        }

        // 時刻のパース
        if (!TimeSpan.TryParse(args[2], out var time))
        {
            await message.Channel.SendMessageAsync(
                "❌ 時刻の形式が正しくありません。\n" +
                "正しい形式: `19:00`、`09:30` など");
            return;
        }

        // メッセージ送信日を基準に日程を生成
        var baseDate = message.Timestamp.Date.AddDays(dayOffset);
        var validatedDates = new List<string>();

        for (int i = 0; i < dayCount; i++)
        {
            var date = baseDate.AddDays(i).Add(time);
            validatedDates.Add(date.ToString("yyyy-MM-dd HH:mm"));
        }

        // Embedメッセージを作成
        var embedBuilder = new EmbedBuilder()
            .WithTitle("📅 日程調整アンケート")
            .WithDescription("参加可能な日程にリアクション（数字の絵文字）をクリックしてください！")
            .WithColor(Color.Blue)
            .WithFooter($"作成者: {message.Author.Username}")
            .WithCurrentTimestamp();

        // 日程リストをフィールドに追加（曜日も表示）
        var japaneseWeekDays = new[] { "日", "月", "火", "水", "木", "金", "土" };
        for (int i = 0; i < validatedDates.Count; i++)
        {
            var date = DateTime.Parse(validatedDates[i]);
            var weekDay = japaneseWeekDays[(int)date.DayOfWeek];
            embedBuilder.AddField(
                $"{_emojiNumbers[i]} 候補{i + 1}", 
                $"{validatedDates[i]} ({weekDay})", 
                inline: false);
        }

        // メッセージを送信
        var pollMessage = await message.Channel.SendMessageAsync(embed: embedBuilder.Build());

        // リアクションを追加
        for (int i = 0; i < validatedDates.Count; i++)
        {
            var emoji = new Emoji(_emojiNumbers[i]);
            await pollMessage.AddReactionAsync(emoji);
        }

        // アンケートデータを保存
        var poll = await _storageService.CreatePollAsync(
            message.Author.Id,
            pollMessage.Id,
            message.Channel.Id,
            validatedDates
        );

        // 確認メッセージ
        await message.Channel.SendMessageAsync(
            $"✅ アンケートを作成しました！\n" +
            $"Poll ID: `{poll.Id}`\n" +
            $"結果確認: `/schedule-result {poll.Id}`");
    }
}
