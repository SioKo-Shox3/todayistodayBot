using System.Text.Json;
using TodayIsTodayBot.Models;

namespace TodayIsTodayBot.Services;

/// <summary>
/// スケジュールリマインダーを管理するサービス
/// </summary>
public class ReminderService
{
    private readonly string _dataFilePath;
    private readonly SemaphoreSlim _semaphore = new(1, 1);
    private Dictionary<string, ScheduleReminder> _reminders = new();

    public ReminderService(string dataFilePath = "reminders.json")
    {
        _dataFilePath = dataFilePath;
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
                _reminders = JsonSerializer.Deserialize<Dictionary<string, ScheduleReminder>>(json) 
                    ?? new Dictionary<string, ScheduleReminder>();
            }
        }
        catch (Exception ex)
        {
            Console.WriteLine($"⚠️ リマインダーデータの読み込みエラー: {ex.Message}");
            _reminders = new Dictionary<string, ScheduleReminder>();
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
            var json = JsonSerializer.Serialize(_reminders, new JsonSerializerOptions 
            { 
                WriteIndented = true 
            });
            await File.WriteAllTextAsync(_dataFilePath, json);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"⚠️ リマインダーデータの保存エラー: {ex.Message}");
        }
        finally
        {
            _semaphore.Release();
        }
    }

    /// <summary>
    /// リマインダーを作成する
    /// </summary>
    public async Task<ScheduleReminder> CreateReminderAsync(string pollId, ulong channelId, ulong creatorId)
    {
        // 既存のリマインダーがあれば更新
        var existingReminder = _reminders.Values.FirstOrDefault(r => r.PollId == pollId);
        if (existingReminder != null)
        {
            existingReminder.ChannelId = channelId;
            existingReminder.CreatorId = creatorId;
            await SaveDataAsync();
            return existingReminder;
        }

        // 新規作成
        var reminder = new ScheduleReminder
        {
            Id = Guid.NewGuid().ToString(),
            PollId = pollId,
            ChannelId = channelId,
            CreatorId = creatorId,
            IsEnabled = false,
            CreatedAt = DateTime.UtcNow
        };

        _reminders[reminder.Id] = reminder;
        await SaveDataAsync();
        return reminder;
    }

    /// <summary>
    /// リマインダーを有効化する
    /// </summary>
    public async Task<bool> EnableReminderAsync(string pollId)
    {
        var reminder = _reminders.Values.FirstOrDefault(r => r.PollId == pollId);
        if (reminder == null)
            return false;

        reminder.IsEnabled = true;
        await SaveDataAsync();
        return true;
    }

    /// <summary>
    /// リマインダーを無効化する
    /// </summary>
    public async Task<bool> DisableReminderAsync(string pollId)
    {
        var reminder = _reminders.Values.FirstOrDefault(r => r.PollId == pollId);
        if (reminder == null)
            return false;

        reminder.IsEnabled = false;
        await SaveDataAsync();
        return true;
    }

    /// <summary>
    /// すべての有効なリマインダーを取得する
    /// </summary>
    public IEnumerable<ScheduleReminder> GetEnabledReminders()
    {
        return _reminders.Values.Where(r => r.IsEnabled);
    }

    /// <summary>
    /// すべてのリマインダーを取得する
    /// </summary>
    public IEnumerable<ScheduleReminder> GetAllReminders()
    {
        return _reminders.Values;
    }

    /// <summary>
    /// アンケートIDからリマインダーを取得する
    /// </summary>
    public ScheduleReminder? GetReminderByPollId(string pollId)
    {
        return _reminders.Values.FirstOrDefault(r => r.PollId == pollId);
    }

    /// <summary>
    /// 日程が通知済みかどうかをチェックし、未通知の場合は記録する
    /// </summary>
    public async Task<bool> MarkAsNotifiedAsync(string pollId, string date)
    {
        var reminder = _reminders.Values.FirstOrDefault(r => r.PollId == pollId);
        if (reminder == null)
            return false;

        if (reminder.NotifiedDates.Contains(date))
            return false; // 既に通知済み

        reminder.NotifiedDates.Add(date);
        await SaveDataAsync();
        return true; // 新規通知
    }

    /// <summary>
    /// リマインダーを削除する
    /// </summary>
    public async Task<bool> DeleteReminderAsync(string pollId)
    {
        var reminder = _reminders.Values.FirstOrDefault(r => r.PollId == pollId);
        if (reminder == null)
            return false;

        _reminders.Remove(reminder.Id);
        await SaveDataAsync();
        return true;
    }
}
