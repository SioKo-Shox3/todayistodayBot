package commands

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestFormatHelpText_ListsCommandsSortedByName(t *testing.T) {
	defs := []*discordgo.ApplicationCommand{
		{Name: "weather", Description: "天気情報を取得します"},
		{Name: "dice", Description: "サイコロを振ります"},
	}

	got := formatHelpText(defs)

	wantOrder := []string{"`/dice` - サイコロを振ります", "`/weather` - 天気情報を取得します"}
	diceIdx := strings.Index(got, wantOrder[0])
	weatherIdx := strings.Index(got, wantOrder[1])
	if diceIdx == -1 || weatherIdx == -1 {
		t.Fatalf("expected both command lines present, got: %q", got)
	}
	if diceIdx > weatherIdx {
		t.Fatalf("expected dice before weather (alphabetical), got: %q", got)
	}
	if !strings.HasPrefix(got, "**📋 利用可能なコマンド一覧**") {
		t.Fatalf("expected header prefix, got: %q", got)
	}
}

func TestFormatHelpText_Empty(t *testing.T) {
	got := formatHelpText(nil)
	if got != "現在、利用可能なコマンドはありません。" {
		t.Fatalf("expected empty-list message, got: %q", got)
	}
}
