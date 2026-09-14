package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SioKo-Shox3/todayistodayBot/internal/casino"
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
		// フェーズC-2: ボタン押下はゲーム側の ComponentHandler へ渡す
		// (どのゲームを足しても main.go は変わらない)。
		if i.Type == discordgo.InteractionMessageComponent {
			if err := commands.DispatchComponent(s, i); err != nil {
				slog.Error("component handler returned an error", "error", err)
			}
			return
		}
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

	// フェーズC-2: 盤面はメモリにしか無いので、起動時に残っている預かりは
	// 「プロセスと一緒に消えた盤面」と同義(設計書 §3)。件数だけログに残す。
	if refunded, err := casino.Default().RefundStaleEscrows(time.Now()); err != nil {
		slog.Error("casino: refunding stale escrows failed", "error", err)
	} else {
		slog.Info("casino: refunded stale escrows", "accounts", refunded)
	}

	// フェーズC-1: 毎朝9:00 JSTのレート掲示スケジューラ(起動直後に1回パスを走らせる)
	waitScheduler := commands.StartCasinoAnnounceScheduler(ctx, session)

	// フェーズC-2: 放置された盤面の自動決着(設計書 §5)。掲示スケジューラと
	// 同じctxで止まり、掲示と同じ理由でセッションを閉じる前に終了を待つ
	// (メッセージ編集の最中にセッションを閉じるとuse-after-closeになる)。
	sweeperDone := make(chan struct{})
	go func() {
		defer close(sweeperDone)
		commands.RunSessionSweeper(ctx, session, casino.DefaultSessions(), casino.Default(), commands.CasinoSweepInterval)
	}()

	<-ctx.Done()

	slog.Info("shutdown signal received, waiting for casino scheduler")
	waitScheduler() // 掲示送信中にセッションを閉じないよう、goroutineの終了を待つ
	<-sweeperDone

	slog.Info("shutdown signal received, closing session")
}
