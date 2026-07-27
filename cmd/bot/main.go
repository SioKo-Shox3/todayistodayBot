package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/SioKo-Shox3/todayistodayBot/internal/commands"
	"github.com/SioKo-Shox3/todayistodayBot/internal/config"

	"github.com/bwmarrin/discordgo"
)

func findCommandByName(cmds []commands.Command, name string) commands.Command {
	for _, c := range cmds {
		if c.Definition().Name == name {
			return c
		}
	}
	return nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		slog.Error("failed to create discordgo session", "error", err)
		os.Exit(1)
	}

	all := commands.All()

	session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type != discordgo.InteractionApplicationCommand {
			return
		}
		name := i.ApplicationCommandData().Name
		cmd := findCommandByName(all, name)
		if cmd == nil {
			slog.Warn("received interaction for unregistered command", "name", name)
			return
		}
		if err := cmd.Handle(s, i); err != nil {
			slog.Error("command handler returned an error", "name", name, "error", err)
			_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "❌ コマンドの実行中にエラーが発生しました。",
				},
			})
		}
	})

	session.Identify.Intents = discordgo.IntentsNone

	if err := session.Open(); err != nil {
		slog.Error("failed to open discord session", "error", err)
		os.Exit(1)
	}
	defer session.Close()

	for _, cmd := range all {
		if _, err := session.ApplicationCommandCreate(session.State.User.ID, "", cmd.Definition()); err != nil {
			slog.Error("failed to register application command", "name", cmd.Definition().Name, "error", err)
			os.Exit(1)
		}
	}

	slog.Info("bot is running", "registered_commands", len(all))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// フェーズC-1: 毎朝9:00 JSTのレート掲示スケジューラ(起動直後に1回パスを走らせる)
	waitScheduler := commands.StartCasinoAnnounceScheduler(ctx, session)

	<-ctx.Done()

	slog.Info("shutdown signal received, waiting for casino scheduler")
	waitScheduler() // 掲示送信中にセッションを閉じないよう、goroutineの終了を待つ

	slog.Info("shutdown signal received, closing session")
}
