using Discord.WebSocket;

namespace TodayIsTodayBot.Commands.Handlers;

/// <summary>
/// 天気情報を取得するコマンド: /weather [地域名]
/// </summary>
public class WeatherCommand : ICommandHandler
{
    private readonly Services.WeatherService _weatherService;

    public WeatherCommand(Services.WeatherService weatherService)
    {
        _weatherService = weatherService ?? throw new ArgumentNullException(nameof(weatherService));
    }

    public string CommandName => "weather";
    
    public string Description => "指定した地域の天気情報を取得します（日本語・英語対応、例: /weather 東京 または /weather Tokyo）";

    public async Task ExecuteAsync(SocketMessage message, string[] args)
    {
        // 引数がない場合はヘルプメッセージを表示
        if (args.Length == 0)
        {
            var helpMessage = "**🌤️ 天気コマンドの使い方**\n\n" +
                             "**使用例**:\n" +
                             "`/weather 東京` - 東京の天気を取得\n" +
                             "`/weather 大阪` - 大阪の天気を取得\n" +
                             "`/weather Tokyo` - 英語の都市名でも可\n\n" +
                             _weatherService.GetAvailableCities();
            
            await message.Channel.SendMessageAsync(helpMessage);
            return;
        }

        // 引数を結合して都市名として扱う（スペース区切りの都市名に対応）
        var cityName = string.Join(" ", args);

        // 処理中メッセージを送信
        var processingMessage = await message.Channel.SendMessageAsync($"🔍 {cityName}の天気情報を取得中...");

        try
        {
            // 天気情報を取得
            var weatherInfo = await _weatherService.GetWeatherAsync(cityName);

            // 処理中メッセージを削除
            await processingMessage.DeleteAsync();

            // 天気情報を送信
            await message.Channel.SendMessageAsync(weatherInfo);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[WeatherCommand] エラー: {ex}");
            
            // 処理中メッセージを削除
            try
            {
                await processingMessage.DeleteAsync();
            }
            catch { }

            await message.Channel.SendMessageAsync($"❌ 天気情報の取得中にエラーが発生しました: {ex.Message}");
        }
    }
}
