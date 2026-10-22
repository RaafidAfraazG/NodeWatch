package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/agent"
	"github.com/RaafidAfraazG/NodeWatch/internal/alerts"
	"github.com/RaafidAfraazG/NodeWatch/internal/config"
	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
	"github.com/RaafidAfraazG/NodeWatch/internal/observability"
	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
)

// These fakes verify HTTP orchestration, not actual database behavior.
type memoryHistory struct {
	mu      sync.Mutex
	hosts   map[string]storage.Host
	rows    map[string][]byte
	failed  bool
	accepts int
	query   storage.Query
}

func newHistory() *memoryHistory {
	return &memoryHistory{hosts: map[string]storage.Host{}, rows: map[string][]byte{}}
}
func (h *memoryHistory) Ping(context.Context) error {
	if h.failed {
		return errors.New("secret internal failure")
	}
	return nil
}
func (h *memoryHistory) Accept(ctx context.Context, s metrics.Snapshot, seen time.Time) (storage.Host, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.accepts++
	if h.failed {
		return storage.Host{}, false, errors.New("secret SQL failure")
	}
	key := s.NodeID + "/" + s.CollectedAt.Format(time.RFC3339Nano)
	data, _ := json.Marshal(s)
	old, duplicate := h.rows[key]
	if duplicate && !bytes.Equal(old, data) {
		return storage.Host{}, false, storage.ErrConflict
	}
	h.rows[key] = data
	host, exists := h.hosts[s.NodeID]
	if !exists || s.CollectedAt.After(host.Latest.CollectedAt) {
		host.Latest = s
	}
	host.ID = s.NodeID
	host.LastSeen = seen
	h.hosts[s.NodeID] = host
	return host, duplicate, nil
}
func (h *memoryHistory) Host(ctx context.Context, id string) (storage.Host, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failed {
		return storage.Host{}, errors.New("secret SQL failure")
	}
	v, ok := h.hosts[id]
	if !ok {
		return v, storage.ErrNotFound
	}
	return v, nil
}
func (h *memoryHistory) List(ctx context.Context, after string, limit int) ([]storage.Host, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failed {
		return nil, errors.New("SQL down")
	}
	ids := []string{}
	for id := range h.hosts {
		if id > after {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	result := []storage.Host{}
	for _, id := range ids {
		result = append(result, h.hosts[id])
	}
	return result, nil
}
func (h *memoryHistory) Query(ctx context.Context, id string, q storage.Query) ([]metrics.Snapshot, error) {
	h.query = q
	return []metrics.Snapshot{}, nil
}

type memoryCache struct {
	failed bool
	puts   int
	states map[string]storage.State
}

func (c *memoryCache) Ping(context.Context) error {
	if c.failed {
		return errors.New("secret redis failure")
	}
	return nil
}
func (c *memoryCache) Put(ctx context.Context, h storage.Host) error {
	c.puts++
	if c.failed {
		return errors.New("secret redis failure")
	}
	s := h.Latest
	c.states[h.ID] = storage.State{Latest: &s, Online: true}
	return nil
}
func (c *memoryCache) States(context.Context, []string) (map[string]storage.State, error) {
	if c.failed {
		return nil, errors.New("secret redis failure")
	}
	return c.states, nil
}
func setup(t *testing.T) (http.Handler, *memoryHistory, *memoryCache) {
	t.Helper()
	h := newHistory()
	c := &memoryCache{states: map[string]storage.State{}}
	handler := New(config.Collector{MaxBodyBytes: 1024, RequestTimeout: time.Second, FreshnessThreshold: time.Hour, CacheRetryAttempts: 0}, h, c, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	return handler, h, c
}
func example() metrics.Snapshot {
	return metrics.Snapshot{SchemaVersion: 1, NodeID: "host-a", Hostname: "host-a", OS: "linux", Architecture: "amd64", CollectedAt: time.Now().UTC().Add(-time.Minute), CPU: &metrics.CPU{UtilizationPercent: 25}}
}
func payload(s metrics.Snapshot) string { b, _ := json.Marshal(s); return string(b) }
func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestIngestDuplicateConflictAndPartialFailure(t *testing.T) {
	h, db, cache := setup(t)
	s := example()
	body := payload(s)
	cache.failed = true
	w := request(h, "POST", "/api/v1/metrics", body)
	if w.Code != 503 || len(db.rows) != 1 {
		t.Fatalf("partial failure: %d %s rows=%d", w.Code, w.Body, len(db.rows))
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("internal error leaked")
	}
	cache.failed = false
	w = request(h, "POST", "/api/v1/metrics", body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "duplicate") || len(db.rows) != 1 {
		t.Fatalf("retry: %d %s", w.Code, w.Body)
	}
	s.CPU.UtilizationPercent = 26
	puts := cache.puts
	w = request(h, "POST", "/api/v1/metrics", payload(s))
	if w.Code != 409 || cache.puts != puts {
		t.Fatalf("conflict: %d %s", w.Code, w.Body)
	}
	s.CollectedAt = s.CollectedAt.Add(time.Second)
	w = request(h, "POST", "/api/v1/metrics", payload(s))
	if w.Code != 201 || len(db.rows) != 2 {
		t.Fatalf("new: %d %s", w.Code, w.Body)
	}
	db.failed = true
	puts = cache.puts
	w = request(h, "POST", "/api/v1/metrics", payload(s))
	if w.Code != 503 || cache.puts != puts {
		t.Fatal("cache touched after historical failure")
	}
}

func TestAgentCompatibility(t *testing.T) {
	h, db, _ := setup(t)
	server := httptest.NewServer(h)
	defer server.Close()
	sender := agent.NewSender(server.URL+"/api/v1/metrics", time.Second, 0)
	s := example()
	s.CPU = nil
	s.Errors = map[string]string{"cpu": "warming up: CPU requires two samples"}
	if err := sender.Send(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(db.rows) != 1 {
		t.Fatal("agent snapshot missing")
	}
}

func TestStrictIngestion(t *testing.T) {
	base := payload(example())
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"empty", "", 400}, {"trailing", base + " {}", 400}, {"unknown", strings.Replace(base, `"cpu":`, `"extra":1,"cpu":`, 1), 400},
		{"duplicate", strings.Replace(base, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1), 400},
		{"case alias", strings.Replace(base, `"node_id":`, `"NODE_ID":`, 1), 400},
		{"incomplete CPU", strings.Replace(base, `"utilization_percent":25`, "", 1), 400},
		{"null CPU", strings.Replace(base, `{"utilization_percent":25}`, "null", 1), 400},
		{"negative percentage", strings.Replace(base, `"utilization_percent":25`, `"utilization_percent":-1`, 1), 422},
		{"unsupported schema", strings.Replace(base, `"schema_version":1`, `"schema_version":2`, 1), 422},
		{"bad host", strings.Replace(base, `"node_id":"host-a"`, `"node_id":"a/b"`, 1), 422},
		{"size", strings.Repeat(" ", 1025), 413},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, db, _ := setup(t)
			w := request(h, "POST", "/api/v1/metrics", tt.body)
			if w.Code != tt.status || db.accepts != 0 {
				t.Fatalf("got %d %s, accepts %d", w.Code, w.Body, db.accepts)
			}
		})
	}
	h, _, _ := setup(t)
	r := httptest.NewRequest("POST", "/api/v1/metrics", strings.NewReader(base))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
}

