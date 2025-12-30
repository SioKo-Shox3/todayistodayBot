using Discord.WebSocket;

namespace TodayIsTodayBot.Commands.Handlers;

/// <summary>
/// サイコロコマンド: /dice [面数] [回数]
/// 指定した面数のサイコロを指定した回数振る
/// </summary>
public class DiceCommand : ICommandHandler
{
    private readonly Random _random;
    
    public string CommandName => "dice";
    
    public string Description => "サイコロを振ります。使い方: /dice [面数] [回数] (例: /dice 6 3 で6面ダイスを3回振る)";

    public DiceCommand()
    {
        _random = new Random();
    }

    public async Task ExecuteAsync(SocketMessage message, string[] args)
    {
        // デフォルト値
        int sides = 6;
        int rolls = 1;

        // 引数の解析
        if (args.Length >= 1)
        {
            if (!int.TryParse(args[0], out sides) || sides < 2)
            {
                await message.Channel.SendMessageAsync("❌ 面数は2以上の整数で指定してください。");
                return;
            }
        }

        if (args.Length >= 2)
        {
            if (!int.TryParse(args[1], out rolls) || rolls < 1)
            {
                await message.Channel.SendMessageAsync("❌ 回数は1以上の整数で指定してください。");
                return;
            }
        }

        // 回数の上限チェック（スパム防止）
        if (rolls > 100)
        {
            await message.Channel.SendMessageAsync("❌ 一度に振れる回数は100回までです。");
            return;
        }

        // 面数の上限チェック
        if (sides > 1000000)
        {
            await message.Channel.SendMessageAsync("❌ 面数は1,000,000以下で指定してください。");
            return;
        }

        // サイコロを振る
        var results = new List<int>();
        for (int i = 0; i < rolls; i++)
        {
            results.Add(_random.Next(1, sides + 1));
        }

        // 合計を計算
        int total = results.Sum();

        // 結果メッセージを作成
        string resultMessage;
        if (rolls == 1)
        {
            resultMessage = $"🎲 {sides}面ダイスを1回振って、結果は **{total}** です！";
        }
        else
        {
            string individualResults = string.Join("＋", results);
            resultMessage = $"🎲 {sides}面ダイスを{rolls}回振って、結果は **{total}** （{individualResults}）です！";
        }

        await message.Channel.SendMessageAsync(resultMessage);
    }
}
