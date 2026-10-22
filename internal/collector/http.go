package collector

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/alerts"
	"github.com/RaafidAfraazG/NodeWatch/internal/config"
	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
	"github.com/RaafidAfraazG/NodeWatch/internal/observability"
	"github.com/RaafidAfraazG/NodeWatch/internal/recovery"
	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type API struct {
	history storage.History
	cache   storage.Cache
	config  config.Collector
	logger  *slog.Logger
	now     func() time.Time
	alerts  *alerts.Engine
	source  *HostSource
	metrics *observability.Metrics
}

func New(c config.Collector, h storage.History, cache storage.Cache, logger *slog.Logger, engine *alerts.Engine, observed ...*observability.Metrics) http.Handler {
	collectorMetrics := observability.New()
	if len(observed) > 0 && observed[0] != nil {
		collectorMetrics = observed[0]
	}
	h = observability.WrapHistory(h, collectorMetrics)
	cache = observability.WrapCache(cache, collectorMetrics)
	a := &API{history: h, cache: cache, config: c, logger: logger, now: time.Now, alerts: engine, source: NewHostSource(h, cache, c.FreshnessThreshold, time.Now), metrics: collectorMetrics}
	mux := http.NewServeMux()
	register := func(path, method string, handler http.HandlerFunc) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != method {
				w.Header().Set("Allow", method)
				fail(w, 405, "method_not_allowed", "method not allowed")
				return
			}
			handler(w, r)
		})
	}
	register("/healthz", "GET", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	register("/readyz", "GET", a.ready)
	register("/api/v1/metrics", "POST", a.ingest)
	register("/api/v1/hosts", "GET", a.list)
	register("/api/v1/hosts/{id}", "GET", a.host)
	register("/api/v1/hosts/{id}/metrics", "GET", a.historyQuery)
	register("/api/v1/alerts", "GET", a.alertList)
	register("/api/v1/alerts/{id}", "GET", a.alert)
	register("/dashboard", "GET", a.dashboard)
	metricsHandler := promhttp.HandlerFor(collectorMetrics.Registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
	mux.Handle("/metrics", metricsHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "not_found", "route not found") })
	timed := http.TimeoutHandler(mux, c.RequestTimeout, `{"error":{"code":"request_timeout","message":"request timed out"}}`)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Do not count a Prometheus scrape in the metrics it scrapes.
		if r.URL.Path == "/metrics" {
			metricsHandler.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Cache-Control", "no-store")
		defer func() {
			a.logger.Info("HTTP request", "method", r.Method, "status", rw.status, "duration_ms", time.Since(start).Milliseconds())
			a.metrics.ObserveHTTP(r.Method, routeName(r.URL.Path), rw.status, time.Since(start))
		}()
		timed.ServeHTTP(rw, r)
	})
}

