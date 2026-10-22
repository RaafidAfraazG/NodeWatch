package redis

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/RaafidAfraazG/NodeWatch/internal/config"
	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
)

// miniredis is an emulator; these are not real-service integration tests.
func TestLatestHeartbeatAndConcurrentOrdering(t *testing.T) {
	server := miniredis.RunT(t)
	s := Open(config.Collector{RedisAddr: server.Addr(), DependencyTimeout: time.Second, HeartbeatTTL: 5 * time.Second})
	defer s.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	host := func(n int) storage.Host {
		return storage.Host{ID: "redis-unit", LastSeen: now, Latest: metrics.Snapshot{NodeID: "redis-unit", CollectedAt: now.Add(time.Duration(n) * time.Nanosecond)}}
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Put(ctx, host(i)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	states, err := s.States(ctx, []string{"redis-unit", "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if !states["redis-unit"].Online || states["redis-unit"].Latest.CollectedAt != host(19).Latest.CollectedAt {
		t.Fatalf("latest not monotonic: %+v", states)
	}
	if states["unknown"].Latest != nil || states["unknown"].Online {
		t.Fatal("unknown host invented")
	}
	server.FastForward(6 * time.Second)
	states, err = s.States(ctx, []string{"redis-unit"})
	if err != nil {
		t.Fatal(err)
	}
	if states["redis-unit"].Online || states["redis-unit"].Latest == nil {
		t.Fatal("heartbeat must expire while latest persists")
	}
	stale := host(1)
	stale.LastSeen = now.Add(-time.Minute)
	if err = s.Put(ctx, stale); err != nil {
		t.Fatal(err)
	}
	states, err = s.States(ctx, []string{"redis-unit"})
	if err != nil {
		t.Fatal(err)
	}
	if states["redis-unit"].Online {
		t.Fatal("stale completion resurrected heartbeat")
	}
	fresh := host(19)
	fresh.LastSeen = time.Now().UTC()
	if err = s.Put(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	states, err = s.States(ctx, []string{"redis-unit"})
	if err != nil || !states["redis-unit"].Online {
		t.Fatal("duplicate did not refresh heartbeat", err)
	}
	server.Close()
	if err = s.Ping(ctx); err == nil {
		t.Fatal("expected dependency failure")
	}
}

func TestExpiredCompletionDoesNotMarkOnline(t *testing.T) {
	server := miniredis.RunT(t)
	s := Open(config.Collector{RedisAddr: server.Addr(), DependencyTimeout: time.Second, HeartbeatTTL: time.Second})
	defer s.Close()
	h := storage.Host{ID: "delayed", LastSeen: time.Now().Add(-time.Minute), Latest: metrics.Snapshot{NodeID: "delayed", CollectedAt: time.Now().Add(-time.Minute)}}
	if err := s.Put(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	states, err := s.States(context.Background(), []string{h.ID})
	if err != nil {
		t.Fatal(err)
	}
	if states[h.ID].Online {
		t.Fatal(fmt.Sprint(states))
	}
}
