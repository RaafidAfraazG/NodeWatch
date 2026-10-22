package recovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
)

type history struct {
	hosts []storage.Host
	calls int
}

func (h *history) Ping(context.Context) error { return nil }
func (h *history) Accept(context.Context, metrics.Snapshot, time.Time) (storage.Host, bool, error) {
	return storage.Host{}, false, nil
}
func (h *history) Host(context.Context, string) (storage.Host, error) {
	return storage.Host{}, storage.ErrNotFound
}
func (h *history) Query(context.Context, string, storage.Query) ([]metrics.Snapshot, error) {
	return nil, nil
}
func (h *history) List(ctx context.Context, after string, limit int) ([]storage.Host, error) {
	h.calls++
	out := []storage.Host{}
	for _, item := range h.hosts {
		if item.ID > after && len(out) < limit {
			out = append(out, item)
		}
	}
	return out, nil
}

type cache struct {
	puts []string
	fail int
}

func (c *cache) Ping(context.Context) error                                         { return nil }
func (c *cache) States(context.Context, []string) (map[string]storage.State, error) { return nil, nil }
func (c *cache) Put(ctx context.Context, h storage.Host) error {
	c.puts = append(c.puts, h.ID)
	if c.fail > 0 {
		c.fail--
		return errors.New("temporary")
	}
	return nil
}
func TestReconciliationPaginationAndRecovery(t *testing.T) {
	h := &history{hosts: []storage.Host{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	c := &cache{fail: 1}
	w, err := New(h, c, Config{Interval: time.Second, BatchSize: 2, MaxBatches: 1, RetryAttempts: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = w.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.puts) != 4 || c.puts[len(c.puts)-1] != "c" {
		t.Fatalf("%+v", c.puts)
	}
	if err = w.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.puts[len(c.puts)-1] != "b" {
		t.Fatal("cursor did not cycle")
	}
}
func TestCancellation(t *testing.T) {
	h := &history{hosts: []storage.Host{{ID: "a"}}}
	c := &cache{fail: 10}
	w, err := New(h, c, Config{Interval: time.Second, BatchSize: 1, MaxBatches: 1, RetryAttempts: 3, InitialBackoff: time.Second, MaxBackoff: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.sleep = func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }
	if err := w.Once(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
