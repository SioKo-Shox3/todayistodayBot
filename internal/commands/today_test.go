package commands

import "testing"

func TestTodayCommand_ParseDate_ValidMMdd(t *testing.T) {
	cmd := &TodayCommand{}
	d, err := cmd.parseDateArg("1225")
	if err != nil {
		t.Fatalf("expected valid MMdd to parse, got error: %v", err)
	}
	if d.Month() != 12 || d.Day() != 25 {
		t.Fatalf("expected December 25, got month=%d day=%d", d.Month(), d.Day())
	}
}

func TestTodayCommand_ParseDate_InvalidLength_ReturnsMalformedMessage(t *testing.T) {
	cmd := &TodayCommand{}
	_, err := cmd.parseDateArg("725")
	if err == nil {
		t.Fatal("expected error for non-4-digit input")
	}
	got := todayDateErrorMessage(err)
	want := "❌ 日付はMMdd形式で入力してください（例: 0101 = 1月1日、1225 = 12月25日）"
	if got != want {
		t.Fatalf("expected malformed-input message %q, got %q", want, got)
	}
}

func TestTodayCommand_ParseDate_NonNumeric_ReturnsMalformedMessage(t *testing.T) {
	cmd := &TodayCommand{}
	_, err := cmd.parseDateArg("abcd")
	if err == nil {
		t.Fatal("expected error for non-numeric input")
	}
	got := todayDateErrorMessage(err)
	want := "❌ 日付はMMdd形式で入力してください（例: 0101 = 1月1日、1225 = 12月25日）"
	if got != want {
		t.Fatalf("expected malformed-input message %q, got %q", want, got)
	}
}

func TestTodayCommand_ParseDate_InvalidCalendarDate_ReturnsInvalidDateMessage(t *testing.T) {
	cmd := &TodayCommand{}
	_, err := cmd.parseDateArg("0230") // Feb 30 does not exist
	if err == nil {
		t.Fatal("expected error for Feb 30 (invalid calendar date)")
	}
	got := todayDateErrorMessage(err)
	want := "❌ 無効な日付です。MMdd形式で正しい日付を入力してください（例: 0101 = 1月1日）"
	if got != want {
		t.Fatalf("expected invalid-calendar-date message %q, got %q", want, got)
	}
}

func TestTodayCommand_Definition(t *testing.T) {
	cmd := &TodayCommand{}
	def := cmd.Definition()
	if def.Name != "today" {
		t.Fatalf("expected command name 'today', got %q", def.Name)
	}
}
