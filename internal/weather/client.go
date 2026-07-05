package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const defaultBaseURL = "https://api.open-meteo.com/v1/forecast"

type openMeteoResponse struct {
	// Pointer so a missing/null "current" key in the response JSON is
	// distinguishable from a present-but-zero-valued object (see the nil
	// check in GetWeather below, matching WeatherService.cs:225-227).
	Current *currentWeather `json:"current"`
}

type currentWeather struct {
	Temperature         float64 `json:"temperature_2m"`
	ApparentTemperature float64 `json:"apparent_temperature"`
	RelativeHumidity    int     `json:"relative_humidity_2m"`
	WeatherCode         int     `json:"weather_code"`
	WindSpeed           float64 `json:"wind_speed_10m"`
}

// Client fetches and formats weather info from Open-Meteo. Ported from
// TodayIsTodayBot/Services/WeatherService.cs.
type Client struct {
	httpClient *http.Client
	baseURL    string // overridable in tests; defaults to defaultBaseURL
}

func NewClient(httpClient *http.Client) *Client {
	return &Client{httpClient: httpClient, baseURL: defaultBaseURL}
}

// GetWeather returns a user-facing formatted message (success or a
// Japanese error message) for cityName. It only returns a non-nil error
// for programmer errors (e.g. malformed request construction) — network
// and API failures are surfaced as formatted error strings, matching the
// .NET WeatherService.GetWeatherAsync behavior of never throwing to the
// caller for expected failure modes.
func (c *Client) GetWeather(ctx context.Context, cityName string) (string, error) {
	loc, ok := lookupCity(cityName)
	if !ok {
		return fmt.Sprintf("❌ 都市「%s」が見つかりませんでした。都道府県名または主要都市名を指定してください。\n\n%s",
			cityName, AvailableCities()), nil
	}

	url := fmt.Sprintf("%s?latitude=%g&longitude=%g&current=temperature_2m,relative_humidity_2m,apparent_temperature,weather_code,wind_speed_10m&timezone=%s",
		c.baseURL, loc.Lat, loc.Lon, loc.Timezone)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("weather: building request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Sprintf("❌ 天気情報の取得中にネットワークエラーが発生しました: %s", err.Error()), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// NOTE (deliberate, acknowledged wording difference — not a silent
		// regression): the .NET source interpolates .NET's HttpStatusCode
		// enum name here (e.g. "InternalServerError"), not a bare number —
		// see WeatherService.cs:219 `{response.StatusCode}`. Go's
		// http.StatusText() doesn't produce matching no-space PascalCase
		// names either, and hand-rolling a name table for this rare,
		// low-stakes error-wording path isn't worth the maintenance cost.
		// This Go port intentionally uses the numeric HTTP status code
		// instead (e.g. "500") in this one message.
		return fmt.Sprintf("❌ 天気情報の取得に失敗しました。(ステータスコード: %d)", resp.StatusCode), nil
	}

	var parsed openMeteoResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "❌ 天気情報の解析に失敗しました。", nil
	}

	// Matches WeatherService.cs:225-227's `if (weatherData?.Current == null)
	// return "❌ 天気情報の解析に失敗しました。";` guard — Current is a pointer
	// here specifically so a missing/null "current" key in the response JSON
	// is distinguishable from a present-but-zero-valued object. Without the
	// pointer, a missing key would silently format as 0°C/快晴 instead of
	// surfacing this error.
	if parsed.Current == nil {
		return "❌ 天気情報の解析に失敗しました。", nil
	}

	return formatWeatherMessage(*parsed.Current, cityName), nil
}

func formatWeatherMessage(cur currentWeather, requestedCity string) string {
	icon := weatherIcon(cur.WeatherCode)
	desc := weatherDescription(cur.WeatherCode)

	var b strings.Builder
	fmt.Fprintf(&b, "**%s %sの天気**\n\n", icon, requestedCity)
	fmt.Fprintf(&b, "🌡️ **気温**: %.1f°C (体感: %.1f°C)\n", cur.Temperature, cur.ApparentTemperature)
	fmt.Fprintf(&b, "📊 **状態**: %s\n", desc)
	fmt.Fprintf(&b, "💧 **湿度**: %d%%\n", cur.RelativeHumidity)
	fmt.Fprintf(&b, "💨 **風速**: %.1f m/s\n", cur.WindSpeed)
	return b.String()
}

func weatherDescription(code int) string {
	switch code {
	case 0:
		return "快晴"
	case 1:
		return "ほぼ晴れ"
	case 2:
		return "部分的に曇り"
	case 3:
		return "曇り"
	case 45, 48:
		return "霧"
	case 51, 53, 55:
		return "霧雨"
	case 56, 57:
		return "凍る霧雨"
	case 61, 63, 65:
		return "雨"
	case 66, 67:
		return "凍る雨"
	case 71, 73, 75:
		return "雪"
	case 77:
		return "みぞれ"
	case 80, 81, 82:
		return "にわか雨"
	case 85, 86:
		return "にわか雪"
	case 95:
		return "雷雨"
	case 96, 99:
		return "雹を伴う雷雨"
	default:
		return "不明"
	}
}

func weatherIcon(code int) string {
	switch code {
	case 0:
		return "☀️"
	case 1, 2:
		return "🌤️"
	case 3:
		return "☁️"
	case 45, 48:
		return "🌫️"
	case 51, 53, 55, 56, 57:
		return "🌦️"
	case 61, 63, 65, 66, 67, 80, 81, 82:
		return "🌧️"
	case 71, 73, 75, 77, 85, 86:
		return "❄️"
	case 95, 96, 99:
		return "⛈️"
	default:
		return "🌤️"
	}
}

// AvailableCities returns the same "available regions" summary as
// TodayIsTodayBot/Services/WeatherService.cs's GetAvailableCities().
func AvailableCities() string {
	return "**利用可能な地域**:\n" +
		"🇯🇵 **日本**: 東京, 大阪, 京都, 名古屋, 札幌, 福岡, 仙台, 広島, 神戸, 横浜, 沖縄 など\n" +
		"🇺🇸 **アメリカ**: New York, Los Angeles, Chicago, San Francisco, Seattle, Las Vegas, Miami, Honolulu など"
}
