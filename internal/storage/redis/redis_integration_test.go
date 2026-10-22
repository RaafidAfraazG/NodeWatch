//go:build integration

package redis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/config"
	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
)

func TestIntegrationRedis(t *testing.T) {
	addr := os.Getenv("NODEWATCH_INTEGRATION_REDIS_ADDR")
	if addr == "" {
		t.Skip("real Redis not configured: set NODEWATCH_INTEGRATION_REDIS_ADDR")
	}
	s := Open(config.Collector{RedisAddr: addr, RedisUsername: os.Getenv("NODEWATCH_REDIS_USERNAME"), RedisPassword: os.Getenv("NODEWATCH_REDIS_PASSWORD"), DependencyTimeout: 2 * time.Second, HeartbeatTTL: time.Second})
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Ping(ctx); err != nil {
		t.Fatal("real Redis unavailable", err)
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("integration-%d", now.UnixNano())
	latest, heartbeat := keys(id)
	defer s.client.Del(context.Background(), latest, heartbeat)
	h := storage.Host{ID: id, LastSeen: now, Latest: metrics.Snapshot{NodeID: id, CollectedAt: now}}
	if err := s.Put(ctx, h); err != nil {
		t.Fatal(err)
	}
	states, err := s.States(ctx, []string{id})
	if err != nil || !states[id].Online {
		t.Fatal("heartbeat not set", err)
	}
	older := h
	older.Latest.CollectedAt = now.Add(-time.Second)
	older.LastSeen = now.Add(-time.Second)
	if err = s.Put(ctx, older); err != nil {
		t.Fatal(err)
	}
	states, err = s.States(ctx, []string{id})
	if err != nil || !states[id].Latest.CollectedAt.Equal(now) {
		t.Fatal("latest regressed", err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for {
		states, err = s.States(ctx, []string{id})
		if err != nil {
			t.Fatal(err)
		}
		if !states[id].Online && states[id].Latest != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat did not expire")
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.LastSeen = time.Now().UTC()
	if err = s.Put(ctx, h); err != nil {
		t.Fatal(err)
	}
	states, err = s.States(ctx, []string{id})
	if err != nil || !states[id].Online {
		t.Fatal("duplicate refresh failed", err)
	}
}
