using Discord;
using Discord.WebSocket;

namespace TodayIsTodayBot;

class Program
{
    private DiscordSocketClient? _client;
    private bool _isRunning = true;

    static async Task Main(string[] args)
    {
        var program = new Program();
        await program.RunAsync();
    }

    public async Task RunAsync()
    {
        // Discord クライアントの設定
        var config = new DiscordSocketConfig
        {
            GatewayIntents = GatewayIntents.AllUnprivileged | GatewayIntents.MessageContent
        };

        _client = new DiscordSocketClient(config);

        // イベントハンドラの登録
        _client.Log += LogAsync;
        _client.Ready += ReadyAsync;

        // TODO: ここにDiscordボットのトークンを設定してください
        // 環境変数や設定ファイルから読み込むことを推奨
        var token = Environment.GetEnvironmentVariable("DISCORD_BOT_TOKEN") ?? "";
        
        if (string.IsNullOrEmpty(token))
        {
            Console.WriteLine("エラー: Discord ボットトークンが設定されていません。");
            Console.WriteLine("環境変数 DISCORD_BOT_TOKEN を設定してください。");
            return;
        }

        await _client.LoginAsync(TokenType.Bot, token);
        await _client.StartAsync();

        // メインループ（毎フレーム実行されるルーチン）
        await MainLoopAsync();

        await _client.StopAsync();
    }

    /// <summary>
    /// 毎フレーム実行されるメインループ
    /// </summary>
    private async Task MainLoopAsync()
    {
        Console.WriteLine("メインループを開始します。終了するには Ctrl+C を押してください。");
        
        // Ctrl+C でループを終了できるようにする
        Console.CancelKeyPress += (sender, e) =>
        {
            e.Cancel = true;
            _isRunning = false;
        };

        var lastUpdateTime = DateTime.Now;
        var frameCount = 0;

        while (_isRunning)
        {
            var currentTime = DateTime.Now;
            var deltaTime = (currentTime - lastUpdateTime).TotalSeconds;
            lastUpdateTime = currentTime;

            // 毎フレームの処理をここに記述
            await UpdateAsync(deltaTime);

            frameCount++;

            // フレームレートを制限（例: 60FPS）
            await Task.Delay(16); // 約60FPS (1000ms / 60 ≈ 16ms)
        }

        Console.WriteLine($"メインループを終了します。総フレーム数: {frameCount}");
    }

    /// <summary>
    /// 毎フレーム呼び出される更新処理
    /// </summary>
    /// <param name="deltaTime">前フレームからの経過時間（秒）</param>
    private async Task UpdateAsync(double deltaTime)
    {
        // ここに毎フレーム実行したい処理を記述
        // 例: ステータスの更新、定期的なチェック処理など

        // デバッグ用: 1秒ごとにログ出力
        if (frameCount % 60 == 0)
        {
            Console.WriteLine($"[Update] フレーム: {frameCount}, デルタタイム: {deltaTime:F3}秒");
        }
    }

    private int frameCount = 0;

    private Task LogAsync(LogMessage log)
    {
        Console.WriteLine(log.ToString());
        return Task.CompletedTask;
    }

    private Task ReadyAsync()
    {
        Console.WriteLine($"{_client?.CurrentUser} として接続しました！");
        return Task.CompletedTask;
    }
}
