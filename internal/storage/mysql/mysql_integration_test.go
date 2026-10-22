//go:build integration

package mysql

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/config"
	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
)

func TestIntegrationMySQL(t *testing.T) {
	addr := os.Getenv("NODEWATCH_INTEGRATION_MYSQL_ADDR")
	if addr == "" {
		t.Skip("real MySQL not configured: set NODEWATCH_INTEGRATION_MYSQL_ADDR")
	}
	c, err := config.LoadCollector(func(k string) string {
		if k == "NODEWATCH_MYSQL_ADDR" {
			return addr
		}
		return os.Getenv(k)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(c.MySQLDatabase, "_test") {
		t.Fatal("integration database name must end in _test")
	}
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("migration replay failed", err)
	}
	if err = s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("integration-%d", now.UnixNano())
	defer func() {
		_, _ = s.db.Exec("DELETE FROM hosts WHERE host_id = ?", id)
		_, _ = s.db.Exec("DELETE FROM snapshots WHERE host_id = ?", id)
	}()
	snapshot := metrics.Snapshot{SchemaVersion: 1, NodeID: id, OS: "linux", Architecture: "amd64", CollectedAt: now.Add(-time.Minute), CPU: &metrics.CPU{UtilizationPercent: 10}}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, duplicate, err := s.Accept(ctx, snapshot, now)
			if err != nil {
				t.Error(err)
				return
			}
			if !duplicate {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("expected exactly one insert", accepted.Load())
	}
	var count int
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM snapshots WHERE host_id = ?", id).Scan(&count); err != nil || count != 1 {
		t.Fatal("uniqueness failed", count, err)
	}
	conflict := snapshot
	conflict.CPU = &metrics.CPU{UtilizationPercent: 11}
	if _, _, err = s.Accept(ctx, conflict, now); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("expected conflict", err)
	}
	newer := snapshot
	newer.CollectedAt = newer.CollectedAt.Add(time.Nanosecond)
	if _, _, err = s.Accept(ctx, newer, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Accept(ctx, snapshot, now); err != nil {
		t.Fatal(err)
	}
	h, err := s.Host(ctx, id)
	if err != nil || !h.Latest.CollectedAt.Equal(newer.CollectedAt) {
		t.Fatal("latest regression or nanosecond loss", err)
	}
	q := storage.Query{From: snapshot.CollectedAt, To: now, Limit: 1}
	rows, err := s.Query(ctx, id, q)
	if err != nil || len(rows) != 1 || !rows[0].CollectedAt.Equal(newer.CollectedAt) {
		t.Fatal("query failed", err)
	}
	q.Before = &newer.CollectedAt
	rows, err = s.Query(ctx, id, q)
	if err != nil || len(rows) != 1 || !rows[0].CollectedAt.Equal(snapshot.CollectedAt) {
		t.Fatal("pagination failed", err)
	}
	if _, err = s.Host(ctx, id+"-absent"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("unknown host", err)
	}
}
