using Discord;
using Discord.WebSocket;
using Microsoft.Extensions.Configuration;
using TodayIsTodayBot.Commands;
using TodayIsTodayBot.Handlers;

namespace TodayIsTodayBot;

class Program
{
    private DiscordSocketClient? _client;
    private bool _isRunning = true;
    private IConfiguration? _configuration;
    private int _frameRate = 60;
    private CommandService? _commandService;
    private MessageHandler? _messageHandler;

    static async Task Main(string[] args)
    {
        var program = new Program();
        await program.RunAsync();
    }

    public async Task RunAsync()
    {
        // 設定ファイルの読み込み
        _configuration = new ConfigurationBuilder()
            .SetBasePath(Directory.GetCurrentDirectory())
            .AddJsonFile("appsettings.json", optional: false, reloadOnChange: true)
            .Build();

        // 設定からフレームレートを取得
        _frameRate = _configuration.GetValue<int>("Bot:FrameRate", 60);

        // コマンドサービスの初期化
        _commandService = new CommandService("/");
        
        // コマンドの登録
        RegisterCommands();
        
        // メッセージハンドラの初期化
        _messageHandler = new MessageHandler(_commandService);

        // Discord クライアントの設定
        var config = new DiscordSocketConfig
        {
            GatewayIntents = GatewayIntents.AllUnprivileged | GatewayIntents.MessageContent
        };

        _client = new DiscordSocketClient(config);

        // イベントハンドラの登録
        _client.Log += LogAsync;
        _client.Ready += ReadyAsync;
        _client.MessageReceived += MessageReceivedAsync;

        // 設定ファイルからトークンを読み込み
        var token = _configuration["Discord:BotToken"];
        
        if (string.IsNullOrEmpty(token) || token == "ここにDiscordボットトークンを入力してください")
        {
            Console.WriteLine("エラー: Discord ボットトークンが設定されていません。");
            Console.WriteLine("appsettings.json ファイルの Discord:BotToken を設定してください。");
            return;
        }

        await _client.LoginAsync(TokenType.Bot, token);
        await _client.StartAsync();

        // メインループ（毎フレーム実行されるルーチン）
        await MainLoopAsync();

        await _client.StopAsync();
    }

    /// <summary>
    /// コマンドを登録する
    /// </summary>
    private void RegisterCommands()
    {
        if (_commandService == null)
        {
            return;
        }

        // 基本コマンドの登録
        _commandService.RegisterCommand(new Commands.Handlers.PingCommand());
        _commandService.RegisterCommand(new Commands.Handlers.HelpCommand(_commandService));
        
        // 今後、新しいコマンドはここに追加していきます
    }

    /// <summary>
    /// 毎フレーム実行されるメインループ
    /// </summary>
    private async Task MainLoopAsync()
    {
        Console.WriteLine("メインループを開始します。終了するには Ctrl+C を押してください。");
        Console.WriteLine($"フレームレート: {_frameRate} FPS");
        
        // Ctrl+C でループを終了できるようにする
        Console.CancelKeyPress += (sender, e) =>
        {
            e.Cancel = true;
            _isRunning = false;
        };

        var lastUpdateTime = DateTime.Now;
        var frameCount = 0;
        var frameDelay = 1000 / _frameRate; // ミリ秒単位のフレーム遅延

        while (_isRunning)
        {
            var currentTime = DateTime.Now;
            var deltaTime = (currentTime - lastUpdateTime).TotalSeconds;
            lastUpdateTime = currentTime;

            // 毎フレームの処理をここに記述
            await UpdateAsync(deltaTime, frameCount);

            frameCount++;

            // フレームレートを制限
            await Task.Delay(frameDelay);
        }

        Console.WriteLine($"メインループを終了します。総フレーム数: {frameCount}");
    }

    /// <summary>
    /// 毎フレーム呼び出される更新処理
    /// </summary>
    /// <param name="deltaTime">前フレームからの経過時間（秒）</param>
    /// <param name="frameCount">現在のフレーム数</param>
    private async Task UpdateAsync(double deltaTime, int frameCount)
    {
        // ここに毎フレーム実行したい処理を記述
        // 例: ステータスの更新、定期的なチェック処理など

        // デバッグ用: 1秒ごとにログ出力
        if (frameCount % _frameRate == 0 && frameCount > 0)
        {
            Console.WriteLine($"[Update] フレーム: {frameCount}, デルタタイム: {deltaTime:F3}秒");
        }
        
        await Task.CompletedTask;
    }

    private Task LogAsync(LogMessage log)
    {
        Console.WriteLine(log.ToString());
        return Task.CompletedTask;
    }

    private Task ReadyAsync()
    {
        Console.WriteLine($"{_client?.CurrentUser} として接続しました！");
        Console.WriteLine($"コマンドプレフィックス: /");
        Console.WriteLine($"登録されているコマンド数: {_commandService?.GetAllCommands().Count ?? 0}");
        return Task.CompletedTask;
    }

    private async Task MessageReceivedAsync(SocketMessage message)
    {
        if (_messageHandler != null)
        {
            await _messageHandler.HandleMessageAsync(message);
        }
    }
}
