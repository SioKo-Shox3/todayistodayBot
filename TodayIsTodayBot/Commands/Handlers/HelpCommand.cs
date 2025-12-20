using Discord.WebSocket;

namespace TodayIsTodayBot.Commands.Handlers;

/// <summary>
/// サンプルコマンド: /help
/// 利用可能なコマンドの一覧を表示する
/// </summary>
public class HelpCommand : ICommandHandler
{
    private readonly CommandService _commandService;

    public HelpCommand(CommandService commandService)
    {
        _commandService = commandService ?? throw new ArgumentNullException(nameof(commandService));
    }

    public string CommandName => "help";
    
    public string Description => "利用可能なコマンドの一覧を表示します";

    public async Task ExecuteAsync(SocketMessage message, string[] args)
    {
        var commands = _commandService.GetAllCommands();

        if (commands.Count == 0)
        {
            await message.Channel.SendMessageAsync("現在、利用可能なコマンドはありません。");
            return;
        }

        var helpText = "**📋 利用可能なコマンド一覧**\n\n";
        
        foreach (var command in commands.OrderBy(c => c.CommandName))
        {
            helpText += $"`/{command.CommandName}` - {command.Description}\n";
        }

        await message.Channel.SendMessageAsync(helpText);
    }
}
