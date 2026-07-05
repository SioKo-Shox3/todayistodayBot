package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetWeather_KnownCity_FormatsMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openMeteoResponse{
			Current: &currentWeather{
				Temperature:         21.4,
				ApparentTemperature: 20.1,
				RelativeHumidity:    55,
				WeatherCode:         0,
				WindSpeed:           3.2,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.baseURL = server.URL

	got, err := client.GetWeather(context.Background(), "東京")
	if err != nil {
		t.Fatalf("GetWeather returned error: %v", err)
	}
	if !strings.Contains(got, "東京の天気") {
		t.Fatalf("expected city name in message, got: %q", got)
	}
	if !strings.Contains(got, "21.4") || !strings.Contains(got, "20.1") {
		t.Fatalf("expected temperature values in message, got: %q", got)
	}
	if !strings.Contains(got, "快晴") {
		t.Fatalf("expected weather_code 0 to map to 快晴, got: %q", got)
	}
}

func TestGetWeather_UnknownCity_ReturnsErrorMessage(t *testing.T) {
	client := NewClient(http.DefaultClient)

	got, err := client.GetWeather(context.Background(), "存在しない場所999")
	if err != nil {
		t.Fatalf("GetWeather should not return a Go error for unknown city, got: %v", err)
	}
	if !strings.Contains(got, "見つかりませんでした") {
		t.Fatalf("expected not-found message, got: %q", got)
	}
}

func TestGetWeather_NonOKStatus_ReturnsErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.baseURL = server.URL

	got, err := client.GetWeather(context.Background(), "大阪")
	if err != nil {
		t.Fatalf("GetWeather should not return a Go error on non-200, got: %v", err)
	}
	if !strings.Contains(got, "取得に失敗しました") {
		t.Fatalf("expected failure message, got: %q", got)
	}
}

func TestGetWeather_MissingCurrentKey_ReturnsParseErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately omit "current" entirely (not just zero-valued) to
		// reproduce WeatherService.cs:225-227's `weatherData?.Current == null`
		// guard path — a missing key must not silently format as 0°C/快晴.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.baseURL = server.URL

	got, err := client.GetWeather(context.Background(), "東京")
	if err != nil {
		t.Fatalf("GetWeather should not return a Go error for missing current key, got: %v", err)
	}
	if got != "❌ 天気情報の解析に失敗しました。" {
		t.Fatalf("expected exact parse-failure message for missing current key, got: %q", got)
	}
}

func TestLookupCity_KnownCoordinates(t *testing.T) {
	// Table-driven spot-check against the ported city table (WeatherService.cs:25-190)
	// to catch transcription errors (wrong lat/lon/timezone) that the
	// message-formatting tests above would not detect.
	cases := []struct {
		name    string
		wantLat float64
		wantLon float64
		wantTZ  string
	}{
		{"東京", 35.6895, 139.6917, "Asia/Tokyo"},
		{"Tokyo", 35.6895, 139.6917, "Asia/Tokyo"},
		{"大阪", 34.6937, 135.5023, "Asia/Tokyo"},
		{"沖縄", 26.2124, 127.6809, "Asia/Tokyo"},
		{"New York", 40.7128, -74.0060, "America/New_York"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc, ok := lookupCity(tc.name)
			if !ok {
				t.Fatalf("expected %q to be found in city table", tc.name)
			}
			if loc.Lat != tc.wantLat || loc.Lon != tc.wantLon {
				t.Fatalf("city %q: expected (%g, %g), got (%g, %g)", tc.name, tc.wantLat, tc.wantLon, loc.Lat, loc.Lon)
			}
			if loc.Timezone != tc.wantTZ {
				t.Fatalf("city %q: expected timezone %q, got %q", tc.name, tc.wantTZ, loc.Timezone)
			}
		})
	}
}

func TestGetWeather_RequestURL_ContainsExpectedQueryValues(t *testing.T) {
	// Verifies GetWeather actually builds a request URL carrying the looked-up
	// city's coordinates/timezone, catching a transcription or wiring error
	// between the city table and the outgoing request (not just the table
	// itself, which TestLookupCity_KnownCoordinates already covers).
	cases := []struct {
		city    string
		wantLat string
		wantLon string
		wantTZ  string
	}{
		{"東京", "35.6895", "139.6917", "Asia/Tokyo"},
		{"Osaka", "34.6937", "135.5023", "Asia/Tokyo"},
		{"福岡", "33.5904", "130.4017", "Asia/Tokyo"},
		{"San Francisco", "37.7749", "-122.4194", "America/Los_Angeles"},
	}

	for _, tc := range cases {
		t.Run(tc.city, func(t *testing.T) {
			var gotURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotURL = r.URL.String()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(openMeteoResponse{Current: &currentWeather{}})
			}))
			defer server.Close()

			client := NewClient(server.Client())
			client.baseURL = server.URL

			if _, err := client.GetWeather(context.Background(), tc.city); err != nil {
				t.Fatalf("GetWeather returned error: %v", err)
			}

			for _, want := range []string{
				"latitude=" + tc.wantLat,
				"longitude=" + tc.wantLon,
				"timezone=" + tc.wantTZ,
			} {
				if !strings.Contains(gotURL, want) {
					t.Fatalf("city %q: expected request URL to contain %q, got: %q", tc.city, want, gotURL)
				}
			}
		})
	}
}
