using Discord.WebSocket;

namespace TodayIsTodayBot.Commands;

/// <summary>
/// コマンド実行のコンテキスト情報
/// </summary>
public class CommandContext
{
    /// <summary>
    /// メッセージオブジェクト
    /// </summary>
    public SocketMessage Message { get; }

    /// <summary>
    /// チャンネル
    /// </summary>
    public ISocketMessageChannel Channel => Message.Channel;

    /// <summary>
    /// 送信者
    /// </summary>
    public SocketUser User => Message.Author;

    /// <summary>
    /// コマンド名（"/"を除いた部分）
    /// </summary>
    public string CommandName { get; }

    /// <summary>
    /// コマンドの引数
    /// </summary>
    public string[] Arguments { get; }

    public CommandContext(SocketMessage message, string commandName, string[] arguments)
    {
        Message = message;
        CommandName = commandName;
        Arguments = arguments;
    }
}
