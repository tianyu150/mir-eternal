package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tianyu150/mir-eternal/internal/gameserver"
	"github.com/tianyu150/mir-eternal/internal/gamestore"
	"github.com/tianyu150/mir-eternal/internal/observability"
)

func main() {
	configPath := flag.String("config", "configs/gameserver.json", "path to JSON configuration")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(*configPath, logger); err != nil {
		logger.Error("game server stopped", "error", err)
		os.Exit(1)
	}
}
func run(configPath string, logger *slog.Logger) error {
	config, err := gameserver.LoadConfig(configPath)
	if err != nil {
		return err
	}
	store, err := gamestore.Open(config.DatabasePath)
	if err != nil {
		return err
	}
	stats := &gameserver.Stats{}
	server := gameserver.New(config, store, stats, logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 2)
	go func() {
		errCh <- observability.Serve(ctx, config.AdminListen, func() any { return server.Snapshot() }, logger)
	}()
	go func() { errCh <- server.Serve(ctx) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}
