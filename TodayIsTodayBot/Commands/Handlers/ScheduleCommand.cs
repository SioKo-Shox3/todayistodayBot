using Discord;
using Discord.WebSocket;
using TodayIsTodayBot.Commands;
using TodayIsTodayBot.Services;

namespace TodayIsTodayBot.Commands.Handlers;

/// <summary>
/// 日程調整アンケートを作成するコマンド
/// 使用例: /schedule 2025-01-15 19:00, 2025-01-16 19:00, 2025-01-17 19:00
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
    public string Description => "日程調整アンケートを作成します（例: /schedule 2025-01-15 19:00, 2025-01-16 19:00）";

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
                "`/schedule 2025-01-15 19:00, 2025-01-16 19:00, 2025-01-17 20:00`\n\n" +
                "**説明:**\n" +
                "- カンマ区切りで複数の日程を指定できます\n" +
                "- 各日程には数字の絵文字リアクションが付きます\n" +
                "- 参加可能な日程にリアクションしてください\n" +
                "- 最大10個の日程まで指定できます\n\n" +
                "**結果確認:**\n" +
                "`/schedule-result [poll-id]` で全員が参加可能な日を確認できます");
            return;
        }

        // 引数を結合してカンマで分割
        var fullArgs = string.Join(" ", args);
        var dateStrings = fullArgs.Split(',', StringSplitOptions.RemoveEmptyEntries)
            .Select(d => d.Trim())
            .ToList();

        // 日程数のチェック
        if (dateStrings.Count == 0)
        {
            await message.Channel.SendMessageAsync("❌ 日程を指定してください。例: `/schedule 2025-01-15 19:00, 2025-01-16 19:00`");
            return;
        }

        if (dateStrings.Count > 10)
        {
            await message.Channel.SendMessageAsync("❌ 日程は最大10個まで指定できます。");
            return;
        }

        // 日程のバリデーション
        var validatedDates = new List<string>();
        foreach (var dateStr in dateStrings)
        {
            if (DateTime.TryParse(dateStr, out var parsedDate))
            {
                validatedDates.Add(parsedDate.ToString("yyyy-MM-dd HH:mm"));
            }
            else
            {
                await message.Channel.SendMessageAsync($"❌ 無効な日時形式: `{dateStr}`\n正しい形式: `2025-01-15 19:00`");
                return;
            }
        }

        // Embedメッセージを作成
        var embedBuilder = new EmbedBuilder()
            .WithTitle("📅 日程調整アンケート")
            .WithDescription("参加可能な日程にリアクション（数字の絵文字）をクリックしてください！")
            .WithColor(Color.Blue)
            .WithFooter($"作成者: {message.Author.Username}")
            .WithCurrentTimestamp();

        // 日程リストをフィールドに追加
        for (int i = 0; i < validatedDates.Count; i++)
        {
            embedBuilder.AddField($"{_emojiNumbers[i]} 候補{i + 1}", validatedDates[i], inline: false);
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
