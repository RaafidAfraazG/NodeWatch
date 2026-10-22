package mysql

import (
	"testing"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
)

func TestCanonicalSnapshotIdentity(t *testing.T) {
	s := metrics.Snapshot{NodeID: "host-a", CollectedAt: time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC), Errors: map[string]string{"memory": "missing", "cpu": "warmup"}}
	_, first, err := Canonical(s)
	if err != nil {
		t.Fatal(err)
	}
	s.CollectedAt = s.CollectedAt.In(time.FixedZone("offset", 3600))
	s.Errors = map[string]string{"cpu": "warmup", "memory": "missing"}
	_, equivalent, err := Canonical(s)
	if err != nil || first != equivalent {
		t.Fatal("timezone or map ordering changed identity")
	}
	s.Errors["cpu"] = "different"
	_, conflict, _ := Canonical(s)
	if first == conflict {
		t.Fatal("conflicting content undetected")
	}
}
