package metrics

import (
	"encoding/json"
	"testing"
)

func TestMissingMetricsDifferFromZero(t *testing.T) {
	for _, tt := range []struct {
		name    string
		cpu     *CPU
		present bool
	}{
		{"unavailable", nil, false},
		{"idle", &CPU{UtilizationPercent: 0}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(Snapshot{CPU: tt.cpu})
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if _, ok := fields["cpu"]; ok != tt.present {
				t.Fatalf("incorrect availability: %s", data)
			}
		})
	}
}
