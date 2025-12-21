using Discord;
using Discord.WebSocket;
using TodayIsTodayBot.Services;

namespace TodayIsTodayBot.Handlers;

/// <summary>
/// Discordのリアクションイベントを処理するハンドラー
/// </summary>
public class ReactionHandler
{
    private readonly ScheduleStorageService _storageService;
    private static readonly string[] _emojiNumbers = new[] 
    { 
        "1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", 
        "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟" 
    };

    public ReactionHandler(ScheduleStorageService storageService)
    {
        _storageService = storageService;
    }

    /// <summary>
    /// リアクションが追加された時の処理
    /// </summary>
    public async Task HandleReactionAddedAsync(
        Cacheable<IUserMessage, ulong> cachedMessage,
        Cacheable<IMessageChannel, ulong> cachedChannel,
        SocketReaction reaction)
    {
        // ボット自身のリアクションは無視
        if (reaction.User.Value?.IsBot ?? true)
            return;

        // アンケートメッセージかどうかをチェック
        var poll = _storageService.GetPollByMessageId(cachedMessage.Id);
        if (poll == null)
            return;

        // 数字絵文字のリアクションかどうかをチェック
        var emojiIndex = Array.IndexOf(_emojiNumbers, reaction.Emote.Name);
        if (emojiIndex == -1 || emojiIndex >= poll.DateOptions.Count)
            return;

        // 投票を記録
        var selectedDate = poll.DateOptions[emojiIndex];
        await _storageService.AddVoteAsync(poll.Id, selectedDate, reaction.UserId);

        Console.WriteLine($"✅ {reaction.User.Value.Username} が {selectedDate} に投票しました");
    }

    /// <summary>
    /// リアクションが削除された時の処理
    /// </summary>
    public async Task HandleReactionRemovedAsync(
        Cacheable<IUserMessage, ulong> cachedMessage,
        Cacheable<IMessageChannel, ulong> cachedChannel,
        SocketReaction reaction)
    {
        // ボット自身のリアクションは無視
        if (reaction.User.Value?.IsBot ?? true)
            return;

        // アンケートメッセージかどうかをチェック
        var poll = _storageService.GetPollByMessageId(cachedMessage.Id);
        if (poll == null)
            return;

        // 数字絵文字のリアクションかどうかをチェック
        var emojiIndex = Array.IndexOf(_emojiNumbers, reaction.Emote.Name);
        if (emojiIndex == -1 || emojiIndex >= poll.DateOptions.Count)
            return;

        // 投票を削除
        var selectedDate = poll.DateOptions[emojiIndex];
        await _storageService.RemoveVoteAsync(poll.Id, selectedDate, reaction.UserId);

        Console.WriteLine($"❌ {reaction.User.Value.Username} が {selectedDate} への投票を取り消しました");
    }
}
