package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
)

type Source interface {
	Collect(context.Context) metrics.Snapshot
}
type Sink interface {
	Send(context.Context, metrics.Snapshot) error
}

// Run uses one sequential loop to bound memory and avoid overlapping collection
// or delivery. Slow sends coalesce missed ticks; failed snapshots are dropped.
func Run(ctx context.Context, interval time.Duration, source Source, sink Sink, logger *slog.Logger) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		snapshot := source.Collect(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if len(snapshot.Errors) > 0 {
			logger.Warn("some metrics unavailable", "errors", snapshot.Errors)
		}
		if err := sink.Send(ctx, snapshot); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logger.Error("snapshot delivery failed; dropping snapshot", "error", err)
		} else {
			logger.Info("snapshot delivered", "collected_at", snapshot.CollectedAt)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
