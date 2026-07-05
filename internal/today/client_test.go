package today

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustParseDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("failed to parse test date %q: %v", s, err)
	}
	return d
}

func TestGetTodayInfo_AllThreeSucceed_FormatsAllSections(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/anniv/0705", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(anniversaryResponse{Anniv1: "テスト記念日"})
	})
	mux.HandleFunc("/v3/birthflower/0705", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(birthflowerResponse{Flower: "ひまわり", Lang: "憧れ"})
	})
	mux.HandleFunc("/v3/famousbirthday/0705", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(famousBirthdayResponse{Name: "テスト偉人", Profile: "発明家", Lifespan: "1900-1980"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.Client())
	client.baseURL = server.URL

	date := mustParseDate(t, "2026-07-05")
	got := client.GetTodayInfo(context.Background(), date)

	for _, want := range []string{
		"7月5日は何の日？", // full-width "？", matches TodayService.cs:130
		"テスト記念日",
		"ひまわり（花言葉: 憧れ）", // full-width parens, matches TodayService.cs:165
		"テスト偉人（発明家）",    // full-width parens, matches TodayService.cs:177
		"[1900-1980]",
		"Powered by",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got: %q", want, got)
		}
	}
}

func TestGetTodayInfo_AnniversaryEmpty_ShowsFullWidthNoInfoMessage(t *testing.T) {
	// Matches TodayService.cs:153's "  （情報なし）\n" — full-width parens,
	// shown when the anniversary sub-fetch succeeds but returns no anniv1..5.
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/anniv/0705", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(anniversaryResponse{})
	})
	mux.HandleFunc("/v3/birthflower/0705", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/v3/famousbirthday/0705", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.Client())
	client.baseURL = server.URL

	date := mustParseDate(t, "2026-07-05")
	got := client.GetTodayInfo(context.Background(), date)

	if !strings.Contains(got, "  （情報なし）") {
		t.Fatalf("expected full-width empty-anniversary message, got: %q", got)
	}
}

func TestGetTodayInfo_AllFail_OmitsSectionsGracefully(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.baseURL = server.URL

	date := mustParseDate(t, "2026-07-05")
	got := client.GetTodayInfo(context.Background(), date)

	if !strings.Contains(got, "7月5日は何の日？") {
		t.Fatalf("expected header even when all sub-fetches fail, got: %q", got)
	}
	if strings.Contains(got, "テスト記念日") {
		t.Fatal("did not expect anniversary content when fetch failed")
	}
}
