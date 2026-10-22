package alerts

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
)

type pagingSource struct{ after []string }

func (s *pagingSource) List(_ context.Context, after string, limit int) ([]storage.Host, error) {
	s.after = append(s.after, after)
	if after != "" {
		return []storage.Host{{ID: "node-200", Status: "online"}}, nil
	}
	hosts := make([]storage.Host, limit)
	for i := range hosts {
		hosts[i] = storage.Host{ID: fmt.Sprintf("node-%03d", i), Status: "online"}
	}
	return hosts, nil
}

func TestLifecycleSeverityAndDuplicateSuppression(t *testing.T) {
	e, err := New(Config{FilesystemWarning: 85, FilesystemCritical: 95, IOWaitWarning: 20, IOWaitCritical: 40, For: time.Minute, MaxHistory: 10})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wait := 25.0
	h := storage.Host{ID: "node-a", Status: "online", Latest: metrics.Snapshot{CPU: &metrics.CPU{IOWaitPercent: &wait}, Filesystems: []metrics.Filesystem{{MountPoint: "/", UsedPercent: 90}}}}
	e.Evaluate(now, []storage.Host{h})
	items, _ := e.List("", 10)
	if len(items) != 2 || items[0].State != Pending {
		t.Fatalf("%+v", items)
	}
	e.Evaluate(now.Add(30*time.Second), []storage.Host{h})
	items, _ = e.List("", 10)
	if len(items) != 2 || items[0].State != Pending {
		t.Fatal("fired too early")
	}
	e.Evaluate(now.Add(time.Minute), []storage.Host{h})
	items, _ = e.List("", 10)
	for _, a := range items {
		if a.State != Firing || a.FiredAt == nil {
			t.Fatalf("not firing: %+v", a)
		}
	}
	// Re-evaluation does not create a second alert instance or notification state.
	e.Evaluate(now.Add(2*time.Minute), []storage.Host{h})
	items, _ = e.List("", 10)
	if len(items) != 2 {
		t.Fatal("duplicate alerts")
	}
	h.Latest.Filesystems[0].UsedPercent = 80
	wait = 0
	e.Evaluate(now.Add(3*time.Minute), []storage.Host{h})
	items, _ = e.List("resolved", 10)
	if len(items) != 2 {
		t.Fatalf("unresolved: %+v", items)
	}
}
func TestOfflineAndStaleDistinction(t *testing.T) {
	e, _ := New(Config{FilesystemWarning: 85, FilesystemCritical: 95, IOWaitWarning: 20, IOWaitCritical: 40, For: 0, MaxHistory: 10})
	now := time.Now()
	e.Evaluate(now, []storage.Host{{ID: "off", Status: "offline"}, {ID: "stale", Status: "stale"}, {ID: "online", Status: "online"}})
	items, _ := e.List("", 10)
	if len(items) != 1 || items[0].Rule != "host_offline" || items[0].Severity != Critical {
		t.Fatalf("%+v", items)
	}
	e.Evaluate(now.Add(time.Second), []storage.Host{{ID: "off", Status: "online"}})
	items, _ = e.List("resolved", 10)
	if len(items) != 1 {
		t.Fatal("offline recovery did not resolve")
	}
}
func TestConfigurationValidation(t *testing.T) {
	for _, c := range []Config{{}, {FilesystemWarning: 90, FilesystemCritical: 80, IOWaitWarning: 20, IOWaitCritical: 40, MaxHistory: 1}, {FilesystemWarning: 85, FilesystemCritical: 95, IOWaitWarning: 20, IOWaitCritical: 40, For: -time.Second, MaxHistory: 1}} {
		if _, err := New(c); err == nil {
			t.Fatal("expected validation failure")
		}
	}
}

func TestWorkerPagesThroughHosts(t *testing.T) {
	e, err := New(Config{FilesystemWarning: 85, FilesystemCritical: 95, IOWaitWarning: 20, IOWaitCritical: 40, MaxHistory: 10})
	if err != nil {
		t.Fatal(err)
	}
	source := &pagingSource{}
	w, err := NewWorker(e, source, time.Second, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(source.after) != 2 || source.after[0] != "" || source.after[1] != "node-199" {
		t.Fatalf("unexpected cursors: %#v", source.after)
	}
}
