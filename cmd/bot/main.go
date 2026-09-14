package main

import (
	"context"
	"fmt"
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

// startupSteps is the boot order 設計書 §3 requires. The refund comes FIRST,
// before a single interaction can reach the process: boards live only in
// memory, so every escrow still on disk at boot belongs to a board that died
// with the previous process — but that is only true until this process starts
// taking bets. Opening the gateway or registering the commands first leaves a
// window in which a fresh /blackjack stakes its chips and has them refunded
// out from under it, after which settling that hand answers ErrNoGameInProgress
// (反復 1 の指摘 1).
//
// The three steps are fields rather than straight-line code so a test can
// assert the ORDER — the property that broke — without a Discord connection.
type startupSteps struct {
	refundStaleEscrows func() (int, error)
	openSession        func() error
	registerCommands   func() error
}

// run performs the steps in order and stops at the first failure. A refund
// that did not happen must not be followed by an open gateway: the bot would
// then accept games while stale escrow still blocks those very accounts
// ("進行中のゲームがあります")。Startup failures are fatal for the caller, the
// same as a missing token.
func (s startupSteps) run() error {
	refunded, err := s.refundStaleEscrows()
	if err != nil {
		return fmt.Errorf("refunding stale escrows: %w", err)
	}
	slog.Info("casino: refunded stale escrows", "accounts", refunded)

	if err := s.openSession(); err != nil {
		return fmt.Errorf("opening the discord session: %w", err)
	}
	if err := s.registerCommands(); err != nil {
		return fmt.Errorf("registering application commands: %w", err)
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

	if err := (startupSteps{
		refundStaleEscrows: func() (int, error) { return casino.Default().RefundStaleEscrows(time.Now()) },
		openSession:        session.Open,
		registerCommands: func() error {
			for _, cmd := range all {
				if _, err := session.ApplicationCommandCreate(session.State.User.ID, "", cmd.Definition()); err != nil {
					return fmt.Errorf("%s: %w", cmd.Definition().Name, err)
				}
			}
			return nil
		},
	}).run(); err != nil {
		slog.Error("failed to start the bot", "error", err)
		os.Exit(1)
	}
	defer session.Close()

	slog.Info("bot is running", "registered_commands", len(all))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
