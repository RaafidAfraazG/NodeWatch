package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/RaafidAfraazG/NodeWatch/internal/agent"
	"github.com/RaafidAfraazG/NodeWatch/internal/config"
	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
)

func main() { os.Exit(run()) }
func run() int {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := config.Load(os.Args[1:], os.Getenv, os.Hostname)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Print(config.Usage)
		return 0
	}
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("agent starting", "node_id", cfg.NodeID, "interval", cfg.Interval.String())
	if err := agent.Run(ctx, cfg.Interval, metrics.NewCollector(cfg.NodeID, cfg.Mounts), agent.NewSender(cfg.CollectorURL, cfg.HTTPTimeout, cfg.MaxRetries), logger); err != nil {
		logger.Error("agent failed", "error", err)
		return 1
	}
	logger.Info("agent stopped")
	return 0
}
