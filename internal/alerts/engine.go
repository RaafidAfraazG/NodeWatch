// Package alerts evaluates snapshot-derived conditions without depending on
// HTTP, MySQL, or Redis implementations.
package alerts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
)

type Severity string

const (
	Warning  Severity = "warning"
	Critical Severity = "critical"
)

type State string

const (
	Pending  State = "pending"
	Firing   State = "firing"
	Resolved State = "resolved"
)

type Config struct {
	FilesystemWarning, FilesystemCritical float64
	IOWaitWarning, IOWaitCritical         float64
	For                                   time.Duration
	MaxHistory                            int
}

func (c Config) Validate() error {
	if c.FilesystemWarning <= 0 || c.FilesystemWarning > 100 || c.FilesystemCritical < c.FilesystemWarning || c.FilesystemCritical > 100 {
		return errors.New("invalid filesystem thresholds")
	}
	if c.IOWaitWarning <= 0 || c.IOWaitWarning > 100 || c.IOWaitCritical < c.IOWaitWarning || c.IOWaitCritical > 100 {
		return errors.New("invalid iowait thresholds")
	}
	if c.For < 0 || c.For > 24*time.Hour || c.MaxHistory < 1 || c.MaxHistory > 100000 {
		return errors.New("invalid alert duration or history limit")
	}
	return nil
}

type Alert struct {
	ID         string     `json:"id"`
	Rule       string     `json:"rule"`
	HostID     string     `json:"host_id"`
	Target     string     `json:"target,omitempty"`
	Severity   Severity   `json:"severity"`
	State      State      `json:"state"`
	Value      float64    `json:"value,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FiredAt    *time.Time `json:"fired_at,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
type condition struct {
	id, rule, host, target string
	severity               Severity
	value                  float64
}
type Engine struct {
	config Config
	mu     sync.RWMutex
	items  map[string]Alert
	order  []string
}

func New(c Config) (*Engine, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &Engine{config: c, items: map[string]Alert{}}, nil
}

func (e *Engine) Evaluate(now time.Time, hosts []storage.Host) {
	conditions := map[string]condition{}
	for _, h := range hosts {
		if h.Status == "offline" {
			e.add(conditions, condition{id: alertID("host_offline", h.ID, ""), rule: "host_offline", host: h.ID, severity: Critical})
		}
		for _, fs := range h.Latest.Filesystems {
			severity, ok := e.threshold(fs.UsedPercent, e.config.FilesystemWarning, e.config.FilesystemCritical)
			if ok {
				e.add(conditions, condition{id: alertID("filesystem_usage", h.ID, fs.MountPoint), rule: "filesystem_usage", host: h.ID, target: fs.MountPoint, severity: severity, value: fs.UsedPercent})
			}
		}
		if cpu := h.Latest.CPU; cpu != nil && cpu.IOWaitPercent != nil {
			if severity, ok := e.threshold(*cpu.IOWaitPercent, e.config.IOWaitWarning, e.config.IOWaitCritical); ok {
				e.add(conditions, condition{id: alertID("cpu_iowait", h.ID, ""), rule: "cpu_iowait", host: h.ID, severity: severity, value: *cpu.IOWaitPercent})
			}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, c := range conditions {
		e.apply(now, id, c)
	}
	seenHosts := map[string]bool{}
	for _, h := range hosts {
		seenHosts[h.ID] = true
	}
	for id, a := range e.items {
		if a.State != Resolved && seenHosts[a.HostID] && conditions[id].id == "" {
			a.State = Resolved
			a.ResolvedAt = &now
			a.UpdatedAt = now
			e.items[id] = a
		}
	}
}
func alertID(rule, host, target string) string {
	sum := sha256.Sum256([]byte(rule + "\x00" + host + "\x00" + target))
	return "a_" + hex.EncodeToString(sum[:16])
}
func (e *Engine) add(m map[string]condition, c condition) { m[c.id] = c }
func (e *Engine) threshold(v, w, c float64) (Severity, bool) {
	if v >= c {
		return Critical, true
	}
	if v >= w {
		return Warning, true
	}
	return "", false
}
func (e *Engine) apply(now time.Time, id string, c condition) {
	a, exists := e.items[id]
	if !exists || a.State == Resolved {
		a = Alert{ID: id, Rule: c.rule, HostID: c.host, Target: c.target, Severity: c.severity, State: Pending, Value: c.value, StartedAt: now, UpdatedAt: now}
		e.items[id] = a
		e.order = append(e.order, id)
		e.trim()
		return
	}
	a.Severity = c.severity
	a.Value = c.value
	a.UpdatedAt = now
	if a.State == Pending && !now.Before(a.StartedAt.Add(e.config.For)) {
		a.State = Firing
		a.FiredAt = &now
	}
	e.items[id] = a
}
func (e *Engine) trim() {
	for len(e.order) > e.config.MaxHistory {
		id := e.order[0]
		e.order = e.order[1:]
		delete(e.items, id)
	}
}
func (e *Engine) List(state string, limit int) ([]Alert, error) {
	if state != "" && state != string(Pending) && state != string(Firing) && state != string(Resolved) {
		return nil, errors.New("invalid alert state")
	}
	if limit < 1 || limit > 500 {
		return nil, errors.New("invalid alert limit")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Alert, 0)
	for i := len(e.order) - 1; i >= 0 && len(out) < limit; i-- {
		a := e.items[e.order[i]]
		if state == "" || string(a.State) == state {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}
func (e *Engine) Get(id string) (Alert, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	a, ok := e.items[id]
	return a, ok
}

// Counts returns lifecycle totals without exposing mutable alert state.
func (e *Engine) Counts() (pending, firing, resolved int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, a := range e.items {
		switch a.State {
		case Pending:
			pending++
		case Firing:
			firing++
		case Resolved:
			resolved++
		}
	}
	return pending, firing, resolved
}

type Source interface {
	List(context.Context, string, int) ([]storage.Host, error)
}
type Worker struct {
	engine   *Engine
	source   Source
	interval time.Duration
	now      func() time.Time
	after    string
}

func NewWorker(e *Engine, s Source, interval time.Duration, now func() time.Time) (*Worker, error) {
	if interval < time.Second || interval > time.Hour {
		return nil, errors.New("invalid alert evaluation interval")
	}
	if now == nil {
		now = time.Now
	}
	return &Worker{engine: e, source: s, interval: interval, now: now}, nil
}
func (w *Worker) Evaluate(ctx context.Context) error {
	hosts, err := w.source.List(ctx, w.after, 200)
	if err != nil {
		return err
	}
	w.engine.Evaluate(w.now().UTC(), hosts)
	if len(hosts) < 200 {
		w.after = ""
	} else {
		w.after = hosts[len(hosts)-1].ID
	}
	return nil
}
func (w *Worker) Run(ctx context.Context, report func(error)) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.Evaluate(ctx); err != nil && ctx.Err() == nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
