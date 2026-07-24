package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tianyu150/mir-eternal/internal/account"
	"github.com/tianyu150/mir-eternal/internal/accountserver"
	"github.com/tianyu150/mir-eternal/internal/observability"
	"github.com/tianyu150/mir-eternal/internal/ticket"
)

func main() {
	configPath := flag.String("config", "configs/accountserver.json", "path to JSON configuration")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(*configPath, logger); err != nil {
		logger.Error("account server stopped", "error", err)
		os.Exit(1)
	}
}

func run(configPath string, logger *slog.Logger) error {
	config, err := accountserver.LoadConfig(configPath)
	if err != nil {
		return err
	}
	repository, err := account.OpenJSONRepository(config.AccountsDirectory)
	if err != nil {
		return err
	}
	count, err := repository.Count(context.Background())
	if err != nil {
		return fmt.Errorf("count accounts: %w", err)
	}
	service := account.NewService(repository, config.BCryptCost)
	stats := &accountserver.Stats{}
	// Seed the gauge before any requests are accepted.
	stats.SetAccounts(int64(count))
	codec := ticket.Codec{Secret: []byte(config.TicketSecret)}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var server *accountserver.Server
	handler := accountserver.NewHandler(service, config.Servers, codec, config.TicketTTL, ticketSenderFunc(func(ctx context.Context, game accountserver.GameServer, message []byte) error {
		return server.SendTicket(ctx, game, message)
	}), stats, logger)
	server = accountserver.New(config, handler, stats, logger)
	errCh := make(chan error, 2)
	go func() {
		errCh <- observability.Serve(ctx, config.AdminListen, func() any { return stats.Snapshot() }, logger)
	}()
	go func() { errCh <- server.Serve(ctx) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

type ticketSenderFunc func(context.Context, accountserver.GameServer, []byte) error

func (f ticketSenderFunc) SendTicket(ctx context.Context, server accountserver.GameServer, message []byte) error {
	return f(ctx, server, message)
}
