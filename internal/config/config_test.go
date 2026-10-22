package config

import (
	"errors"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	c, err := Load(nil, func(string) string { return "" }, func() (string, error) { return "host-a", nil })
	if err != nil {
		t.Fatal(err)
	}
	if c.NodeID != "host-a" || c.Interval != 10*time.Second || c.HTTPTimeout != 5*time.Second || c.MaxRetries != 3 || c.CollectorURL != "http://localhost:8080/api/v1/metrics" || len(c.Mounts) != 1 || c.Mounts[0] != "/" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestConfigValidation(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		invalid bool
	}{
		{"valid", []string{"--node-id", "node-a", "--interval", "2s", "--mounts", "/,/data,/"}, false},
		{"bad scheme", []string{"--collector-url", "ftp://host"}, true},
		{"missing host", []string{"--collector-url", "http:///metrics"}, true},
		{"userinfo", []string{"--collector-url", "https://user:pass@host"}, true},
		{"query", []string{"--collector-url", "https://host?token=x"}, true},
		{"short interval", []string{"--interval", "0s"}, true},
		{"long interval", []string{"--interval", "25h"}, true},
		{"bad interval", []string{"--interval", "hello"}, true},
		{"timeout", []string{"--http-timeout", "0s"}, true},
		{"long timeout", []string{"--http-timeout", "2m"}, true},
		{"retry limit", []string{"--max-retries", "6"}, true},
		{"negative retries", []string{"--max-retries", "-1"}, true},
		{"malformed retries", []string{"--max-retries", "1x"}, true},
		{"relative mount", []string{"--mounts", "data"}, true},
		{"empty mount", []string{"--mounts", ""}, true},
		{"bad id", []string{"--node-id", " "}, true},
		{"positional", []string{"extra"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Load(tt.args, func(string) string { return "" }, func() (string, error) { return "host", nil })
			if (err != nil) != tt.invalid {
				t.Fatalf("config=%+v error=%v", c, err)
			}
			if tt.name == "valid" && len(c.Mounts) != 2 {
				t.Fatal("mounts not deduplicated")
			}
		})
	}
}

func TestEnvironmentAndFlagPrecedence(t *testing.T) {
	env := map[string]string{"NODEWATCH_NODE_ID": "env-node", "NODEWATCH_INTERVAL": "20s", "NODEWATCH_MAX_RETRIES": "1", "NODEWATCH_MOUNTS": "/data"}
	c, err := Load([]string{"--interval", "3s"}, func(k string) string { return env[k] }, func() (string, error) { t.Fatal("hostname should not be called"); return "", nil })
	if err != nil || c.NodeID != "env-node" || c.Interval != 3*time.Second || c.MaxRetries != 1 || c.Mounts[0] != "/data" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestHostnameFailure(t *testing.T) {
	_, err := Load(nil, func(string) string { return "" }, func() (string, error) { return "", errors.New("unavailable") })
	if err == nil {
		t.Fatal("expected node ID error")
	}
}
