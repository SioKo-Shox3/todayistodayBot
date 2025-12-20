using Discord.WebSocket;

namespace TodayIsTodayBot.Handlers;

/// <summary>
/// メッセージ受信イベントを処理するハンドラ
/// </summary>
public class MessageHandler
{
    private readonly Commands.CommandService _commandService;

    public MessageHandler(Commands.CommandService commandService)
    {
        _commandService = commandService ?? throw new ArgumentNullException(nameof(commandService));
    }

    /// <summary>
    /// メッセージ受信時の処理
    /// </summary>
    /// <param name="message">受信したメッセージ</param>
    public async Task HandleMessageAsync(SocketMessage message)
    {
        // システムメッセージは無視
        if (message is not SocketUserMessage userMessage)
        {
            return;
        }

        // コマンド処理を試みる
        var isCommand = await _commandService.HandleMessageAsync(userMessage);

        if (!isCommand)
        {
            // コマンドでない場合の処理（必要に応じて実装）
            // 例: 通常のメッセージへの反応など
        }
    }
}
