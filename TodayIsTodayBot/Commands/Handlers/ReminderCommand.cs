using Discord.WebSocket;
using TodayIsTodayBot.Commands;
using TodayIsTodayBot.Services;

namespace TodayIsTodayBot.Commands.Handlers;

/// <summary>
/// スケジュールリマインダーを管理するコマンド
/// </summary>
public class ReminderCommand : ICommandHandler
{
    private readonly ReminderService _reminderService;
    private readonly ScheduleStorageService _scheduleStorageService;

    public string CommandName => "reminder";
    public string Description => "スケジュールリマインダーを管理します（例: /reminder set [poll-id]）";

    public ReminderCommand(ReminderService reminderService, ScheduleStorageService scheduleStorageService)
    {
        _reminderService = reminderService;
        _scheduleStorageService = scheduleStorageService;
    }

    public async Task ExecuteAsync(SocketMessage message, string[] args)
    {
        // ヘルプ表示
        if (args.Length == 0 || args[0] == "help")
        {
            await message.Channel.SendMessageAsync(
                "⏰ **リマインダーの使い方**\n\n" +
                "**コマンド一覧:**\n" +
                "`/reminder set [poll-id]` - このチャンネルにリマインダーを設定\n" +
                "`/reminder enable [poll-id]` - リマインダーを有効化\n" +
                "`/reminder disable [poll-id]` - リマインダーを無効化\n" +
                "`/reminder list` - 設定済みリマインダー一覧を表示\n" +
                "`/reminder delete [poll-id]` - リマインダーを削除\n\n" +
                "**説明:**\n" +
                "- リマインダーを設定すると、全員が参加可能な日程の開始時間に @everyone で通知されます\n" +
                "- 通知はこのコマンドを実行したチャンネルに送られます\n" +
                "- poll-id は `/schedule` コマンド実行時に表示されます");
            return;
        }

        var subCommand = args[0].ToLower();

        switch (subCommand)
        {
            case "set":
                await HandleSetAsync(message, args);
                break;
            case "enable":
                await HandleEnableAsync(message, args);
                break;
            case "disable":
                await HandleDisableAsync(message, args);
                break;
            case "list":
                await HandleListAsync(message);
                break;
            case "delete":
                await HandleDeleteAsync(message, args);
                break;
            default:
                await message.Channel.SendMessageAsync(
                    "❌ 不明なサブコマンドです。\n" +
                    "`/reminder help` でヘルプを表示します。");
                break;
        }
    }

    /// <summary>
    /// リマインダーを設定
    /// </summary>
    private async Task HandleSetAsync(SocketMessage message, string[] args)
    {
        if (args.Length < 2)
        {
            await message.Channel.SendMessageAsync(
                "❌ Poll ID を指定してください。\n" +
                "使用例: `/reminder set [poll-id]`");
            return;
        }

        var pollId = args[1];
        var poll = _scheduleStorageService.GetPollById(pollId);

        if (poll == null)
        {
            await message.Channel.SendMessageAsync($"❌ Poll ID `{pollId}` が見つかりませんでした。");
            return;
        }

        var reminder = await _reminderService.CreateReminderAsync(
            pollId,
            message.Channel.Id,
            message.Author.Id
        );

        await message.Channel.SendMessageAsync(
            $"✅ リマインダーを設定しました！\n" +
            $"Poll ID: `{pollId}`\n" +
            $"通知チャンネル: <#{message.Channel.Id}>\n" +
            $"有効化: `/reminder enable {pollId}`");
    }

    /// <summary>
    /// リマインダーを有効化
    /// </summary>
    private async Task HandleEnableAsync(SocketMessage message, string[] args)
    {
        if (args.Length < 2)
        {
            await message.Channel.SendMessageAsync(
                "❌ Poll ID を指定してください。\n" +
                "使用例: `/reminder enable [poll-id]`");
            return;
        }

        var pollId = args[1];
        var success = await _reminderService.EnableReminderAsync(pollId);

        if (success)
        {
            await message.Channel.SendMessageAsync(
                $"✅ リマインダーを有効化しました！\n" +
                $"Poll ID: `{pollId}`\n" +
                $"全員が参加可能な日程の開始時間に @everyone で通知します。");
        }
        else
        {
            await message.Channel.SendMessageAsync(
                $"❌ Poll ID `{pollId}` のリマインダーが見つかりませんでした。\n" +
                $"先に `/reminder set {pollId}` でリマインダーを設定してください。");
        }
    }

    /// <summary>
    /// リマインダーを無効化
    /// </summary>
    private async Task HandleDisableAsync(SocketMessage message, string[] args)
    {
        if (args.Length < 2)
        {
            await message.Channel.SendMessageAsync(
                "❌ Poll ID を指定してください。\n" +
                "使用例: `/reminder disable [poll-id]`");
            return;
        }

        var pollId = args[1];
        var success = await _reminderService.DisableReminderAsync(pollId);

        if (success)
        {
            await message.Channel.SendMessageAsync($"✅ リマインダーを無効化しました（Poll ID: `{pollId}`）");
        }
        else
        {
            await message.Channel.SendMessageAsync($"❌ Poll ID `{pollId}` のリマインダーが見つかりませんでした。");
        }
    }

    /// <summary>
    /// リマインダー一覧を表示
    /// </summary>
    private async Task HandleListAsync(SocketMessage message)
    {
        var reminders = _reminderService.GetAllReminders().ToList();

        if (reminders.Count == 0)
        {
            await message.Channel.SendMessageAsync("📋 設定されているリマインダーはありません。");
            return;
        }

        var response = "📋 **リマインダー一覧**\n\n";
        foreach (var reminder in reminders)
        {
            var poll = _scheduleStorageService.GetPollById(reminder.PollId);
            var status = reminder.IsEnabled ? "✅ 有効" : "❌ 無効";
            var pollInfo = poll != null ? $"({poll.DateOptions.Count}個の日程)" : "(アンケート削除済み)";
            
            response += $"**Poll ID:** `{reminder.PollId}` {pollInfo}\n";
            response += $"状態: {status}\n";
            response += $"通知チャンネル: <#{reminder.ChannelId}>\n";
            response += $"通知済み: {reminder.NotifiedDates.Count}件\n\n";
        }

        await message.Channel.SendMessageAsync(response);
    }

    /// <summary>
    /// リマインダーを削除
    /// </summary>
    private async Task HandleDeleteAsync(SocketMessage message, string[] args)
    {
        if (args.Length < 2)
        {
            await message.Channel.SendMessageAsync(
                "❌ Poll ID を指定してください。\n" +
                "使用例: `/reminder delete [poll-id]`");
            return;
        }

        var pollId = args[1];
        var success = await _reminderService.DeleteReminderAsync(pollId);

        if (success)
        {
            await message.Channel.SendMessageAsync($"✅ リマインダーを削除しました（Poll ID: `{pollId}`）");
        }
        else
        {
            await message.Channel.SendMessageAsync($"❌ Poll ID `{pollId}` のリマインダーが見つかりませんでした。");
        }
    }
}
