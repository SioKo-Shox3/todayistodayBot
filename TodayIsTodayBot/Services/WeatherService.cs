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

    // 都市の緯度経度・タイムゾーンマッピング（日本語・英語両対応）
    private readonly Dictionary<string, (double Lat, double Lon, string Timezone)> _cityMapping;

    public WeatherService(HttpClient httpClient)
    {
        _httpClient = httpClient ?? throw new ArgumentNullException(nameof(httpClient));
        
        // 都市マッピングの初期化
        _cityMapping = new(StringComparer.OrdinalIgnoreCase)
        {
            // 日本の都道府県名・主要都市 → 緯度経度・タイムゾーン
            { "東京", (35.6895, 139.6917, "Asia/Tokyo") },
            { "Tokyo", (35.6895, 139.6917, "Asia/Tokyo") },
            { "大阪", (34.6937, 135.5023, "Asia/Tokyo") },
            { "Osaka", (34.6937, 135.5023, "Asia/Tokyo") },
            { "京都", (35.0116, 135.7681, "Asia/Tokyo") },
            { "Kyoto", (35.0116, 135.7681, "Asia/Tokyo") },
            { "名古屋", (35.1815, 136.9066, "Asia/Tokyo") },
            { "Nagoya", (35.1815, 136.9066, "Asia/Tokyo") },
            { "札幌", (43.0642, 141.3469, "Asia/Tokyo") },
            { "Sapporo", (43.0642, 141.3469, "Asia/Tokyo") },
            { "福岡", (33.5904, 130.4017, "Asia/Tokyo") },
            { "Fukuoka", (33.5904, 130.4017, "Asia/Tokyo") },
            { "仙台", (38.2682, 140.8694, "Asia/Tokyo") },
            { "Sendai", (38.2682, 140.8694, "Asia/Tokyo") },
            { "広島", (34.3853, 132.4553, "Asia/Tokyo") },
            { "Hiroshima", (34.3853, 132.4553, "Asia/Tokyo") },
            { "神戸", (34.6901, 135.1955, "Asia/Tokyo") },
            { "Kobe", (34.6901, 135.1955, "Asia/Tokyo") },
            { "横浜", (35.4437, 139.6380, "Asia/Tokyo") },
            { "Yokohama", (35.4437, 139.6380, "Asia/Tokyo") },
            { "北海道", (43.0642, 141.3469, "Asia/Tokyo") },
            { "Hokkaido", (43.0642, 141.3469, "Asia/Tokyo") },
            { "青森", (40.8246, 140.7400, "Asia/Tokyo") },
            { "Aomori", (40.8246, 140.7400, "Asia/Tokyo") },
            { "岩手", (39.7036, 141.1527, "Asia/Tokyo") },
            { "Iwate", (39.7036, 141.1527, "Asia/Tokyo") },
            { "Morioka", (39.7036, 141.1527, "Asia/Tokyo") },
            { "宮城", (38.2682, 140.8694, "Asia/Tokyo") },
            { "Miyagi", (38.2682, 140.8694, "Asia/Tokyo") },
            { "秋田", (39.7186, 140.1024, "Asia/Tokyo") },
            { "Akita", (39.7186, 140.1024, "Asia/Tokyo") },
            { "山形", (38.2404, 140.3633, "Asia/Tokyo") },
            { "Yamagata", (38.2404, 140.3633, "Asia/Tokyo") },
            { "福島", (37.7500, 140.4676, "Asia/Tokyo") },
            { "Fukushima", (37.7500, 140.4676, "Asia/Tokyo") },
            { "茨城", (36.3418, 140.4468, "Asia/Tokyo") },
            { "Ibaraki", (36.3418, 140.4468, "Asia/Tokyo") },
            { "Mito", (36.3418, 140.4468, "Asia/Tokyo") },
            { "栃木", (36.5658, 139.8836, "Asia/Tokyo") },
            { "Tochigi", (36.5658, 139.8836, "Asia/Tokyo") },
            { "Utsunomiya", (36.5658, 139.8836, "Asia/Tokyo") },
            { "群馬", (36.3911, 139.0608, "Asia/Tokyo") },
            { "Gunma", (36.3911, 139.0608, "Asia/Tokyo") },
            { "Maebashi", (36.3911, 139.0608, "Asia/Tokyo") },
            { "埼玉", (35.8569, 139.6489, "Asia/Tokyo") },
            { "Saitama", (35.8569, 139.6489, "Asia/Tokyo") },
            { "千葉", (35.6074, 140.1065, "Asia/Tokyo") },
            { "Chiba", (35.6074, 140.1065, "Asia/Tokyo") },
            { "神奈川", (35.4437, 139.6380, "Asia/Tokyo") },
            { "Kanagawa", (35.4437, 139.6380, "Asia/Tokyo") },
            { "新潟", (37.9161, 139.0364, "Asia/Tokyo") },
            { "Niigata", (37.9161, 139.0364, "Asia/Tokyo") },
            { "富山", (36.6959, 137.2137, "Asia/Tokyo") },
            { "Toyama", (36.6959, 137.2137, "Asia/Tokyo") },
            { "石川", (36.5946, 136.6256, "Asia/Tokyo") },
            { "Ishikawa", (36.5946, 136.6256, "Asia/Tokyo") },
            { "Kanazawa", (36.5946, 136.6256, "Asia/Tokyo") },
            { "福井", (36.0652, 136.2216, "Asia/Tokyo") },
            { "Fukui", (36.0652, 136.2216, "Asia/Tokyo") },
            { "山梨", (35.6635, 138.5684, "Asia/Tokyo") },
            { "Yamanashi", (35.6635, 138.5684, "Asia/Tokyo") },
            { "Kofu", (35.6635, 138.5684, "Asia/Tokyo") },
            { "長野", (36.6513, 138.1810, "Asia/Tokyo") },
            { "Nagano", (36.6513, 138.1810, "Asia/Tokyo") },
            { "岐阜", (35.3912, 136.7222, "Asia/Tokyo") },
            { "Gifu", (35.3912, 136.7222, "Asia/Tokyo") },
            { "静岡", (34.9756, 138.3828, "Asia/Tokyo") },
            { "Shizuoka", (34.9756, 138.3828, "Asia/Tokyo") },
            { "愛知", (35.1815, 136.9066, "Asia/Tokyo") },
            { "Aichi", (35.1815, 136.9066, "Asia/Tokyo") },
            { "三重", (34.7303, 136.5086, "Asia/Tokyo") },
            { "Mie", (34.7303, 136.5086, "Asia/Tokyo") },
            { "Tsu", (34.7303, 136.5086, "Asia/Tokyo") },
            { "滋賀", (35.0045, 135.8686, "Asia/Tokyo") },
            { "Shiga", (35.0045, 135.8686, "Asia/Tokyo") },
            { "Otsu", (35.0045, 135.8686, "Asia/Tokyo") },
            { "兵庫", (34.6901, 135.1955, "Asia/Tokyo") },
            { "Hyogo", (34.6901, 135.1955, "Asia/Tokyo") },
            { "奈良", (34.6851, 135.8050, "Asia/Tokyo") },
            { "Nara", (34.6851, 135.8050, "Asia/Tokyo") },
            { "和歌山", (34.2261, 135.1675, "Asia/Tokyo") },
            { "Wakayama", (34.2261, 135.1675, "Asia/Tokyo") },
            { "鳥取", (35.5014, 134.2350, "Asia/Tokyo") },
            { "Tottori", (35.5014, 134.2350, "Asia/Tokyo") },
            { "島根", (35.4723, 133.0505, "Asia/Tokyo") },
            { "Shimane", (35.4723, 133.0505, "Asia/Tokyo") },
            { "Matsue", (35.4723, 133.0505, "Asia/Tokyo") },
            { "岡山", (34.6617, 133.9350, "Asia/Tokyo") },
            { "Okayama", (34.6617, 133.9350, "Asia/Tokyo") },
            { "山口", (34.1861, 131.4714, "Asia/Tokyo") },
            { "Yamaguchi", (34.1861, 131.4714, "Asia/Tokyo") },
            { "徳島", (34.0658, 134.5595, "Asia/Tokyo") },
            { "Tokushima", (34.0658, 134.5595, "Asia/Tokyo") },
            { "香川", (34.3401, 134.0433, "Asia/Tokyo") },
            { "Kagawa", (34.3401, 134.0433, "Asia/Tokyo") },
            { "Takamatsu", (34.3401, 134.0433, "Asia/Tokyo") },
            { "愛媛", (33.8416, 132.7657, "Asia/Tokyo") },
            { "Ehime", (33.8416, 132.7657, "Asia/Tokyo") },
            { "Matsuyama", (33.8416, 132.7657, "Asia/Tokyo") },
            { "高知", (33.5597, 133.5311, "Asia/Tokyo") },
            { "Kochi", (33.5597, 133.5311, "Asia/Tokyo") },
            { "佐賀", (33.2494, 130.2989, "Asia/Tokyo") },
            { "Saga", (33.2494, 130.2989, "Asia/Tokyo") },
            { "長崎", (32.7503, 129.8779, "Asia/Tokyo") },
            { "Nagasaki", (32.7503, 129.8779, "Asia/Tokyo") },
            { "熊本", (32.7898, 130.7417, "Asia/Tokyo") },
            { "Kumamoto", (32.7898, 130.7417, "Asia/Tokyo") },
            { "大分", (33.2382, 131.6126, "Asia/Tokyo") },
            { "Oita", (33.2382, 131.6126, "Asia/Tokyo") },
            { "宮崎", (31.9077, 131.4202, "Asia/Tokyo") },
            { "Miyazaki", (31.9077, 131.4202, "Asia/Tokyo") },
            { "鹿児島", (31.5602, 130.5581, "Asia/Tokyo") },
            { "Kagoshima", (31.5602, 130.5581, "Asia/Tokyo") },
            { "沖縄", (26.2124, 127.6809, "Asia/Tokyo") },
            { "Okinawa", (26.2124, 127.6809, "Asia/Tokyo") },
            { "Naha", (26.2124, 127.6809, "Asia/Tokyo") },

            // アメリカ主要都市
            { "ニューヨーク", (40.7128, -74.0060, "America/New_York") },
            { "New York", (40.7128, -74.0060, "America/New_York") },
            { "NYC", (40.7128, -74.0060, "America/New_York") },
            { "ロサンゼルス", (34.0522, -118.2437, "America/Los_Angeles") },
            { "Los Angeles", (34.0522, -118.2437, "America/Los_Angeles") },
            { "LA", (34.0522, -118.2437, "America/Los_Angeles") },
            { "シカゴ", (41.8781, -87.6298, "America/Chicago") },
            { "Chicago", (41.8781, -87.6298, "America/Chicago") },
            { "ヒューストン", (29.7604, -95.3698, "America/Chicago") },
            { "Houston", (29.7604, -95.3698, "America/Chicago") },
            { "フェニックス", (33.4484, -112.0740, "America/Phoenix") },
            { "Phoenix", (33.4484, -112.0740, "America/Phoenix") },
            { "フィラデルフィア", (39.9526, -75.1652, "America/New_York") },
            { "Philadelphia", (39.9526, -75.1652, "America/New_York") },
            { "サンアントニオ", (29.4241, -98.4936, "America/Chicago") },
            { "San Antonio", (29.4241, -98.4936, "America/Chicago") },
            { "サンディエゴ", (32.7157, -117.1611, "America/Los_Angeles") },
            { "San Diego", (32.7157, -117.1611, "America/Los_Angeles") },
            { "ダラス", (32.7767, -96.7970, "America/Chicago") },
            { "Dallas", (32.7767, -96.7970, "America/Chicago") },
            { "サンノゼ", (37.3382, -121.8863, "America/Los_Angeles") },
            { "San Jose", (37.3382, -121.8863, "America/Los_Angeles") },
            { "オースティン", (30.2672, -97.7431, "America/Chicago") },
            { "Austin", (30.2672, -97.7431, "America/Chicago") },
            { "サンフランシスコ", (37.7749, -122.4194, "America/Los_Angeles") },
            { "San Francisco", (37.7749, -122.4194, "America/Los_Angeles") },
            { "SF", (37.7749, -122.4194, "America/Los_Angeles") },
            { "シアトル", (47.6062, -122.3321, "America/Los_Angeles") },
            { "Seattle", (47.6062, -122.3321, "America/Los_Angeles") },
            { "デンバー", (39.7392, -104.9903, "America/Denver") },
            { "Denver", (39.7392, -104.9903, "America/Denver") },
            { "ワシントンDC", (38.9072, -77.0369, "America/New_York") },
            { "Washington DC", (38.9072, -77.0369, "America/New_York") },
            { "Washington D.C.", (38.9072, -77.0369, "America/New_York") },
            { "ボストン", (42.3601, -71.0589, "America/New_York") },
            { "Boston", (42.3601, -71.0589, "America/New_York") },
            { "ラスベガス", (36.1699, -115.1398, "America/Los_Angeles") },
            { "Las Vegas", (36.1699, -115.1398, "America/Los_Angeles") },
            { "マイアミ", (25.7617, -80.1918, "America/New_York") },
            { "Miami", (25.7617, -80.1918, "America/New_York") },
            { "アトランタ", (33.7490, -84.3880, "America/New_York") },
            { "Atlanta", (33.7490, -84.3880, "America/New_York") },
            { "ポートランド", (45.5152, -122.6784, "America/Los_Angeles") },
            { "Portland", (45.5152, -122.6784, "America/Los_Angeles") },
            { "ホノルル", (21.3069, -157.8583, "Pacific/Honolulu") },
            { "Honolulu", (21.3069, -157.8583, "Pacific/Honolulu") },
            { "Hawaii", (21.3069, -157.8583, "Pacific/Honolulu") },
            { "ハワイ", (21.3069, -157.8583, "Pacific/Honolulu") },
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
                      $"&timezone={location.Timezone}";
            
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
        var japanCities = "東京, 大阪, 京都, 名古屋, 札幌, 福岡, 仙台, 広島, 神戸, 横浜, 沖縄";
        var usCities = "New York, Los Angeles, Chicago, San Francisco, Seattle, Las Vegas, Miami, Honolulu";
        return $"**利用可能な地域**:\n🇯🇵 **日本**: {japanCities} など\n🇺🇸 **アメリカ**: {usCities} など";
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
