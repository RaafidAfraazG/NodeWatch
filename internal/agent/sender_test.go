package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
)

func TestSendStatuses(t *testing.T) {
	for _, tt := range []struct {
		name     string
		statuses []int
		attempts int
		wantErr  bool
	}{
		{"success", []int{204}, 1, false},
		{"retry server", []int{503, 204}, 2, false},
		{"retry rate limit", []int{429, 204}, 2, false},
		{"retry timeout", []int{408, 204}, 2, false},
		{"bounded", []int{500}, 3, true},
		{"bad request", []int{400}, 1, true},
		{"redirect", []int{302}, 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("invalid request")
				}
				var got metrics.Snapshot
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.NodeID != "test-node" {
					t.Errorf("invalid payload: %+v %v", got, err)
				}
				i := int(calls.Add(1)) - 1
				if i >= len(tt.statuses) {
					i = len(tt.statuses) - 1
				}
				w.Header().Set("Location", "/redirect")
				w.WriteHeader(tt.statuses[i])
			}))
			defer srv.Close()
			s := NewSender(srv.URL, time.Second, 2)
			s.backoff = time.Millisecond
			err := s.Send(context.Background(), metrics.Snapshot{NodeID: "test-node"})
			if (err != nil) != tt.wantErr || int(calls.Load()) != tt.attempts {
				t.Fatalf("calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}

func TestCancelBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503); cancel() }))
	defer srv.Close()
	s := NewSender(srv.URL, time.Second, 3)
	s.backoff = time.Hour
	start := time.Now()
	if err := s.Send(ctx, metrics.Snapshot{}); err == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation waited for backoff")
	}
}

func TestHTTPTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	s := NewSender(srv.URL, 25*time.Millisecond, 0)
	start := time.Now()
	if err := s.Send(context.Background(), metrics.Snapshot{}); err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > time.Second {
		t.Fatal("HTTP timeout not bounded")
	}
}

func TestTransportFailureBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	s := NewSender(url, 100*time.Millisecond, 1)
	s.backoff = time.Millisecond
	if err := s.Send(context.Background(), metrics.Snapshot{}); err == nil {
		t.Fatal("expected transport failure")
	}
}

type testSource struct{ calls int }

func (s *testSource) Collect(context.Context) metrics.Snapshot { s.calls++; return metrics.Snapshot{} }

type cancelSink struct{ cancel context.CancelFunc }

func (s cancelSink) Send(context.Context, metrics.Snapshot) error { s.cancel(); return nil }
func TestRunCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &testSource{}
	err := Run(ctx, time.Hour, source, cancelSink{cancel}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || source.calls != 1 {
		t.Fatalf("calls=%d err=%v", source.calls, err)
	}
}
