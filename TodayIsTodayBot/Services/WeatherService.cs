using System.Text.Json;

namespace TodayIsTodayBot.Services;

/// <summary>
/// 天気情報を取得するサービス（OpenWeatherMap API使用）
/// </summary>
public class WeatherService
{
    private readonly HttpClient _httpClient;
    private readonly string? _apiKey;
    private const string ApiBaseUrl = "https://api.openweathermap.org/data/2.5/weather";

    // 日本の主要都市マッピング
    private readonly Dictionary<string, string> _cityMapping = new()
    {
        // 都道府県名 → 英語都市名
        { "東京", "Tokyo" },
        { "大阪", "Osaka" },
        { "京都", "Kyoto" },
        { "名古屋", "Nagoya" },
        { "札幌", "Sapporo" },
        { "福岡", "Fukuoka" },
        { "仙台", "Sendai" },
        { "広島", "Hiroshima" },
        { "神戸", "Kobe" },
        { "横浜", "Yokohama" },
        { "北海道", "Sapporo" },
        { "青森", "Aomori" },
        { "岩手", "Morioka" },
        { "宮城", "Sendai" },
        { "秋田", "Akita" },
        { "山形", "Yamagata" },
        { "福島", "Fukushima" },
        { "茨城", "Mito" },
        { "栃木", "Utsunomiya" },
        { "群馬", "Maebashi" },
        { "埼玉", "Saitama" },
        { "千葉", "Chiba" },
        { "神奈川", "Yokohama" },
        { "新潟", "Niigata" },
        { "富山", "Toyama" },
        { "石川", "Kanazawa" },
        { "福井", "Fukui" },
        { "山梨", "Kofu" },
        { "長野", "Nagano" },
        { "岐阜", "Gifu" },
        { "静岡", "Shizuoka" },
        { "愛知", "Nagoya" },
        { "三重", "Tsu" },
        { "滋賀", "Otsu" },
        { "兵庫", "Kobe" },
        { "奈良", "Nara" },
        { "和歌山", "Wakayama" },
        { "鳥取", "Tottori" },
        { "島根", "Matsue" },
        { "岡山", "Okayama" },
        { "山口", "Yamaguchi" },
        { "徳島", "Tokushima" },
        { "香川", "Takamatsu" },
        { "愛媛", "Matsuyama" },
        { "高知", "Kochi" },
        { "佐賀", "Saga" },
        { "長崎", "Nagasaki" },
        { "熊本", "Kumamoto" },
        { "大分", "Oita" },
        { "宮崎", "Miyazaki" },
        { "鹿児島", "Kagoshima" },
        { "沖縄", "Naha" },
    };

    public WeatherService(HttpClient httpClient, string? apiKey)
    {
        _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        _apiKey = apiKey;
    }

    /// <summary>
    /// 指定された都市の天気情報を取得する
    /// </summary>
    /// <param name="cityName">都市名（日本語または英語）</param>
    /// <returns>天気情報の文字列</returns>
    public async Task<string> GetWeatherAsync(string cityName)
    {
        if (string.IsNullOrEmpty(_apiKey))
        {
            return "❌ OpenWeatherMap APIキーが設定されていません。appsettings.jsonでAPIキーを設定してください。\n" +
                   "無料APIキーは https://openweathermap.org/api で取得できます。";
        }

        // 日本語の都市名を英語に変換
        var englishCityName = _cityMapping.TryGetValue(cityName, out var mappedCity) 
            ? mappedCity 
            : cityName;

        try
        {
            var url = $"{ApiBaseUrl}?q={englishCityName},JP&appid={_apiKey}&units=metric&lang=ja";
            var response = await _httpClient.GetAsync(url);

            if (!response.IsSuccessStatusCode)
            {
                if (response.StatusCode == System.Net.HttpStatusCode.NotFound)
                {
                    return $"❌ 都市「{cityName}」が見つかりませんでした。都道府県名または主要都市名を指定してください。";
                }
                else if (response.StatusCode == System.Net.HttpStatusCode.Unauthorized)
                {
                    return "❌ APIキーが無効です。正しいAPIキーを設定してください。";
                }
                else
                {
                    return $"❌ 天気情報の取得に失敗しました。(ステータスコード: {response.StatusCode})";
                }
            }

            var jsonString = await response.Content.ReadAsStringAsync();
            var weatherData = JsonSerializer.Deserialize<WeatherApiResponse>(jsonString);

            if (weatherData == null)
            {
                return "❌ 天気情報の解析に失敗しました。";
            }

            return FormatWeatherMessage(weatherData, cityName);
        }
        catch (HttpRequestException ex)
        {
            return $"❌ 天気情報の取得中にネットワークエラーが発生しました: {ex.Message}";
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[WeatherService] エラー: {ex}");
            return $"❌ 天気情報の取得中にエラーが発生しました: {ex.Message}";
        }
    }

    /// <summary>
    /// 天気情報を見やすい形式にフォーマットする
    /// </summary>
    private string FormatWeatherMessage(WeatherApiResponse data, string requestedCity)
    {
        var weather = data.Weather?.FirstOrDefault();
        var weatherIcon = GetWeatherIcon(weather?.Main ?? "");
        
        var message = $"**{weatherIcon} {data.Name ?? requestedCity}の天気**\n\n";
        message += $"🌡️ **気温**: {data.Main?.Temp:F1}°C (体感: {data.Main?.FeelsLike:F1}°C)\n";
        message += $"📊 **状態**: {weather?.Description ?? "不明"}\n";
        message += $"💧 **湿度**: {data.Main?.Humidity}%\n";
        message += $"💨 **風速**: {data.Wind?.Speed:F1} m/s\n";
        
        if (data.Main?.TempMin != null && data.Main?.TempMax != null)
        {
            message += $"🌡️ **最低/最高**: {data.Main.TempMin:F1}°C / {data.Main.TempMax:F1}°C\n";
        }

        return message;
    }

    /// <summary>
    /// 天気の種類に応じた絵文字を返す
    /// </summary>
    private string GetWeatherIcon(string weatherMain)
    {
        return weatherMain.ToLower() switch
        {
            "clear" => "☀️",
            "clouds" => "☁️",
            "rain" => "🌧️",
            "drizzle" => "🌦️",
            "thunderstorm" => "⛈️",
            "snow" => "❄️",
            "mist" or "fog" or "haze" => "🌫️",
            _ => "🌤️"
        };
    }

    /// <summary>
    /// 利用可能な都市の一覧を取得
    /// </summary>
    public string GetAvailableCities()
    {
        var cities = string.Join(", ", _cityMapping.Keys.Take(20));
        return $"**利用可能な地域 (一部)**:\n{cities}... など\n\nまたは主要都市名を英語で指定できます。";
    }
}

// OpenWeatherMap APIレスポンスのデータモデル
public class WeatherApiResponse
{
    public string? Name { get; set; }
    public MainData? Main { get; set; }
    public WeatherData[]? Weather { get; set; }
    public WindData? Wind { get; set; }
}

public class MainData
{
    public double Temp { get; set; }
    public double FeelsLike { get; set; }
    public double TempMin { get; set; }
    public double TempMax { get; set; }
    public int Humidity { get; set; }
}

public class WeatherData
{
    public string? Main { get; set; }
    public string? Description { get; set; }
}

public class WindData
{
    public double Speed { get; set; }
}
