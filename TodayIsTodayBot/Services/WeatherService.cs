using System.Text.Json;
using System.Text.Json.Serialization;

namespace TodayIsTodayBot.Services;

/// <summary>
/// 天気情報を取得するサービス（Open-Meteo API使用）
/// </summary>
public class WeatherService
{
    private readonly HttpClient _httpClient;
    private const string ApiBaseUrl = "https://api.open-meteo.com/v1/forecast";

    // 日本の主要都市の緯度経度マッピング（日本語・英語両対応）
    private readonly Dictionary<string, (double Lat, double Lon)> _cityMapping;

    public WeatherService(HttpClient httpClient)
    {
        _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        
        // 都市マッピングの初期化
        _cityMapping = new(StringComparer.OrdinalIgnoreCase)
        {
            // 都道府県名・主要都市 → 緯度経度
            { "東京", (35.6895, 139.6917) },
            { "Tokyo", (35.6895, 139.6917) },
            { "大阪", (34.6937, 135.5023) },
            { "Osaka", (34.6937, 135.5023) },
            { "京都", (35.0116, 135.7681) },
            { "Kyoto", (35.0116, 135.7681) },
            { "名古屋", (35.1815, 136.9066) },
            { "Nagoya", (35.1815, 136.9066) },
            { "札幌", (43.0642, 141.3469) },
            { "Sapporo", (43.0642, 141.3469) },
            { "福岡", (33.5904, 130.4017) },
            { "Fukuoka", (33.5904, 130.4017) },
            { "仙台", (38.2682, 140.8694) },
            { "Sendai", (38.2682, 140.8694) },
            { "広島", (34.3853, 132.4553) },
            { "Hiroshima", (34.3853, 132.4553) },
            { "神戸", (34.6901, 135.1955) },
            { "Kobe", (34.6901, 135.1955) },
            { "横浜", (35.4437, 139.6380) },
            { "Yokohama", (35.4437, 139.6380) },
            { "北海道", (43.0642, 141.3469) },
            { "Hokkaido", (43.0642, 141.3469) },
            { "青森", (40.8246, 140.7400) },
            { "Aomori", (40.8246, 140.7400) },
            { "岩手", (39.7036, 141.1527) },
            { "Iwate", (39.7036, 141.1527) },
            { "Morioka", (39.7036, 141.1527) },
            { "宮城", (38.2682, 140.8694) },
            { "Miyagi", (38.2682, 140.8694) },
            { "秋田", (39.7186, 140.1024) },
            { "Akita", (39.7186, 140.1024) },
            { "山形", (38.2404, 140.3633) },
            { "Yamagata", (38.2404, 140.3633) },
            { "福島", (37.7500, 140.4676) },
            { "Fukushima", (37.7500, 140.4676) },
            { "茨城", (36.3418, 140.4468) },
            { "Ibaraki", (36.3418, 140.4468) },
            { "Mito", (36.3418, 140.4468) },
            { "栃木", (36.5658, 139.8836) },
            { "Tochigi", (36.5658, 139.8836) },
            { "Utsunomiya", (36.5658, 139.8836) },
            { "群馬", (36.3911, 139.0608) },
            { "Gunma", (36.3911, 139.0608) },
            { "Maebashi", (36.3911, 139.0608) },
            { "埼玉", (35.8569, 139.6489) },
            { "Saitama", (35.8569, 139.6489) },
            { "千葉", (35.6074, 140.1065) },
            { "Chiba", (35.6074, 140.1065) },
            { "神奈川", (35.4437, 139.6380) },
            { "Kanagawa", (35.4437, 139.6380) },
            { "新潟", (37.9161, 139.0364) },
            { "Niigata", (37.9161, 139.0364) },
            { "富山", (36.6959, 137.2137) },
            { "Toyama", (36.6959, 137.2137) },
            { "石川", (36.5946, 136.6256) },
            { "Ishikawa", (36.5946, 136.6256) },
            { "Kanazawa", (36.5946, 136.6256) },
            { "福井", (36.0652, 136.2216) },
            { "Fukui", (36.0652, 136.2216) },
            { "山梨", (35.6635, 138.5684) },
            { "Yamanashi", (35.6635, 138.5684) },
            { "Kofu", (35.6635, 138.5684) },
            { "長野", (36.6513, 138.1810) },
            { "Nagano", (36.6513, 138.1810) },
            { "岐阜", (35.3912, 136.7222) },
            { "Gifu", (35.3912, 136.7222) },
            { "静岡", (34.9756, 138.3828) },
            { "Shizuoka", (34.9756, 138.3828) },
            { "愛知", (35.1815, 136.9066) },
            { "Aichi", (35.1815, 136.9066) },
            { "三重", (34.7303, 136.5086) },
            { "Mie", (34.7303, 136.5086) },
            { "Tsu", (34.7303, 136.5086) },
            { "滋賀", (35.0045, 135.8686) },
            { "Shiga", (35.0045, 135.8686) },
            { "Otsu", (35.0045, 135.8686) },
            { "兵庫", (34.6901, 135.1955) },
            { "Hyogo", (34.6901, 135.1955) },
            { "奈良", (34.6851, 135.8050) },
            { "Nara", (34.6851, 135.8050) },
            { "和歌山", (34.2261, 135.1675) },
            { "Wakayama", (34.2261, 135.1675) },
            { "鳥取", (35.5014, 134.2350) },
            { "Tottori", (35.5014, 134.2350) },
            { "島根", (35.4723, 133.0505) },
            { "Shimane", (35.4723, 133.0505) },
            { "Matsue", (35.4723, 133.0505) },
            { "岡山", (34.6617, 133.9350) },
            { "Okayama", (34.6617, 133.9350) },
            { "山口", (34.1861, 131.4714) },
            { "Yamaguchi", (34.1861, 131.4714) },
            { "徳島", (34.0658, 134.5595) },
            { "Tokushima", (34.0658, 134.5595) },
            { "香川", (34.3401, 134.0433) },
            { "Kagawa", (34.3401, 134.0433) },
            { "Takamatsu", (34.3401, 134.0433) },
            { "愛媛", (33.8416, 132.7657) },
            { "Ehime", (33.8416, 132.7657) },
            { "Matsuyama", (33.8416, 132.7657) },
            { "高知", (33.5597, 133.5311) },
            { "Kochi", (33.5597, 133.5311) },
            { "佐賀", (33.2494, 130.2989) },
            { "Saga", (33.2494, 130.2989) },
            { "長崎", (32.7503, 129.8779) },
            { "Nagasaki", (32.7503, 129.8779) },
            { "熊本", (32.7898, 130.7417) },
            { "Kumamoto", (32.7898, 130.7417) },
            { "大分", (33.2382, 131.6126) },
            { "Oita", (33.2382, 131.6126) },
            { "宮崎", (31.9077, 131.4202) },
            { "Miyazaki", (31.9077, 131.4202) },
            { "鹿児島", (31.5602, 130.5581) },
            { "Kagoshima", (31.5602, 130.5581) },
            { "沖縄", (26.2124, 127.6809) },
            { "Okinawa", (26.2124, 127.6809) },
            { "Naha", (26.2124, 127.6809) },
        };
    }

