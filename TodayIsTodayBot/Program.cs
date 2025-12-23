using Discord;
using Discord.WebSocket;
using Microsoft.Extensions.Configuration;
using TodayIsTodayBot.Commands;
using TodayIsTodayBot.Handlers;
using TodayIsTodayBot.Services;

namespace TodayIsTodayBot;

class Program
{
    private DiscordSocketClient? _client;
    private bool _isRunning = true;
    private IConfiguration? _configuration;
    private int _frameRate = 60;
    private CommandService? _commandService;
    private MessageHandler? _messageHandler;
    private HttpClient? _httpClient;
    private ScheduleStorageService? _scheduleStorageService;
    private ReactionHandler? _reactionHandler;
    private ReminderService? _reminderService;

    static async Task Main(string[] args)
    {
        var program = new Program();
        await program.RunAsync();
    }

    public async Task RunAsync()
    {
        // 設定ファイルの読み込み（実行ファイルのディレクトリを基準にする）
        var appDirectory = AppDomain.CurrentDomain.BaseDirectory;
        Console.WriteLine($"📁 アプリケーションディレクトリ: {appDirectory}");
        
        // 設定ファイルが存在しない場合は作成
        var appSettingsPath = Path.Combine(appDirectory, "appsettings.json");
        if (!File.Exists(appSettingsPath))
        {
            Console.WriteLine("⚠️ appsettings.json が見つかりません。新規作成します...");
            var defaultSettings = @"{
  ""Discord"": {
    ""BotToken"": ""ここにDiscordボットトークンを入力してください""
  },
  ""Bot"": {
    ""FrameRate"": 60
  }
}";
            await File.WriteAllTextAsync(appSettingsPath, defaultSettings);
            Console.WriteLine($"✅ appsettings.json を作成しました: {appSettingsPath}");
            Console.WriteLine("⚠️ Discord:BotToken を設定してからアプリケーションを再起動してください。");
        }
        
        _configuration = new ConfigurationBuilder()
            .SetBasePath(appDirectory)
            .AddJsonFile("appsettings.json", optional: false, reloadOnChange: true)
            .Build();

        // 設定からフレームレートを取得
        _frameRate = _configuration.GetValue<int>("Bot:FrameRate", 60);

        // HttpClientの初期化
        _httpClient = new HttpClient();

        // スケジュールストレージサービスの初期化
        _scheduleStorageService = new ScheduleStorageService();

        // リマインダーサービスの初期化
        _reminderService = new ReminderService();

        // コマンドサービスの初期化
        _commandService = new CommandService("/");
        
        // コマンドの登録
        RegisterCommands();
        
        // メッセージハンドラの初期化
        _messageHandler = new MessageHandler(_commandService);

        // リアクションハンドラの初期化
        _reactionHandler = new ReactionHandler(_scheduleStorageService);

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
        _client.ReactionAdded += ReactionAddedAsync;
        _client.ReactionRemoved += ReactionRemovedAsync;

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
        
        // 天気コマンドの登録（Open-Meteo使用、APIキー不要）
        var weatherService = new WeatherService(_httpClient!);
        _commandService.RegisterCommand(new Commands.Handlers.WeatherCommand(weatherService));
        
        // 日程調整コマンドの登録
        _commandService.RegisterCommand(new Commands.Handlers.ScheduleCommand(_scheduleStorageService!));
        _commandService.RegisterCommand(new Commands.Handlers.ScheduleResultCommand(_scheduleStorageService!));
        
        // リマインダーコマンドの登録
        _commandService.RegisterCommand(new Commands.Handlers.ReminderCommand(_reminderService!, _scheduleStorageService!));
        
        // 今日は何の日コマンドの登録
        var todayService = new TodayService(_httpClient!);
        _commandService.RegisterCommand(new Commands.Handlers.TodayCommand(todayService));
        
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

        // リマインダーチェック: 1分ごとに実行
        if (frameCount % (_frameRate * 60) == 0 && frameCount > 0)
        {
            await CheckRemindersAsync();
        }

        // デバッグ用: 1秒ごとにログ出力
        if (frameCount % _frameRate == 0 && frameCount > 0)
        {
            Console.WriteLine($"[Update] フレーム: {frameCount}, デルタタイム: {deltaTime:F3}秒");
        }
        
        await Task.CompletedTask;
    }

    /// <summary>
    /// リマインダーをチェックして通知を送信
    /// </summary>
    private async Task CheckRemindersAsync()
    {
        if (_reminderService == null || _scheduleStorageService == null || _client == null)
            return;

        var now = DateTime.Now;
        var enabledReminders = _reminderService.GetEnabledReminders();

        foreach (var reminder in enabledReminders)
        {
            var poll = _scheduleStorageService.GetPollById(reminder.PollId);
            if (poll == null)
                continue;

            // 全員が参加可能な日程を取得
            var availableDates = _scheduleStorageService.GetDatesAvailableForAll(reminder.PollId);

            foreach (var dateStr in availableDates)
            {
                if (!DateTime.TryParse(dateStr, out var scheduleDate))
                    continue;

                // 現在時刻が開始時間の±5分以内かチェック
                var timeDiff = Math.Abs((now - scheduleDate).TotalMinutes);
                if (timeDiff <= 5)
                {
                    // 未通知の場合のみ通知
                    var shouldNotify = await _reminderService.MarkAsNotifiedAsync(reminder.PollId, dateStr);
                    if (shouldNotify)
                    {
                        await SendReminderNotificationAsync(reminder, dateStr, poll);
                    }
                }
            }
        }
    }

    /// <summary>
    /// リマインダー通知を送信
    /// </summary>
    private async Task SendReminderNotificationAsync(
        Models.ScheduleReminder reminder, 
        string dateStr, 
        Models.SchedulePoll poll)
    {
        try
        {
            var channel = _client?.GetChannel(reminder.ChannelId) as IMessageChannel;
            if (channel == null)
            {
                Console.WriteLine($"⚠️ リマインダー通知失敗: チャンネル {reminder.ChannelId} が見つかりません");
                return;
            }

            var date = DateTime.Parse(dateStr);
            var japaneseWeekDays = new[] { "日", "月", "火", "水", "木", "金", "土" };
            var weekDay = japaneseWeekDays[(int)date.DayOfWeek];

            await channel.SendMessageAsync(
                $"@everyone\n\n" +
                $"⏰ **スケジュールリマインダー** ⏰\n\n" +
                $"📅 **日時:** {dateStr} ({weekDay})\n" +
                $"✅ 全員が参加可能な日程です！\n\n" +
                $"Poll ID: `{poll.Id}`");

            Console.WriteLine($"✅ リマインダー通知送信: {dateStr} (Poll ID: {reminder.PollId})");
        }
        catch (Exception ex)
        {
            Console.WriteLine($"⚠️ リマインダー通知エラー: {ex.Message}");
        }
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

    private async Task ReactionAddedAsync(
        Cacheable<IUserMessage, ulong> cachedMessage,
        Cacheable<IMessageChannel, ulong> cachedChannel,
        SocketReaction reaction)
    {
        if (_reactionHandler != null)
        {
            await _reactionHandler.HandleReactionAddedAsync(cachedMessage, cachedChannel, reaction);
        }
    }

    private async Task ReactionRemovedAsync(
        Cacheable<IUserMessage, ulong> cachedMessage,
        Cacheable<IMessageChannel, ulong> cachedChannel,
        SocketReaction reaction)
    {
        if (_reactionHandler != null)
        {
            await _reactionHandler.HandleReactionRemovedAsync(cachedMessage, cachedChannel, reaction);
        }
    }
}
