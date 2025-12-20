using Discord.WebSocket;

namespace TodayIsTodayBot.Commands;

/// <summary>
/// コマンドハンドラのインターフェース
/// </summary>
public interface ICommandHandler
{
    /// <summary>
    /// コマンド名（"/"を除いた部分）
    /// </summary>
    string CommandName { get; }

    /// <summary>
    /// コマンドの説明
    /// </summary>
    string Description { get; }

    /// <summary>
    /// コマンドを実行する
    /// </summary>
    /// <param name="message">受信したメッセージ</param>
    /// <param name="args">コマンドの引数</param>
    Task ExecuteAsync(SocketMessage message, string[] args);
}
