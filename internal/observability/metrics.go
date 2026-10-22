// Package observability provides bounded Prometheus instrumentation for the collector.
package observability

import (
	"context"
	"strconv"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics uses a private registry so tests and independently constructed
// collectors never collide in the global Prometheus registry.
type Metrics struct {
	Registry        *prometheus.Registry
	HTTPRequests    *prometheus.CounterVec
	HTTPDuration    *prometheus.HistogramVec
	Ingestions      *prometheus.CounterVec
	IngestionErrors *prometheus.CounterVec
	StorageDuration *prometheus.HistogramVec
	KnownHosts      prometheus.Gauge
	OnlineHosts     prometheus.Gauge
	ActiveAlerts    *prometheus.GaugeVec
}

func New() *Metrics {
	m := &Metrics{Registry: prometheus.NewRegistry()}
	m.HTTPRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nodewatch_http_requests_total", Help: "Completed collector HTTP requests."}, []string{"method", "route", "status"})
	m.HTTPDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "nodewatch_http_request_duration_seconds", Help: "Collector HTTP request duration."}, []string{"method", "route"})
	m.Ingestions = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nodewatch_ingestions_total", Help: "Snapshot ingestion outcomes."}, []string{"outcome"})
	m.IngestionErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nodewatch_ingestion_errors_total", Help: "Bounded snapshot ingestion error categories."}, []string{"category"})
	m.StorageDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "nodewatch_storage_operation_duration_seconds", Help: "Storage operation duration."}, []string{"backend", "operation", "outcome"})
	m.KnownHosts = prometheus.NewGauge(prometheus.GaugeOpts{Name: "nodewatch_known_hosts", Help: "Known hosts in the most recent complete host scan."})
	m.OnlineHosts = prometheus.NewGauge(prometheus.GaugeOpts{Name: "nodewatch_online_hosts", Help: "Online hosts in the most recent complete host scan."})
	m.ActiveAlerts = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nodewatch_active_alerts", Help: "Current alert count by lifecycle state."}, []string{"state"})
	m.Registry.MustRegister(m.HTTPRequests, m.HTTPDuration, m.Ingestions, m.IngestionErrors, m.StorageDuration, m.KnownHosts, m.OnlineHosts, m.ActiveAlerts)
	m.Registry.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	return m
}

func (m *Metrics) ObserveHTTP(method, route string, status int, elapsed time.Duration) {
	m.HTTPRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.HTTPDuration.WithLabelValues(method, route).Observe(elapsed.Seconds())
}
func (m *Metrics) ObserveIngestion(outcome, category string) {
	m.Ingestions.WithLabelValues(outcome).Inc()
	if category != "" {
		m.IngestionErrors.WithLabelValues(category).Inc()
	}
}
func (m *Metrics) ObserveHosts(hosts []storage.Host, complete bool) {
	if !complete {
		return
	}
	online := 0
	for _, host := range hosts {
		if host.Status == "online" {
			online++
		}
	}
	m.KnownHosts.Set(float64(len(hosts)))
	m.OnlineHosts.Set(float64(online))
}
func (m *Metrics) ObserveAlertCounts(pending, firing, resolved int) {
	m.ActiveAlerts.WithLabelValues("pending").Set(float64(pending))
	m.ActiveAlerts.WithLabelValues("firing").Set(float64(firing))
	m.ActiveAlerts.WithLabelValues("resolved").Set(float64(resolved))
}

type history struct {
	next    storage.History
	metrics *Metrics
}

func WrapHistory(next storage.History, m *Metrics) storage.History { return history{next, m} }
func (h history) observe(operation string, start time.Time, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	h.metrics.StorageDuration.WithLabelValues("mysql", operation, outcome).Observe(time.Since(start).Seconds())
}
func (h history) Ping(ctx context.Context) error {
	start := time.Now()
	err := h.next.Ping(ctx)
	h.observe("ping", start, err)
	return err
}
func (h history) Accept(ctx context.Context, s metrics.Snapshot, seen time.Time) (storage.Host, bool, error) {
	start := time.Now()
	host, duplicate, err := h.next.Accept(ctx, s, seen)
	h.observe("accept", start, err)
	return host, duplicate, err
}
func (h history) Host(ctx context.Context, id string) (storage.Host, error) {
	start := time.Now()
	host, err := h.next.Host(ctx, id)
	h.observe("host", start, err)
	return host, err
}
func (h history) List(ctx context.Context, after string, limit int) ([]storage.Host, error) {
	start := time.Now()
	hosts, err := h.next.List(ctx, after, limit)
	h.observe("list", start, err)
	return hosts, err
}
func (h history) Query(ctx context.Context, id string, q storage.Query) ([]metrics.Snapshot, error) {
	start := time.Now()
	snapshots, err := h.next.Query(ctx, id, q)
	h.observe("query", start, err)
	return snapshots, err
}

type cache struct {
	next    storage.Cache
	metrics *Metrics
}

func WrapCache(next storage.Cache, m *Metrics) storage.Cache { return cache{next, m} }
func (c cache) observe(operation string, start time.Time, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	c.metrics.StorageDuration.WithLabelValues("redis", operation, outcome).Observe(time.Since(start).Seconds())
}
func (c cache) Ping(ctx context.Context) error {
	start := time.Now()
	err := c.next.Ping(ctx)
	c.observe("ping", start, err)
	return err
}
func (c cache) Put(ctx context.Context, host storage.Host) error {
	start := time.Now()
	err := c.next.Put(ctx, host)
	c.observe("put", start, err)
	return err
}
func (c cache) States(ctx context.Context, ids []string) (map[string]storage.State, error) {
	start := time.Now()
	states, err := c.next.States(ctx, ids)
	c.observe("states", start, err)
	return states, err
}
