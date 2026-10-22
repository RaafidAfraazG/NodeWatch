package config

import (
	"fmt"
	"net"
	"strconv"
	"time"
)

type Collector struct {
	Bind                                                                          string
	RequestTimeout, DependencyTimeout, ShutdownTimeout, HeartbeatTTL              time.Duration
	MaxBodyBytes                                                                  int64
	RedisAddr, RedisUsername, RedisPassword                                       string
	RedisDB                                                                       int
	MySQLAddr, MySQLUser, MySQLPassword, MySQLDatabase                            string
	MySQLMaxOpen, MySQLMaxIdle                                                    int
	MySQLConnLifetime                                                             time.Duration
	AutoMigrate                                                                   bool
	FreshnessThreshold, AlertEvaluationInterval, AlertFor, ReconciliationInterval time.Duration
	FilesystemWarning, FilesystemCritical, IOWaitWarning, IOWaitCritical          float64
	ReconciliationBatchSize, ReconciliationMaxBatches, CacheRetryAttempts         int
}

func LoadCollector(getenv func(string) string) (Collector, error) {
	value := func(k, d string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return d
	}
	c := Collector{
		Bind: value("NODEWATCH_BIND", ":8080"), RedisAddr: value("NODEWATCH_REDIS_ADDR", "127.0.0.1:6379"),
		RedisUsername: getenv("NODEWATCH_REDIS_USERNAME"), RedisPassword: getenv("NODEWATCH_REDIS_PASSWORD"),
		MySQLAddr: value("NODEWATCH_MYSQL_ADDR", "127.0.0.1:3306"), MySQLUser: value("NODEWATCH_MYSQL_USER", "nodewatch"),
		MySQLPassword: getenv("NODEWATCH_MYSQL_PASSWORD"), MySQLDatabase: value("NODEWATCH_MYSQL_DATABASE", "nodewatch"),
	}
	for _, item := range []struct{ k, v string }{{"NODEWATCH_BIND", c.Bind}, {"NODEWATCH_REDIS_ADDR", c.RedisAddr}, {"NODEWATCH_MYSQL_ADDR", c.MySQLAddr}} {
		_, p, err := net.SplitHostPort(item.v)
		n, e := strconv.Atoi(p)
		if err != nil || e != nil || n < 1 || n > 65535 {
			return c, fmt.Errorf("%s must be a host:port with port 1-65535", item.k)
		}
	}
	for _, item := range []struct {
		k, d     string
		target   *time.Duration
		min, max time.Duration
	}{
		{"NODEWATCH_REQUEST_TIMEOUT", "10s", &c.RequestTimeout, time.Second, time.Minute},
		{"NODEWATCH_DEPENDENCY_TIMEOUT", "2s", &c.DependencyTimeout, 100 * time.Millisecond, 30 * time.Second},
		{"NODEWATCH_SHUTDOWN_TIMEOUT", "15s", &c.ShutdownTimeout, time.Second, time.Minute},
		{"NODEWATCH_HEARTBEAT_TTL", "30s", &c.HeartbeatTTL, time.Second, 24 * time.Hour},
		{"NODEWATCH_MYSQL_CONN_LIFETIME", "3m", &c.MySQLConnLifetime, time.Second, time.Hour},
		{"NODEWATCH_FRESHNESS_THRESHOLD", "2m", &c.FreshnessThreshold, time.Second, 24 * time.Hour},
		{"NODEWATCH_ALERT_EVALUATION_INTERVAL", "10s", &c.AlertEvaluationInterval, time.Second, time.Hour},
		{"NODEWATCH_ALERT_FOR", "30s", &c.AlertFor, 0, 24 * time.Hour},
		{"NODEWATCH_RECONCILIATION_INTERVAL", "30s", &c.ReconciliationInterval, time.Second, time.Hour},
	} {
		v, err := time.ParseDuration(value(item.k, item.d))
		if err != nil || v < item.min || v > item.max {
			return c, fmt.Errorf("invalid %s", item.k)
		}
		*item.target = v
	}
	for _, item := range []struct {
		k, d     string
		target   *int
		min, max int
	}{
		{"NODEWATCH_REDIS_DB", "0", &c.RedisDB, 0, 15},
		{"NODEWATCH_MYSQL_MAX_OPEN", "20", &c.MySQLMaxOpen, 1, 1000},
		{"NODEWATCH_MYSQL_MAX_IDLE", "5", &c.MySQLMaxIdle, 0, 1000},
		{"NODEWATCH_RECONCILIATION_BATCH_SIZE", "100", &c.ReconciliationBatchSize, 1, 200},
		{"NODEWATCH_RECONCILIATION_MAX_BATCHES", "5", &c.ReconciliationMaxBatches, 1, 100},
		{"NODEWATCH_CACHE_RETRY_ATTEMPTS", "3", &c.CacheRetryAttempts, 0, 8},
	} {
		v, err := strconv.Atoi(value(item.k, item.d))
		if err != nil || v < item.min || v > item.max {
			return c, fmt.Errorf("invalid %s", item.k)
		}
		*item.target = v
	}
	v, err := strconv.ParseInt(value("NODEWATCH_MAX_BODY_BYTES", "1048576"), 10, 64)
	if err != nil || v < 1024 || v > 4*1024*1024 {
		return c, fmt.Errorf("NODEWATCH_MAX_BODY_BYTES must be 1024..4194304")
	}
	c.MaxBodyBytes = v
	c.AutoMigrate, err = strconv.ParseBool(value("NODEWATCH_AUTO_MIGRATE", "false"))
	if err != nil {
		return c, fmt.Errorf("invalid NODEWATCH_AUTO_MIGRATE")
	}
	if c.MySQLMaxIdle > c.MySQLMaxOpen {
		return c, fmt.Errorf("MySQL max idle must not exceed max open")
	}
	if c.RequestTimeout < 2*c.DependencyTimeout {
		return c, fmt.Errorf("request timeout must allow at least two dependency timeouts")
	}
	if c.MySQLPassword == "" {
		return c, fmt.Errorf("NODEWATCH_MYSQL_PASSWORD is required")
	}
	for _, item := range []struct {
		k, d   string
		target *float64
	}{
		{"NODEWATCH_FILESYSTEM_WARNING_PERCENT", "85", &c.FilesystemWarning},
		{"NODEWATCH_FILESYSTEM_CRITICAL_PERCENT", "95", &c.FilesystemCritical},
		{"NODEWATCH_IOWAIT_WARNING_PERCENT", "20", &c.IOWaitWarning},
		{"NODEWATCH_IOWAIT_CRITICAL_PERCENT", "40", &c.IOWaitCritical},
	} {
		v, e := strconv.ParseFloat(value(item.k, item.d), 64)
		if e != nil || v <= 0 || v > 100 {
			return c, fmt.Errorf("invalid %s", item.k)
		}
		*item.target = v
	}
	if c.FilesystemCritical < c.FilesystemWarning || c.IOWaitCritical < c.IOWaitWarning {
		return c, fmt.Errorf("critical alert threshold must not be lower than warning")
	}
	return c, nil
}
