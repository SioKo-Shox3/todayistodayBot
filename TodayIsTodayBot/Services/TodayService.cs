using System.Text.Json;
using System.Text.Json.Serialization;

namespace TodayIsTodayBot.Services;

/// <summary>
/// 今日は何の日APIから情報を取得するサービス
/// </summary>
public class TodayService
{
    private readonly HttpClient _httpClient;
    private const string AnnivApiBaseUrl = "https://api.whatistoday.cyou/v3/anniv/";
    private const string BirthflowerApiBaseUrl = "https://api.whatistoday.cyou/v3/birthflower/";
    private const string FamousBirthdayApiBaseUrl = "https://api.whatistoday.cyou/v3/famousbirthday/";

    public TodayService(HttpClient httpClient)
    {
        _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
    }

    /// <summary>
    /// 今日の記念日情報を取得する
    /// </summary>
    public async Task<AnniversaryResponse?> GetAnniversaryAsync(DateTime? date = null)
    {
        var targetDate = date ?? DateTime.Today;
        var mmdd = targetDate.ToString("MMdd");
        var url = $"{AnnivApiBaseUrl}{mmdd}";

        try
        {
            var response = await _httpClient.GetAsync(url);
            response.EnsureSuccessStatusCode();

            var json = await response.Content.ReadAsStringAsync();
            return JsonSerializer.Deserialize<AnniversaryResponse>(json, new JsonSerializerOptions
            {
                PropertyNameCaseInsensitive = true
            });
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[TodayService] 記念日API取得エラー: {ex.Message}");
            throw;
        }
    }

    /// <summary>
    /// 今日の誕生花情報を取得する
    /// </summary>
    public async Task<BirthflowerResponse?> GetBirthflowerAsync(DateTime? date = null)
    {
        var targetDate = date ?? DateTime.Today;
        var mmdd = targetDate.ToString("MMdd");
        var url = $"{BirthflowerApiBaseUrl}{mmdd}";

        try
        {
            var response = await _httpClient.GetAsync(url);
            response.EnsureSuccessStatusCode();

            var json = await response.Content.ReadAsStringAsync();
            return JsonSerializer.Deserialize<BirthflowerResponse>(json, new JsonSerializerOptions
            {
                PropertyNameCaseInsensitive = true
            });
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[TodayService] 誕生花API取得エラー: {ex.Message}");
            throw;
        }
    }

    /// <summary>
    /// 今日が誕生日の偉人情報を取得する
    /// </summary>
    public async Task<FamousBirthdayResponse?> GetFamousBirthdayAsync(DateTime? date = null)
    {
        var targetDate = date ?? DateTime.Today;
        var mmdd = targetDate.ToString("MMdd");
        var url = $"{FamousBirthdayApiBaseUrl}{mmdd}";

        try
        {
            var response = await _httpClient.GetAsync(url);
            response.EnsureSuccessStatusCode();

            var json = await response.Content.ReadAsStringAsync();
            return JsonSerializer.Deserialize<FamousBirthdayResponse>(json, new JsonSerializerOptions
            {
                PropertyNameCaseInsensitive = true
            });
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[TodayService] 偉人誕生日API取得エラー: {ex.Message}");
            throw;
        }
    }

