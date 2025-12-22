using System.Text.Json;
using TodayIsTodayBot.Models;

namespace TodayIsTodayBot.Services;

/// <summary>
/// 日程調整アンケートのデータをJSONファイルに保存/読み込みするサービス
/// </summary>
public class ScheduleStorageService
{
    private readonly string _dataFilePath;
    private readonly SemaphoreSlim _semaphore = new(1, 1);
    private Dictionary<string, SchedulePoll> _polls = new();

    public ScheduleStorageService(string? dataFilePath = null)
    {
        if (string.IsNullOrEmpty(dataFilePath))
        {
            // 実行ファイルのディレクトリを基準にパスを設定
            var appDirectory = AppDomain.CurrentDomain.BaseDirectory;
            var dataDirectory = Path.Combine(appDirectory, "data");
            
            try
            {
                if (!Directory.Exists(dataDirectory))
                {
                    Directory.CreateDirectory(dataDirectory);
                    Console.WriteLine($"✅ データディレクトリを作成しました: {dataDirectory}");
                }
            }
            catch (Exception ex)
            {
                Console.WriteLine($"⚠️ データディレクトリの作成に失敗しました: {ex.Message}");
                // ディレクトリが作成できない場合は実行ディレクトリ直下に保存
                dataDirectory = appDirectory;
            }
            
            _dataFilePath = Path.Combine(dataDirectory, "schedules.json");
        }
        else
        {
            _dataFilePath = dataFilePath;
        }
        Console.WriteLine($"📁 スケジュールデータファイル: {_dataFilePath}");
        LoadData();
    }

    /// <summary>
    /// ファイルからデータを読み込む
    /// </summary>
    private void LoadData()
    {
        try
        {
            if (File.Exists(_dataFilePath))
            {
                var json = File.ReadAllText(_dataFilePath);
                _polls = JsonSerializer.Deserialize<Dictionary<string, SchedulePoll>>(json) 
                    ?? new Dictionary<string, SchedulePoll>();
                Console.WriteLine($"✅ スケジュールデータを読み込みました ({_polls.Count}件)");
            }
            else
            {
                // ファイルが存在しない場合は空のJSONファイルを作成
                Console.WriteLine("⚠️ schedules.json が見つかりません。新規作成します...");
                _polls = new Dictionary<string, SchedulePoll>();
                File.WriteAllText(_dataFilePath, "{}");
                Console.WriteLine($"✅ schedules.json を作成しました: {_dataFilePath}");
            }
        }
        catch (Exception ex)
        {
            Console.WriteLine($"⚠️ スケジュールデータの読み込みエラー: {ex.Message}");
            _polls = new Dictionary<string, SchedulePoll>();
        }
    }

    /// <summary>
    /// データをファイルに保存する
    /// </summary>
    private async Task SaveDataAsync()
    {
        await _semaphore.WaitAsync();
        try
        {
            var json = JsonSerializer.Serialize(_polls, new JsonSerializerOptions 
            { 
                WriteIndented = true 
            });
            await File.WriteAllTextAsync(_dataFilePath, json);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"⚠️ スケジュールデータの保存エラー: {ex.Message}");
        }
        finally
        {
            _semaphore.Release();
        }
    }

    /// <summary>
    /// 新しいアンケートを作成する
    /// </summary>
    public async Task<SchedulePoll> CreatePollAsync(ulong creatorId, ulong messageId, ulong channelId, List<string> dateOptions)
    {
        var poll = new SchedulePoll
        {
            Id = Guid.NewGuid().ToString(),
            CreatorId = creatorId,
            MessageId = messageId,
            ChannelId = channelId,
            DateOptions = dateOptions,
            CreatedAt = DateTime.UtcNow
        };

        // 各日程に対して空の投票リストを初期化
        foreach (var date in dateOptions)
        {
            poll.Votes[date] = new List<ulong>();
        }

        _polls[poll.Id] = poll;
        await SaveDataAsync();
        return poll;
    }

    /// <summary>
    /// メッセージIDからアンケートを取得する
    /// </summary>
    public SchedulePoll? GetPollByMessageId(ulong messageId)
    {
        return _polls.Values.FirstOrDefault(p => p.MessageId == messageId);
    }

    /// <summary>
    /// IDからアンケートを取得する
    /// </summary>
    public SchedulePoll? GetPollById(string pollId)
    {
        return _polls.ContainsKey(pollId) ? _polls[pollId] : null;
    }

    /// <summary>
    /// すべてのアンケートを取得する
    /// </summary>
    public IEnumerable<SchedulePoll> GetAllPolls()
    {
        return _polls.Values;
    }

    /// <summary>
    /// チャンネルIDで最新のアンケートを取得する
    /// </summary>
    public SchedulePoll? GetLatestPollByChannelId(ulong channelId)
    {
        return _polls.Values
            .Where(p => p.ChannelId == channelId)
            .OrderByDescending(p => p.CreatedAt)
            .FirstOrDefault();
    }

    /// <summary>
    /// 投票を追加する
    /// </summary>
    public async Task AddVoteAsync(string pollId, string dateOption, ulong userId)
    {
        if (!_polls.ContainsKey(pollId))
            return;

        var poll = _polls[pollId];
        if (!poll.Votes.ContainsKey(dateOption))
            return;

        if (!poll.Votes[dateOption].Contains(userId))
        {
            poll.Votes[dateOption].Add(userId);
            await SaveDataAsync();
        }
    }

    /// <summary>
    /// 投票を削除する
    /// </summary>
    public async Task RemoveVoteAsync(string pollId, string dateOption, ulong userId)
    {
        if (!_polls.ContainsKey(pollId))
            return;

        var poll = _polls[pollId];
        if (!poll.Votes.ContainsKey(dateOption))
            return;

        if (poll.Votes[dateOption].Contains(userId))
        {
            poll.Votes[dateOption].Remove(userId);
            await SaveDataAsync();
        }
    }

    /// <summary>
    /// 全員が参加可能な日程を取得する
    /// </summary>
    public List<string> GetDatesAvailableForAll(string pollId)
    {
        if (!_polls.ContainsKey(pollId))
            return new List<string>();

        var poll = _polls[pollId];
        
        // 投票した全ユーザーを取得
        var allVoters = poll.Votes.Values
            .SelectMany(v => v)
            .Distinct()
            .ToHashSet();

        if (allVoters.Count == 0)
            return new List<string>();

        // 全員が投票した日程を検索
        var availableDates = new List<string>();
        foreach (var date in poll.DateOptions)
        {
            var voters = poll.Votes[date].ToHashSet();
            if (allVoters.SetEquals(voters))
            {
                availableDates.Add(date);
            }
        }

        return availableDates;
    }

    /// <summary>
    /// アンケートを削除する
    /// </summary>
    public async Task DeletePollAsync(string pollId)
    {
        if (_polls.ContainsKey(pollId))
        {
            _polls.Remove(pollId);
            await SaveDataAsync();
        }
    }
}
