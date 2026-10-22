package config

import (
	"testing"
	"time"
)

func TestCollectorConfig(t *testing.T) {
	base := func(k string) string {
		if k == "NODEWATCH_MYSQL_PASSWORD" {
			return "test-only-placeholder"
		}
		return ""
	}
	c, err := LoadCollector(base)
	if err != nil {
		t.Fatal(err)
	}
	if c.Bind != ":8080" || c.HeartbeatTTL != 30*time.Second || c.FreshnessThreshold != 2*time.Minute || c.AlertFor != 30*time.Second || c.ReconciliationBatchSize != 100 || c.AutoMigrate || c.MySQLMaxOpen != 20 {
		t.Fatalf("bad defaults: %+v", c)
	}
	for _, tt := range []struct{ k, v string }{
		{"NODEWATCH_BIND", "bad"}, {"NODEWATCH_MYSQL_ADDR", "host:99999"}, {"NODEWATCH_REDIS_DB", "-1"},
		{"NODEWATCH_HEARTBEAT_TTL", "0s"}, {"NODEWATCH_REQUEST_TIMEOUT", "1s"}, {"NODEWATCH_MYSQL_MAX_IDLE", "21"},
		{"NODEWATCH_MAX_BODY_BYTES", "999999999"}, {"NODEWATCH_AUTO_MIGRATE", "maybe"}, {"NODEWATCH_MYSQL_PASSWORD", ""},
		{"NODEWATCH_FRESHNESS_THRESHOLD", "0s"}, {"NODEWATCH_ALERT_FOR", "25h"}, {"NODEWATCH_FILESYSTEM_CRITICAL_PERCENT", "80"},
		{"NODEWATCH_IOWAIT_WARNING_PERCENT", "101"}, {"NODEWATCH_RECONCILIATION_BATCH_SIZE", "201"}, {"NODEWATCH_CACHE_RETRY_ATTEMPTS", "9"},
	} {
		t.Run(tt.k, func(t *testing.T) {
			_, err := LoadCollector(func(k string) string {
				if k == tt.k {
					return tt.v
				}
				return base(k)
			})
			if err == nil {
				t.Fatal("expected invalid config")
			}
		})
	}
}