func routeName(path string) string {
	switch {
	case path == "/healthz", path == "/readyz", path == "/dashboard", path == "/api/v1/metrics", path == "/api/v1/hosts", path == "/api/v1/alerts":
		return path
	case strings.HasPrefix(path, "/api/v1/hosts/") && strings.HasSuffix(path, "/metrics"):
		return "/api/v1/hosts/{id}/metrics"
	case strings.HasPrefix(path, "/api/v1/hosts/"):
		return "/api/v1/hosts/{id}"
	case strings.HasPrefix(path, "/api/v1/alerts/"):
		return "/api/v1/alerts/{id}"
	default:
		return "not_found"
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	// TimeoutHandler defaults to text/html even with a JSON timeout body.
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Content-Type", "application/json")
	}
	w.ResponseWriter.WriteHeader(status)
}
func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func (a *API) unavailable(w http.ResponseWriter, dependency string) {
	a.logger.Warn("dependency operation failed", "dependency", dependency)
	w.Header().Set("Retry-After", "1")
	fail(w, 503, "dependency_unavailable", "required storage unavailable; retry the same request")
}
func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	myErr := a.history.Ping(r.Context())
	redisErr := a.cache.Ping(r.Context())
	if myErr != nil || redisErr != nil {
		fail(w, 503, "not_ready", "required dependencies or schema unavailable")
		return
	}
	write(w, 200, map[string]string{"status": "ready"})
}
func (a *API) ingest(w http.ResponseWriter, r *http.Request) {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		a.metrics.ObserveIngestion("rejected", "media_type")
		fail(w, 415, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	if r.Header.Get("Content-Encoding") != "" {
		a.metrics.ObserveIngestion("rejected", "encoding")
		fail(w, 415, "unsupported_encoding", "compressed requests are not supported")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.config.MaxBodyBytes)
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			a.metrics.ObserveIngestion("rejected", "body_size")
			fail(w, 413, "body_too_large", "request body exceeds limit")
		} else {
			a.metrics.ObserveIngestion("rejected", "body")
			fail(w, 400, "invalid_body", "cannot read request body")
		}
		return
	}
	s, err := decodeSnapshot(data)
	if err != nil {
		a.metrics.ObserveIngestion("rejected", "json")
		fail(w, 400, "invalid_json", "expected one strict, complete snapshot JSON object")
		return
	}
	now := a.now().UTC()
	if err = metrics.Validate(s, now); err != nil {
		a.metrics.ObserveIngestion("rejected", "validation")
		fail(w, 422, "invalid_snapshot", err.Error())
		return
	}
	h, duplicate, err := a.history.Accept(r.Context(), s, now)
	if errors.Is(err, storage.ErrConflict) {
		a.metrics.ObserveIngestion("conflict", "conflict")
		fail(w, 409, "snapshot_conflict", "different data already exists for this node_id and collected_at")
		return
	}
	if err != nil {
		a.metrics.ObserveIngestion("failed", "mysql")
		a.unavailable(w, "mysql")
		return
	}
	if err = recovery.PutWithRetry(r.Context(), a.cache, h, a.config.CacheRetryAttempts); err != nil {
		a.metrics.ObserveIngestion("failed", "redis")
		a.unavailable(w, "redis")
		return
	}
	status, word := 201, "accepted"
	if duplicate {
		status, word = 200, "duplicate"
	}
	a.metrics.ObserveIngestion(word, "")
	write(w, status, map[string]any{"status": word, "node_id": s.NodeID, "collected_at": s.CollectedAt})
}
func hostID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !metrics.ValidNodeID(id) {
		fail(w, 400, "invalid_host_id", "invalid host ID")
		return "", false
	}
	return id, true
}
func (a *API) getHost(w http.ResponseWriter, r *http.Request, id string) (storage.Host, bool) {
	h, err := a.history.Host(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		fail(w, 404, "host_not_found", "host has never reported")
		return h, false
	}
	if err != nil {
		a.unavailable(w, "mysql")
		return h, false
	}
	return h, true
}
func (a *API) host(w http.ResponseWriter, r *http.Request) {
	id, ok := hostID(w, r)
	if !ok {
		return
	}
	h, ok := a.getHost(w, r, id)
	if !ok {
		return
	}
	hosts, err := a.source.withStates(r.Context(), []storage.Host{h})
	if err != nil {
		a.unavailable(w, "redis")
		return
	}
	write(w, 200, hosts[0])
}
func queryValues(r *http.Request, allowed ...string) (url.Values, error) {
	v, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for _, k := range allowed {
		keys[k] = true
	}
	for k, values := range v {
		if !keys[k] || len(values) != 1 || values[0] == "" {
			return nil, errors.New("unknown, repeated, or empty query parameter")
		}
	}
	return v, nil
}
func parseLimit(v string, def, max int) (int, error) {
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > max {
		return 0, errors.New("limit out of range")
	}
	return n, nil
}
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	v, err := queryValues(r, "after", "limit")
	if err != nil {
		fail(w, 400, "invalid_query", err.Error())
		return
	}
	limit, err := parseLimit(v.Get("limit"), 50, 200)
	if err != nil || (v.Get("after") != "" && !metrics.ValidNodeID(v.Get("after"))) {
		fail(w, 400, "invalid_query", "invalid cursor or limit (1-200)")
		return
	}
	hosts, err := a.source.List(r.Context(), v.Get("after"), limit)
	if err != nil {
		a.unavailable(w, "mysql")
		return
	}
	a.metrics.ObserveHosts(hosts, v.Get("after") == "" && len(hosts) < limit)
	next := ""
	if len(hosts) > 0 {
		next = hosts[len(hosts)-1].ID
	}
	write(w, 200, map[string]any{"hosts": hosts, "next_after": next})
}
func (a *API) alertList(w http.ResponseWriter, r *http.Request) {
	if a.alerts == nil {
		fail(w, 503, "alerts_unavailable", "alert engine unavailable")
		return
	}
	v, err := queryValues(r, "state", "limit")
	if err != nil {
		fail(w, 400, "invalid_query", err.Error())
		return
	}
	limit, err := parseLimit(v.Get("limit"), 100, 500)
	if err != nil {
		fail(w, 400, "invalid_query", "invalid alert limit (1-500)")
		return
	}
	items, err := a.alerts.List(v.Get("state"), limit)
	if err != nil {
		fail(w, 400, "invalid_query", err.Error())
		return
	}
	pending, firing, resolved := a.alerts.Counts()
	a.metrics.ObserveAlertCounts(pending, firing, resolved)
	write(w, 200, map[string]any{"alerts": items})
}
func (a *API) alert(w http.ResponseWriter, r *http.Request) {
	if a.alerts == nil {
		fail(w, 503, "alerts_unavailable", "alert engine unavailable")
		return
	}
	id := r.PathValue("id")
	item, ok := a.alerts.Get(id)
	if !ok {
		fail(w, 404, "alert_not_found", "alert not found")
		return
	}
	write(w, 200, item)
}
func historyBounds(r *http.Request, now time.Time) (storage.Query, error) {
	q := storage.Query{From: now.Add(-time.Hour), To: now, Limit: 100}
	v, err := queryValues(r, "from", "to", "before", "limit")
	if err != nil {
		return q, err
	}
	for _, p := range []struct {
		key    string
		target *time.Time
	}{{"from", &q.From}, {"to", &q.To}} {
		if raw := v.Get(p.key); raw != "" {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return q, errors.New("timestamps must be RFC3339")
			}
			*p.target = t.UTC()
		}
	}
	q.Limit, err = parseLimit(v.Get("limit"), 100, 1000)
	if err != nil {
		return q, err
	}
	if !q.From.Before(q.To) || q.To.Sub(q.From) > 31*24*time.Hour || q.From.Year() < 2000 || q.To.Year() > 2261 || q.To.After(now.Add(2*time.Minute)) {
		return q, errors.New("range must be increasing, at most 31 days, and not in the future")
	}
	if raw := v.Get("before"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || t.Before(q.From) || t.After(q.To) {
			return q, errors.New("before cursor must lie within the range")
		}
		t = t.UTC()
		q.Before = &t
	}
	return q, nil
}
func (a *API) historyQuery(w http.ResponseWriter, r *http.Request) {
	id, ok := hostID(w, r)
	if !ok {
		return
	}
	q, err := historyBounds(r, a.now().UTC())
	if err != nil {
		fail(w, 400, "invalid_query", err.Error())
		return
	}
	if _, ok = a.getHost(w, r, id); !ok {
		return
	}
	snapshots, err := a.history.Query(r.Context(), id, q)
	if err != nil {
		a.unavailable(w, "mysql")
		return
	}
	var next *time.Time
	if len(snapshots) > 0 {
		t := snapshots[len(snapshots)-1].CollectedAt
		next = &t
	}
	write(w, 200, map[string]any{"metrics": snapshots, "from": q.From, "to": q.To, "next_before": next})
}
