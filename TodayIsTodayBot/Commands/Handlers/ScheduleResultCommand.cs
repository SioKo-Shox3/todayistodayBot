using Discord;
using Discord.WebSocket;
using TodayIsTodayBot.Commands;
using TodayIsTodayBot.Services;

namespace TodayIsTodayBot.Commands.Handlers;

/// <summary>
/// 日程調整アンケートの結果を表示するコマンド
/// 使用例: /schedule-result [poll-id]
/// </summary>
public class ScheduleResultCommand : ICommandHandler
{
    private readonly ScheduleStorageService _storageService;

    public string CommandName => "schedule-result";
    public string Description => "日程調整アンケートの結果を表示します（例: /schedule-result poll-id）";

    public ScheduleResultCommand(ScheduleStorageService storageService)
    {
        _storageService = storageService;
    }

    public async Task ExecuteAsync(SocketMessage message, string[] args)
    {
        // ヘルプ表示
        if (args.Length == 0)
        {
            await message.Channel.SendMessageAsync(
                "📊 **アンケート結果の確認方法**\n\n" +
                "**使用例:**\n" +
                "`/schedule-result [poll-id]`\n\n" +
                "**説明:**\n" +
                "- poll-id は `/schedule` コマンド実行時に表示されます\n" +
                "- 全員が参加可能な日程が表示されます\n" +
                "- 各日程の投票状況も確認できます");
            return;
        }

        var pollId = args[0];
        var poll = _storageService.GetPollById(pollId);

        if (poll == null)
        {
            await message.Channel.SendMessageAsync($"❌ Poll ID `{pollId}` が見つかりませんでした。");
            return;
        }

        // 全員が参加可能な日程を取得
        var availableForAll = _storageService.GetDatesAvailableForAll(pollId);

        // Embedメッセージを作成
        var embedBuilder = new EmbedBuilder()
            .WithTitle("📊 日程調整結果")
            .WithColor(availableForAll.Count > 0 ? Color.Green : Color.Orange)
            .WithCurrentTimestamp();

        // 全員が参加可能な日程を表示
        if (availableForAll.Count > 0)
        {
            var availableDatesText = string.Join("\n", availableForAll.Select(d => $"✅ {d}"));
            embedBuilder.AddField("🎉 全員が参加可能な日程", availableDatesText, inline: false);
        }
        else
        {
            embedBuilder.AddField("⚠️ 全員が参加可能な日程", "残念ながら、全員が参加可能な日程はありませんでした。", inline: false);
        }

        // 投票した全ユーザーを取得
        var allVoters = poll.Votes.Values
            .SelectMany(v => v)
            .Distinct()
            .ToList();

        embedBuilder.AddField("👥 投票者数", $"{allVoters.Count}人", inline: true);
        embedBuilder.AddField("📅 日程候補数", $"{poll.DateOptions.Count}個", inline: true);

        // 各日程の詳細な投票状況
        var detailsText = "";
        for (int i = 0; i < poll.DateOptions.Count; i++)
        {
            var date = poll.DateOptions[i];
            var votersCount = poll.Votes[date].Count;
            var percentage = allVoters.Count > 0 
                ? (int)((double)votersCount / allVoters.Count * 100) 
                : 0;
            
            var bar = GenerateProgressBar(percentage);
            detailsText += $"**{date}**\n{bar} {votersCount}/{allVoters.Count}人 ({percentage}%)\n\n";
        }

        if (!string.IsNullOrEmpty(detailsText))
        {
            embedBuilder.AddField("📈 詳細な投票状況", detailsText, inline: false);
        }

        embedBuilder.WithFooter($"Poll ID: {pollId}");

        await message.Channel.SendMessageAsync(embed: embedBuilder.Build());
    }

    /// <summary>
    /// パーセンテージからプログレスバーを生成
    /// </summary>
    private string GenerateProgressBar(int percentage, int length = 10)
    {
        var filled = (int)(percentage / 10.0);
        var empty = length - filled;
        return $"[{'█'.ToString().PadLeft(filled, '█')}{'░'.ToString().PadLeft(empty, '░')}]";
    }
}
