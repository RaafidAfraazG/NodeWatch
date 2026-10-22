// Package recovery repairs Redis latest-state entries from the durable history.
package recovery

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
)

type Config struct {
	Interval                             time.Duration
	BatchSize, MaxBatches, RetryAttempts int
	InitialBackoff, MaxBackoff           time.Duration
}

func (c Config) Validate() error {
	if c.Interval < time.Second || c.Interval > time.Hour || c.BatchSize < 1 || c.BatchSize > 200 || c.MaxBatches < 1 || c.MaxBatches > 100 || c.RetryAttempts < 0 || c.RetryAttempts > 8 || c.InitialBackoff < time.Millisecond || c.MaxBackoff < c.InitialBackoff || c.MaxBackoff > time.Minute {
		return errors.New("invalid recovery configuration")
	}
	return nil
}

type Worker struct {
	history storage.History
	cache   storage.Cache
	config  Config
	after   string
	sleep   func(context.Context, time.Duration) error
}

func New(h storage.History, c storage.Cache, config Config) (*Worker, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Worker{history: h, cache: c, config: config, sleep: sleep}, nil
}
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (w *Worker) Once(ctx context.Context) error {
	hosts, err := w.history.List(ctx, w.after, w.config.BatchSize)
	if err != nil {
		return err
	}
	for _, h := range hosts {
		if err = w.put(ctx, h); err != nil {
			return err
		}
	}
	if len(hosts) < w.config.BatchSize {
		w.after = ""
	} else {
		w.after = hosts[len(hosts)-1].ID
	}
	return nil
}
func (w *Worker) put(ctx context.Context, h storage.Host) error {
	var err error
	delay := w.config.InitialBackoff
	for attempt := 0; attempt <= w.config.RetryAttempts; attempt++ {
		if err = w.cache.Put(ctx, h); err == nil {
			return nil
		}
		if attempt == w.config.RetryAttempts {
			return err
		}
		jitter := time.Duration(rand.Int64N(int64(delay/2 + 1)))
		if err = w.sleep(ctx, delay/2+jitter); err != nil {
			return err
		}
		if delay < w.config.MaxBackoff/2 {
			delay *= 2
		} else {
			delay = w.config.MaxBackoff
		}
	}
	return err
}
func PutWithRetry(ctx context.Context, cache storage.Cache, h storage.Host, attempts int) error {
	w, err := New(nil, cache, Config{Interval: time.Second, BatchSize: 1, MaxBatches: 1, RetryAttempts: attempts, InitialBackoff: 50 * time.Millisecond, MaxBackoff: time.Second})
	if err != nil {
		return err
	}
	return w.put(ctx, h)
}
func (w *Worker) Run(ctx context.Context, report func(error)) {
	ticker := time.NewTicker(w.config.Interval)
	defer ticker.Stop()
	for {
		for i := 0; i < w.config.MaxBatches; i++ {
			if err := w.Once(ctx); err != nil {
				if ctx.Err() == nil {
					report(err)
				}
				break
			}
			if w.after == "" {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
