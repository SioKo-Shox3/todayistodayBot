package commands

import (
	"strings"
	"testing"
)

func TestWeatherCommand_NoArgs_ReturnsHelpText(t *testing.T) {
	cmd := &WeatherCommand{}
	got := cmd.helpText()
	if !strings.Contains(got, "天気コマンドの使い方") {
		t.Fatalf("expected usage help text, got: %q", got)
	}
	if !strings.Contains(got, "/weather 東京") {
		t.Fatalf("expected usage example, got: %q", got)
	}
}

func TestWeatherCommand_Definition(t *testing.T) {
	cmd := &WeatherCommand{}
	def := cmd.Definition()
	if def.Name != "weather" {
		t.Fatalf("expected command name 'weather', got %q", def.Name)
	}
	if len(def.Options) != 1 {
		t.Fatalf("expected exactly 1 option (city), got %d", len(def.Options))
	}
	if def.Options[0].Required {
		t.Fatal("expected city option to be optional, to allow the no-args help-text path")
	}
}

func TestWeatherCommand_BuildResponseContent_NoCityArg_ReturnsHelpText(t *testing.T) {
	cmd := &WeatherCommand{}
	got := cmd.buildResponseContent("")
	if got != cmd.helpText() {
		t.Fatalf("expected buildResponseContent(\"\") to equal helpText(), got: %q", got)
	}
}