    /// <summary>
    /// 指定された都市の天気情報を取得する
    /// </summary>
    /// <param name="cityName">都市名（日本語または英語）</param>
    /// <returns>天気情報の文字列</returns>
    public async Task<string> GetWeatherAsync(string cityName)
    {
        // 都市名から緯度経度を取得
        if (!_cityMapping.TryGetValue(cityName, out var location))
        {
            return $"❌ 都市「{cityName}」が見つかりませんでした。都道府県名または主要都市名を指定してください。\n\n" +
                   GetAvailableCities();
        }

        try
        {
            // Open-Meteo APIにリクエスト（current=temperature_2m,relative_humidity_2m,apparent_temperature,weather_code,wind_speed_10m）
            var url = $"{ApiBaseUrl}?latitude={location.Lat}&longitude={location.Lon}" +
                      "&current=temperature_2m,relative_humidity_2m,apparent_temperature,weather_code,wind_speed_10m" +
                      "&timezone=Asia/Tokyo";
            
            var response = await _httpClient.GetAsync(url);

            if (!response.IsSuccessStatusCode)
            {
                return $"❌ 天気情報の取得に失敗しました。(ステータスコード: {response.StatusCode})";
            }

            var jsonString = await response.Content.ReadAsStringAsync();
            var weatherData = JsonSerializer.Deserialize<OpenMeteoResponse>(jsonString);

            if (weatherData?.Current == null)
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
    private string FormatWeatherMessage(OpenMeteoResponse data, string requestedCity)
    {
        var current = data.Current!;
        var weatherIcon = GetWeatherIcon(current.WeatherCode);
        var weatherDescription = GetWeatherDescription(current.WeatherCode);
        
        var message = $"**{weatherIcon} {requestedCity}の天気**\n\n";
        message += $"🌡️ **気温**: {current.Temperature:F1}°C (体感: {current.ApparentTemperature:F1}°C)\n";
        message += $"📊 **状態**: {weatherDescription}\n";
        message += $"💧 **湿度**: {current.RelativeHumidity}%\n";
        message += $"💨 **風速**: {current.WindSpeed:F1} m/s\n";

        return message;
    }

    /// <summary>
    /// WMO Weather Codeから天気の説明を取得
    /// </summary>
    private string GetWeatherDescription(int code)
    {
        return code switch
        {
            0 => "快晴",
            1 => "ほぼ晴れ",
            2 => "部分的に曇り",
            3 => "曇り",
            45 or 48 => "霧",
            51 or 53 or 55 => "霧雨",
            56 or 57 => "凍る霧雨",
            61 or 63 or 65 => "雨",
            66 or 67 => "凍る雨",
            71 or 73 or 75 => "雪",
            77 => "みぞれ",
            80 or 81 or 82 => "にわか雨",
            85 or 86 => "にわか雪",
            95 => "雷雨",
            96 or 99 => "雹を伴う雷雨",
            _ => "不明"
        };
    }

    /// <summary>
    /// WMO Weather Codeから天気に応じた絵文字を返す
    /// </summary>
    private string GetWeatherIcon(int code)
    {
        return code switch
        {
            0 => "☀️",
            1 or 2 => "🌤️",
            3 => "☁️",
            45 or 48 => "🌫️",
            51 or 53 or 55 or 56 or 57 => "🌦️",
            61 or 63 or 65 or 66 or 67 or 80 or 81 or 82 => "🌧️",
            71 or 73 or 75 or 77 or 85 or 86 => "❄️",
            95 or 96 or 99 => "⛈️",
            _ => "🌤️"
        };
    }

    /// <summary>
    /// 利用可能な都市の一覧を取得
    /// </summary>
    public string GetAvailableCities()
    {
        var cities = string.Join(", ", _cityMapping.Keys.Take(20));
        return $"**利用可能な地域 (一部)**:\n{cities}... など";
    }
}

// Open-Meteo APIレスポンスのデータモデル
public class OpenMeteoResponse
{
    [JsonPropertyName("current")]
    public CurrentWeather? Current { get; set; }
}

public class CurrentWeather
{
    [JsonPropertyName("temperature_2m")]
    public double Temperature { get; set; }
    
    [JsonPropertyName("apparent_temperature")]
    public double ApparentTemperature { get; set; }
    
    [JsonPropertyName("relative_humidity_2m")]
    public int RelativeHumidity { get; set; }
    
    [JsonPropertyName("weather_code")]
    public int WeatherCode { get; set; }
    
    [JsonPropertyName("wind_speed_10m")]
    public double WindSpeed { get; set; }
}
