using Discord.WebSocket;
using System.Collections.Concurrent;

namespace TodayIsTodayBot.Commands;

/// <summary>
/// コマンドの登録と実行を管理するサービス
/// </summary>
public class CommandService
{
    private readonly ConcurrentDictionary<string, ICommandHandler> _commands;
    private readonly string _commandPrefix;

    public CommandService(string commandPrefix = "/")
    {
        _commands = new ConcurrentDictionary<string, ICommandHandler>();
        _commandPrefix = commandPrefix;
    }

    /// <summary>
    /// コマンドハンドラを登録する
    /// </summary>
    /// <param name="handler">登録するコマンドハンドラ</param>
    /// <returns>登録に成功した場合はtrue</returns>
    public bool RegisterCommand(ICommandHandler handler)
    {
        if (handler == null)
        {
            throw new ArgumentNullException(nameof(handler));
        }

        var success = _commands.TryAdd(handler.CommandName.ToLower(), handler);
        
        if (success)
        {
            Console.WriteLine($"[CommandService] コマンドを登録しました: {_commandPrefix}{handler.CommandName} - {handler.Description}");
        }
        else
        {
            Console.WriteLine($"[CommandService] 警告: コマンド '{handler.CommandName}' は既に登録されています。");
        }

        return success;
    }

    /// <summary>
    /// コマンドハンドラの登録を解除する
    /// </summary>
    /// <param name="commandName">コマンド名</param>
    /// <returns>解除に成功した場合はtrue</returns>
    public bool UnregisterCommand(string commandName)
    {
        var success = _commands.TryRemove(commandName.ToLower(), out _);
        
        if (success)
        {
            Console.WriteLine($"[CommandService] コマンドの登録を解除しました: {_commandPrefix}{commandName}");
        }

        return success;
    }

    /// <summary>
    /// 登録されているすべてのコマンドを取得する
    /// </summary>
    public IReadOnlyCollection<ICommandHandler> GetAllCommands()
    {
        return _commands.Values.ToList().AsReadOnly();
    }

    /// <summary>
    /// メッセージを解析してコマンドを実行する
    /// </summary>
    /// <param name="message">受信したメッセージ</param>
    /// <returns>コマンドが実行された場合はtrue</returns>
    public async Task<bool> HandleMessageAsync(SocketMessage message)
    {
        // ボット自身のメッセージは無視
        if (message.Author.IsBot)
        {
            return false;
        }

        var content = message.Content.Trim();

        // コマンドプレフィックスで始まっているかチェック
        if (!content.StartsWith(_commandPrefix))
        {
            return false;
        }

        // コマンド部分を抽出
        var commandText = content.Substring(_commandPrefix.Length);
        
        if (string.IsNullOrWhiteSpace(commandText))
        {
            return false;
        }

        // コマンドと引数を分割
        var parts = commandText.Split(new[] { ' ' }, StringSplitOptions.RemoveEmptyEntries);
        var commandName = parts[0].ToLower();
        var args = parts.Length > 1 ? parts[1..] : Array.Empty<string>();

        // コマンドハンドラを検索
        if (_commands.TryGetValue(commandName, out var handler))
        {
            try
            {
                Console.WriteLine($"[CommandService] コマンドを実行します: {_commandPrefix}{commandName} (ユーザー: {message.Author.Username})");
                await handler.ExecuteAsync(message, args);
                return true;
            }
            catch (Exception ex)
            {
                Console.WriteLine($"[CommandService] エラー: コマンド '{commandName}' の実行中にエラーが発生しました: {ex.Message}");
                Console.WriteLine(ex.StackTrace);
                
                // エラーメッセージをチャンネルに送信
                try
                {
                    await message.Channel.SendMessageAsync($"❌ コマンドの実行中にエラーが発生しました: {ex.Message}");
                }
                catch
                {
                    // エラーメッセージの送信に失敗した場合は無視
                }
                
                return false;
            }
        }
        else
        {
            // 未知のコマンド
            Console.WriteLine($"[CommandService] 警告: 未知のコマンド '{commandName}' が実行されました (ユーザー: {message.Author.Username})");
            
            try
            {
                await message.Channel.SendMessageAsync($"❌ 未知のコマンド: `{_commandPrefix}{commandName}`");
            }
            catch
            {
                // エラーメッセージの送信に失敗した場合は無視
            }
            
            return false;
        }
    }
}
