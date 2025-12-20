using Discord.WebSocket;

namespace TodayIsTodayBot.Commands.Handlers;

/// <summary>
/// サンプルコマンド: /ping
/// ボットが応答しているか確認するためのコマンド
/// </summary>
public class PingCommand : ICommandHandler
{
    public string CommandName => "ping";
    
    public string Description => "ボットが応答しているか確認します";

    public async Task ExecuteAsync(SocketMessage message, string[] args)
    {
        await message.Channel.SendMessageAsync("🏓 Pong!");
    }
}
