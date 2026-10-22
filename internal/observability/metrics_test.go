package observability

import (
	"testing"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
)

func TestMetricFamiliesAndBoundedLabels(t *testing.T) {
	m := New()
	m.ObserveHTTP("POST", "/api/v1/metrics", 201, 25*time.Millisecond)
	m.ObserveIngestion("accepted", "")
	m.ObserveIngestion("rejected", "validation")
	m.StorageDuration.WithLabelValues("mysql", "accept", "success").Observe(time.Millisecond.Seconds())
	m.ObserveHosts([]storage.Host{{ID: "a", Status: "online"}, {ID: "b", Status: "offline"}}, true)
	m.ObserveAlertCounts(1, 2, 3)
	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"nodewatch_http_requests_total":                false,
		"nodewatch_http_request_duration_seconds":      false,
		"nodewatch_ingestions_total":                   false,
		"nodewatch_ingestion_errors_total":             false,
		"nodewatch_storage_operation_duration_seconds": false,
		"nodewatch_known_hosts":                        false,
		"nodewatch_online_hosts":                       false,
		"nodewatch_active_alerts":                      false,
	}
	for _, family := range families {
		if _, ok := want[family.GetName()]; ok {
			want[family.GetName()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing metric family %s", name)
		}
	}
}
