package today

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultAnnivBaseURL          = "https://api.whatistoday.cyou/v3/anniv/"
	defaultBirthflowerBaseURL    = "https://api.whatistoday.cyou/v3/birthflower/"
	defaultFamousBirthdayBaseURL = "https://api.whatistoday.cyou/v3/famousbirthday/"
)

type anniversaryResponse struct {
	ID     int    `json:"id"`
	Mmdd   string `json:"mmdd"`
	Anniv1 string `json:"anniv1"`
	Anniv2 string `json:"anniv2"`
	Anniv3 string `json:"anniv3"`
	Anniv4 string `json:"anniv4"`
	Anniv5 string `json:"anniv5"`
}

type birthflowerResponse struct {
	ID     int    `json:"id"`
	Mmdd   string `json:"mmdd"`
	Flower string `json:"flower"`
	Lang   string `json:"lang"`
}

type famousBirthdayResponse struct {
	ID       int    `json:"id"`
	Mmdd     string `json:"mmdd"`
	Lifespan string `json:"lifespan"`
	Name     string `json:"name"`
	Profile  string `json:"profile"`
}

// Client fetches "what day is today" info (anniversaries, birth flower,
// famous birthdays) from the WhatIsToday API. Ported from
// TodayIsTodayBot/Services/TodayService.cs.
type Client struct {
	httpClient *http.Client
	baseURL    string // overridable in tests; when set, used as the scheme+host prefix for all 3 sub-paths
}

func NewClient(httpClient *http.Client) *Client {
	return &Client{httpClient: httpClient}
}

func (c *Client) endpoint(kind, mmdd string) string {
	if c.baseURL != "" {
		return fmt.Sprintf("%s/v3/%s/%s", c.baseURL, kind, mmdd)
	}
	switch kind {
	case "anniv":
		return defaultAnnivBaseURL + mmdd
	case "birthflower":
		return defaultBirthflowerBaseURL + mmdd
	case "famousbirthday":
		return defaultFamousBirthdayBaseURL + mmdd
	}
	return ""
}

func (c *Client) fetchJSON(ctx context.Context, url string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("today: unexpected status %d from %s", resp.StatusCode, url)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// GetTodayInfo fetches all three sub-APIs concurrently and returns a single
// formatted Japanese message, omitting any section whose sub-fetch failed.
//
// IMPORTANT — this is a deliberate, documented BEHAVIOR CHANGE from the .NET
// source, not a faithful port of TodayService.cs's failure path. Tracing
// TodayService.cs's actual control flow: GetAnniversaryAsync /
// GetBirthflowerAsync / GetFamousBirthdayAsync each `throw;` internally on
// failure (TodayService.cs:44,70,97). GetTodayInfoAsync wraps each in
// `.ContinueWith(t => (object?)t.Result)` (TodayService.cs:112-114) — when
// the antecedent task is Faulted, evaluating `t.Result` inside the
// continuation throws, so the continuation itself becomes Faulted too.
// `Task.WhenAll` then throws, which is swallowed by an empty `catch {}`
// (TodayService.cs:117-124) — but the following lines
// (`tasks[0].Result as AnniversaryResponse` etc., TodayService.cs:126-128)
// call `.Result` on the still-Faulted task AGAIN, which throws synchronously
// and is NOT caught anywhere inside GetTodayInfoAsync. It propagates out to
// TodayCommand.ExecuteAsync's `catch (Exception ex)` (TodayCommand.cs:71-83),
// which discards the entire reply and substitutes one generic
// "❌ 情報の取得中にエラーが発生しました: {ex.Message}" message.
//
// So the REAL .NET behavior is: any single sub-API failure (not just all
// three) aborts the WHOLE /today reply with one generic error — this reads
// as an unintentional side effect of mixing .ContinueWith with .Result, not
// a deliberate design choice. For this Go port, a partial/best-effort reply
// (omitting only the failed section) is strictly better UX for a low-stakes
// info command, so GetTodayInfo intentionally implements best-effort
// per-section omission instead of reproducing the all-or-nothing failure
// path above.
func (c *Client) GetTodayInfo(ctx context.Context, date time.Time) string {
	mmdd := date.Format("0102")

	var (
		wg       sync.WaitGroup
		anniv    anniversaryResponse
		annivOK  bool
		flower   birthflowerResponse
		flowerOK bool
		famous   famousBirthdayResponse
		famousOK bool
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		if err := c.fetchJSON(ctx, c.endpoint("anniv", mmdd), &anniv); err == nil {
			annivOK = true
		}
	}()
	go func() {
		defer wg.Done()
		if err := c.fetchJSON(ctx, c.endpoint("birthflower", mmdd), &flower); err == nil {
			flowerOK = true
		}
	}()
	go func() {
		defer wg.Done()
		if err := c.fetchJSON(ctx, c.endpoint("famousbirthday", mmdd), &famous); err == nil {
			famousOK = true
		}
	}()
	wg.Wait()

	return formatTodayInfo(date, anniv, annivOK, flower, flowerOK, famous, famousOK)
}

func formatTodayInfo(
	date time.Time,
	anniv anniversaryResponse, annivOK bool,
	flower birthflowerResponse, flowerOK bool,
	famous famousBirthdayResponse, famousOK bool,
) string {
	dateStr := fmt.Sprintf("%d月%d日", int(date.Month()), date.Day())

	var b strings.Builder
	fmt.Fprintf(&b, "📅 **%sは何の日？**\n\n", dateStr)

	if annivOK {
		b.WriteString("🎉 **記念日**\n")
		items := []string{anniv.Anniv1, anniv.Anniv2, anniv.Anniv3, anniv.Anniv4, anniv.Anniv5}
		any := false
		for _, item := range items {
			if item != "" {
				fmt.Fprintf(&b, "  • %s\n", item)
				any = true
			}
		}
		if !any {
			b.WriteString("  （情報なし）\n")
		}
		b.WriteString("\n")
	}

	if flowerOK && flower.Flower != "" {
		b.WriteString("🌸 **誕生花**\n")
		fmt.Fprintf(&b, "  • %s", flower.Flower)
		if flower.Lang != "" {
			fmt.Fprintf(&b, "（花言葉: %s）", flower.Lang)
		}
		b.WriteString("\n\n")
	}

	if famousOK && famous.Name != "" {
		b.WriteString("👤 **この日生まれの偉人**\n")
		fmt.Fprintf(&b, "  • %s", famous.Name)
		if famous.Profile != "" {
			fmt.Fprintf(&b, "（%s）", famous.Profile)
		}
		if famous.Lifespan != "" {
			fmt.Fprintf(&b, " [%s]", famous.Lifespan)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n_Powered by [whatistoday API](https://note.com/sooz/n/naffb68c7f53b)_")

	return b.String()
}