    /// <summary>
    /// 今日は何の日の全情報をまとめて取得してフォーマットする
    /// </summary>
    public async Task<string> GetTodayInfoAsync(DateTime? date = null)
    {
        var targetDate = date ?? DateTime.Today;
        var dateStr = targetDate.ToString("M月d日");

        var tasks = new Task<object?>[]
        {
            GetAnniversaryAsync(targetDate).ContinueWith(t => (object?)t.Result),
            GetBirthflowerAsync(targetDate).ContinueWith(t => (object?)t.Result),
            GetFamousBirthdayAsync(targetDate).ContinueWith(t => (object?)t.Result)
        };

        try
        {
            await Task.WhenAll(tasks);
        }
        catch
        {
            // 個別のエラーは後で処理
        }

        var anniversary = tasks[0].Result as AnniversaryResponse;
        var birthflower = tasks[1].Result as BirthflowerResponse;
        var famousBirthday = tasks[2].Result as FamousBirthdayResponse;

        var result = $"📅 **{dateStr}は何の日？**\n\n";

        // 記念日情報
        if (anniversary != null)
        {
            result += "🎉 **記念日**\n";
            var anniversaries = new List<string>();
            
            if (!string.IsNullOrEmpty(anniversary.Anniv1)) anniversaries.Add(anniversary.Anniv1);
            if (!string.IsNullOrEmpty(anniversary.Anniv2)) anniversaries.Add(anniversary.Anniv2);
            if (!string.IsNullOrEmpty(anniversary.Anniv3)) anniversaries.Add(anniversary.Anniv3);
            if (!string.IsNullOrEmpty(anniversary.Anniv4)) anniversaries.Add(anniversary.Anniv4);
            if (!string.IsNullOrEmpty(anniversary.Anniv5)) anniversaries.Add(anniversary.Anniv5);

            if (anniversaries.Count > 0)
            {
                foreach (var anniv in anniversaries)
                {
                    result += $"  • {anniv}\n";
                }
            }
            else
            {
                result += "  （情報なし）\n";
            }
            result += "\n";
        }

        // 誕生花情報
        if (birthflower != null && !string.IsNullOrEmpty(birthflower.Flower))
        {
            result += "🌸 **誕生花**\n";
            result += $"  • {birthflower.Flower}";
            if (!string.IsNullOrEmpty(birthflower.Lang))
            {
                result += $"（花言葉: {birthflower.Lang}）";
            }
            result += "\n\n";
        }

        // 偉人誕生日情報
        if (famousBirthday != null && !string.IsNullOrEmpty(famousBirthday.Name))
        {
            result += "👤 **この日生まれの偉人**\n";
            result += $"  • {famousBirthday.Name}";
            if (!string.IsNullOrEmpty(famousBirthday.Profile))
            {
                result += $"（{famousBirthday.Profile}）";
            }
            if (!string.IsNullOrEmpty(famousBirthday.Lifespan))
            {
                result += $" [{famousBirthday.Lifespan}]";
            }
            result += "\n";
        }

        result += "\n_Powered by [whatistoday API](https://note.com/sooz/n/naffb68c7f53b)_";

        return result;
    }
}

/// <summary>
/// 記念日APIのレスポンス
/// </summary>
public class AnniversaryResponse
{
    [JsonPropertyName("id")]
    public int Id { get; set; }

    [JsonPropertyName("mmdd")]
    public string? Mmdd { get; set; }

    [JsonPropertyName("anniv1")]
    public string? Anniv1 { get; set; }

    [JsonPropertyName("anniv2")]
    public string? Anniv2 { get; set; }

    [JsonPropertyName("anniv3")]
    public string? Anniv3 { get; set; }

    [JsonPropertyName("anniv4")]
    public string? Anniv4 { get; set; }

    [JsonPropertyName("anniv5")]
    public string? Anniv5 { get; set; }
}

/// <summary>
/// 誕生花APIのレスポンス
/// </summary>
public class BirthflowerResponse
{
    [JsonPropertyName("id")]
    public int Id { get; set; }

    [JsonPropertyName("mmdd")]
    public string? Mmdd { get; set; }

    [JsonPropertyName("flower")]
    public string? Flower { get; set; }

    [JsonPropertyName("lang")]
    public string? Lang { get; set; }
}

/// <summary>
/// 偉人誕生日APIのレスポンス
/// </summary>
public class FamousBirthdayResponse
{
    [JsonPropertyName("id")]
    public int Id { get; set; }

    [JsonPropertyName("mmdd")]
    public string? Mmdd { get; set; }

    [JsonPropertyName("lifespan")]
    public string? Lifespan { get; set; }

    [JsonPropertyName("name")]
    public string? Name { get; set; }

    [JsonPropertyName("profile")]
    public string? Profile { get; set; }
}
