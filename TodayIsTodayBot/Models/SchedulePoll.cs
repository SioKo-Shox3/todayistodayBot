namespace TodayIsTodayBot.Models;

/// <summary>
/// 日程調整アンケートのデータモデル
/// </summary>
public class SchedulePoll
{
    /// <summary>
    /// アンケートID（ユニーク）
    /// </summary>
    public string Id { get; set; } = string.Empty;

    /// <summary>
    /// アンケートを作成したユーザーID
    /// </summary>
    public ulong CreatorId { get; set; }

    /// <summary>
    /// アンケートのメッセージID
    /// </summary>
    public ulong MessageId { get; set; }

    /// <summary>
    /// アンケートのチャンネルID
    /// </summary>
    public ulong ChannelId { get; set; }

    /// <summary>
    /// 日程候補のリスト（"2025-01-15 19:00"形式）
    /// </summary>
    public List<string> DateOptions { get; set; } = new();

    /// <summary>
    /// 各日程への投票結果（DateOption -> List of UserIds）
    /// </summary>
    public Dictionary<string, List<ulong>> Votes { get; set; } = new();

    /// <summary>
    /// アンケート作成日時
    /// </summary>
    public DateTime CreatedAt { get; set; }
}
