//go:build integration

package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/alerts"
	"github.com/RaafidAfraazG/NodeWatch/internal/config"
	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
	mysqlstore "github.com/RaafidAfraazG/NodeWatch/internal/storage/mysql"
	redisstore "github.com/RaafidAfraazG/NodeWatch/internal/storage/redis"
)

func TestIntegrationCollectorIngestionAndOfflineAlert(t *testing.T) {
	mysqlAddr, redisAddr := os.Getenv("NODEWATCH_INTEGRATION_MYSQL_ADDR"), os.Getenv("NODEWATCH_INTEGRATION_REDIS_ADDR")
	if mysqlAddr == "" || redisAddr == "" {
		t.Skip("real MySQL and Redis not configured")
	}
	c := config.Collector{
		MaxBodyBytes: 1 << 20, RequestTimeout: 10 * time.Second, DependencyTimeout: 2 * time.Second, HeartbeatTTL: 150 * time.Millisecond,
		FreshnessThreshold: time.Minute, CacheRetryAttempts: 0, MySQLAddr: mysqlAddr, MySQLUser: os.Getenv("NODEWATCH_MYSQL_USER"),
		MySQLPassword: os.Getenv("NODEWATCH_MYSQL_PASSWORD"), MySQLDatabase: os.Getenv("NODEWATCH_MYSQL_DATABASE"), MySQLMaxOpen: 5,
		MySQLMaxIdle: 2, MySQLConnLifetime: time.Minute, RedisAddr: redisAddr, RedisUsername: os.Getenv("NODEWATCH_REDIS_USERNAME"), RedisPassword: os.Getenv("NODEWATCH_REDIS_PASSWORD"),
	}
	db, err := mysqlstore.Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cache := redisstore.Open(c)
	defer cache.Close()
	if err := cache.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	engine, err := alerts.New(alerts.Config{FilesystemWarning: 85, FilesystemCritical: 95, IOWaitWarning: 20, IOWaitCritical: 40, For: 0, MaxHistory: 10})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(c, db, cache, slog.New(slog.NewTextHandler(io.Discard, nil)), engine)
	id := "collector-integration-" + time.Now().UTC().Format("20060102150405.000000000")
	snapshot := metrics.Snapshot{SchemaVersion: 1, NodeID: id, Hostname: id, OS: "linux", Architecture: "amd64", CollectedAt: time.Now().UTC(), Filesystems: []metrics.Filesystem{{MountPoint: "/", TotalBytes: 1000, FreeBytes: 40, AvailableBytes: 40, UsedPercent: 96}}}
	body, _ := json.Marshal(snapshot)
	post := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := post(); w.Code != http.StatusCreated {
		t.Fatalf("ingestion status %d: %s", w.Code, w.Body)
	}
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("duplicate status %d: %s", w.Code, w.Body)
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	if w := get("/api/v1/hosts/" + id); w.Code != http.StatusOK {
		t.Fatalf("host status %d: %s", w.Code, w.Body)
	}
	if w := get("/api/v1/hosts/" + id + "/metrics?from=" + snapshot.CollectedAt.Add(-time.Second).Format(time.RFC3339Nano) + "&to=" + time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)); w.Code != http.StatusOK {
		t.Fatalf("history status %d: %s", w.Code, w.Body)
	}
	deadline := time.Now().Add(4 * time.Second)
	var hosts []storage.Host
	for {
		source := NewHostSource(db, cache, c.FreshnessThreshold, time.Now)
		host, hostErr := db.Host(ctx, id)
		if hostErr != nil {
			t.Fatal(hostErr)
		}
		hosts, err = source.withStates(ctx, []storage.Host{host})
		if err != nil {
			t.Fatal(err)
		}
		if len(hosts) == 1 && hosts[0].ID == id && hosts[0].Status == "offline" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat did not expire: %+v", hosts)
		}
		time.Sleep(20 * time.Millisecond)
	}
	engine.Evaluate(time.Now().UTC(), hosts)
	engine.Evaluate(time.Now().UTC(), hosts)
	items, err := engine.List("firing", 10)
	foundOffline := false
	for _, item := range items {
		if item.Rule == "host_offline" && item.HostID == id {
			foundOffline = true
			break
		}
	}
	if err != nil || !foundOffline {
		t.Fatalf("offline alert missing: %#v, %v", items, err)
	}
}
