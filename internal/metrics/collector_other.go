//go:build !linux

package metrics

import (
	"context"
	"runtime"
	"time"
)

type Collector struct{ nodeID string }

func NewCollector(nodeID string, mounts []string) *Collector { return &Collector{nodeID: nodeID} }
func (c *Collector) Collect(ctx context.Context) Snapshot {
	return Snapshot{SchemaVersion: 1, NodeID: c.nodeID, OS: runtime.GOOS, Architecture: runtime.GOARCH, CollectedAt: time.Now().UTC(), Errors: map[string]string{"collection": "host metrics are supported only on Linux"}}
}
