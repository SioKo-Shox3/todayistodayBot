namespace TodayIsTodayBot.Models;

/// <summary>
/// スケジュールリマインダーのデータモデル
/// </summary>
public class ScheduleReminder
{
    /// <summary>
    /// リマインダーID（ユニーク）
    /// </summary>
    public string Id { get; set; } = string.Empty;

    /// <summary>
    /// 対象のアンケートID
    /// </summary>
    public string PollId { get; set; } = string.Empty;

    /// <summary>
    /// 通知先のチャンネルID
    /// </summary>
    public ulong ChannelId { get; set; }

    /// <summary>
    /// リマインダーが有効かどうか
    /// </summary>
    public bool IsEnabled { get; set; }

    /// <summary>
    /// 通知済みの日程リスト（重複通知を防ぐ）
    /// </summary>
    public List<string> NotifiedDates { get; set; } = new();

    /// <summary>
    /// 作成日時
    /// </summary>
    public DateTime CreatedAt { get; set; }

    /// <summary>
    /// リマインダーを作成したユーザーID
    /// </summary>
    public ulong CreatorId { get; set; }
}