func TestHostsReadinessAndQueries(t *testing.T) {
	h, db, cache := setup(t)
	for _, path := range []string{"/api/v1/hosts/unknown", "/api/v1/hosts/unknown/metrics"} {
		if w := request(h, "GET", path, ""); w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
	if w := request(h, "GET", "/api/v1/hosts/bad%2Fid", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := request(h, "DELETE", "/api/v1/metrics", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request(h, "GET", "/readyz", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	request(h, "POST", "/api/v1/metrics", payload(example()))
	for _, status := range []string{"online", "offline", "unknown"} {
		if status == "offline" {
			s := cache.states["host-a"]
			s.Online = false
			cache.states["host-a"] = s
		}
		if status == "unknown" {
			delete(cache.states, "host-a")
		}
		w := request(h, "GET", "/api/v1/hosts/host-a", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"`+status+`"`) {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	if w := request(h, "GET", "/api/v1/hosts?limit=1", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"next_after":"host-a"`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(h, "GET", "/api/v1/hosts/host-a/metrics?limit=5", ""); w.Code != 200 || db.query.Limit != 5 {
		t.Fatal(w.Code)
	}
	for _, q := range []string{"limit=0", "limit=1001", "limit=2&limit=3", "limit=", "from=bad", "from=2001-01-01T00:00:00Z&to=2002-01-01T00:00:00Z", "before=1999-01-01T00:00:00Z", "bogus=1"} {
		if w := request(h, "GET", "/api/v1/hosts/host-a/metrics?"+q, ""); w.Code != 400 {
			t.Fatalf("%s: %d", q, w.Code)
		}
	}
	cache.failed = true
	for _, path := range []string{"/readyz", "/api/v1/hosts/host-a", "/api/v1/hosts"} {
		if w := request(h, "GET", path, ""); w.Code != 503 {
			t.Fatal(path, w.Code)
		}
	}
	if w := request(h, "GET", "/healthz", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(h, "GET", "/api/v1/hosts/host-a/metrics", ""); w.Code != 200 {
		t.Fatal("history must not require Redis")
	}
	cache.failed = false
	db.failed = true
	if w := request(h, "GET", "/readyz", ""); w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestHostFreshnessAndAlertEndpoints(t *testing.T) {
	db := newHistory()
	cache := &memoryCache{states: map[string]storage.State{}}
	now := time.Now().UTC()
	db.hosts["host-a"] = storage.Host{ID: "host-a", Latest: metrics.Snapshot{NodeID: "host-a", CollectedAt: now.Add(-2 * time.Minute), Filesystems: []metrics.Filesystem{{MountPoint: "/", UsedPercent: 96}}}}
	s := db.hosts["host-a"].Latest
	cache.states["host-a"] = storage.State{Latest: &s, Online: true}
	engine, err := alerts.New(alerts.Config{FilesystemWarning: 85, FilesystemCritical: 95, IOWaitWarning: 20, IOWaitCritical: 40, For: 0, MaxHistory: 10})
	if err != nil {
		t.Fatal(err)
	}
	engine.Evaluate(now, []storage.Host{{ID: "host-a", Status: "offline", Latest: db.hosts["host-a"].Latest}})
	h := New(config.Collector{MaxBodyBytes: 1024, RequestTimeout: time.Second, FreshnessThreshold: time.Minute}, db, cache, slog.New(slog.NewTextHandler(io.Discard, nil)), engine)

	if w := request(h, "GET", "/api/v1/hosts/host-a", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"stale"`) {
		t.Fatalf("stale host: %d %s", w.Code, w.Body)
	}
	w := request(h, "GET", "/api/v1/alerts?state=pending&limit=10", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"rule":"host_offline"`) {
		t.Fatalf("alert list: %d %s", w.Code, w.Body)
	}
	var response struct {
		Alerts []alerts.Alert `json:"alerts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Alerts) == 0 {
		t.Fatalf("decode alert list: %v %#v", err, response)
	}
	if w := request(h, "GET", "/api/v1/alerts/"+response.Alerts[0].ID, ""); w.Code != 200 || !strings.Contains(w.Body.String(), response.Alerts[0].ID) {
		t.Fatalf("alert detail: %d %s", w.Code, w.Body)
	}
	if w := request(h, "GET", "/api/v1/alerts?state=invalid", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

type blockingHistory struct {
	*memoryHistory
	release <-chan struct{}
}

func (h blockingHistory) Ping(ctx context.Context) error { <-ctx.Done(); <-h.release; return ctx.Err() }
func TestRequestDeadline(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := New(config.Collector{RequestTimeout: 20 * time.Millisecond, FreshnessThreshold: time.Hour}, blockingHistory{newHistory(), release}, &memoryCache{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	w := request(h, "GET", "/readyz", "")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "request_timeout") || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestPrometheusMetricsEndpoint(t *testing.T) {
	db := newHistory()
	cache := &memoryCache{states: map[string]storage.State{}}
	observed := observability.New()
	h := New(config.Collector{MaxBodyBytes: 1024, RequestTimeout: time.Second, FreshnessThreshold: time.Hour}, db, cache, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, observed)
	if w := request(h, "POST", "/api/v1/metrics", "{"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := request(h, "GET", "/metrics", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "nodewatch_http_requests_total") || !strings.Contains(w.Body.String(), "nodewatch_ingestion_errors_total") {
		t.Fatalf("metrics endpoint: %d %s", w.Code, w.Body)
	}
}
