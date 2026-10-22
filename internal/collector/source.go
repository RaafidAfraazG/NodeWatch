package collector

import (
	"context"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
)

// HostSource joins durable hosts with bounded cache state for alert evaluation.
type HostSource struct {
	history   storage.History
	cache     storage.Cache
	freshness time.Duration
	now       func() time.Time
}

func NewHostSource(history storage.History, cache storage.Cache, freshness time.Duration, now func() time.Time) *HostSource {
	if now == nil {
		now = time.Now
	}
	return &HostSource{history, cache, freshness, now}
}
func (s *HostSource) List(ctx context.Context, after string, limit int) ([]storage.Host, error) {
	hosts, err := s.history.List(ctx, after, limit)
	if err != nil {
		return nil, err
	}
	return s.withStates(ctx, hosts)
}
func (s *HostSource) withStates(ctx context.Context, hosts []storage.Host) ([]storage.Host, error) {
	ids := make([]string, len(hosts))
	for i := range hosts {
		ids[i] = hosts[i].ID
	}
	if len(ids) == 0 {
		return hosts, nil
	}
	states, err := s.cache.States(ctx, ids)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	for i := range hosts {
		state := states[hosts[i].ID]
		hosts[i].Status = "unknown"
		if state.Latest != nil {
			hosts[i].Status = "offline"
			if state.Online {
				hosts[i].Status = "online"
			}
			if state.Latest.CollectedAt.After(hosts[i].Latest.CollectedAt) {
				hosts[i].Latest = *state.Latest
			}
			if state.Online && now.Sub(hosts[i].Latest.CollectedAt) > s.freshness {
				hosts[i].Status = "stale"
			}
		}
	}
	return hosts, nil
}
