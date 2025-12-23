using Discord.WebSocket;

namespace TodayIsTodayBot.Commands.Handlers;

/// <summary>
/// 今日は何の日情報を取得するコマンド: /today [日付(MMdd形式、省略時は今日)]
/// </summary>
public class TodayCommand : ICommandHandler
{
    private readonly Services.TodayService _todayService;

    public TodayCommand(Services.TodayService todayService)
    {
        _todayService = todayService ?? throw new ArgumentNullException(nameof(todayService));
    }

    public string CommandName => "today";
    
    public string Description => "今日は何の日かを表示します（例: /today または /today 0101 で1月1日の情報）";

    public async Task ExecuteAsync(SocketMessage message, string[] args)
    {
        DateTime targetDate;

        // 引数がある場合は日付として解析
        if (args.Length > 0)
        {
            var dateArg = args[0];
            
            // MMdd形式で日付を解析
            if (dateArg.Length == 4 && int.TryParse(dateArg, out _))
            {
                var month = int.Parse(dateArg.Substring(0, 2));
                var day = int.Parse(dateArg.Substring(2, 2));
                
                try
                {
                    targetDate = new DateTime(DateTime.Now.Year, month, day);
                }
                catch (ArgumentOutOfRangeException)
                {
                    await message.Channel.SendMessageAsync("❌ 無効な日付です。MMdd形式で正しい日付を入力してください（例: 0101 = 1月1日）");
                    return;
                }
            }
            else
            {
                await message.Channel.SendMessageAsync("❌ 日付はMMdd形式で入力してください（例: 0101 = 1月1日、1225 = 12月25日）");
                return;
            }
        }
        else
        {
            targetDate = DateTime.Today;
        }

        // 処理中メッセージを送信
        var processingMessage = await message.Channel.SendMessageAsync($"🔍 {targetDate:M月d日}の情報を取得中...");

        try
        {
            // 今日は何の日情報を取得
            var todayInfo = await _todayService.GetTodayInfoAsync(targetDate);

            // 処理中メッセージを削除
            await processingMessage.DeleteAsync();

            // 情報を送信
            await message.Channel.SendMessageAsync(todayInfo);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[TodayCommand] エラー: {ex}");
            
            // 処理中メッセージを削除
            try
            {
                await processingMessage.DeleteAsync();
            }
            catch { }

            await message.Channel.SendMessageAsync($"❌ 情報の取得中にエラーが発生しました: {ex.Message}");
        }
    }
}
