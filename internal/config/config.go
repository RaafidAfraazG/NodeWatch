package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
)

type Config struct {
	CollectorURL string
	NodeID       string
	Interval     time.Duration
	HTTPTimeout  time.Duration
	MaxRetries   int
	Mounts       []string
}

// Load applies defaults, then environment, then flags. Dependencies are injected
// so parsing has no global flags or environment side effects in tests.
func Load(args []string, getenv func(string) string, hostname func() (string, error)) (Config, error) {
	env := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	}
	var c Config
	var interval, timeout, mounts string
	fs := flag.NewFlagSet("nodewatch-agent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.CollectorURL, "collector-url", env("NODEWATCH_COLLECTOR_URL", "http://localhost:8080/api/v1/metrics"), "collector metrics endpoint")
	fs.StringVar(&c.NodeID, "node-id", env("NODEWATCH_NODE_ID", ""), "stable node identifier (default hostname)")
	fs.StringVar(&interval, "interval", env("NODEWATCH_INTERVAL", "10s"), "collection interval")
	fs.StringVar(&timeout, "http-timeout", env("NODEWATCH_HTTP_TIMEOUT", "5s"), "timeout per HTTP attempt")
	fs.StringVar(&mounts, "mounts", env("NODEWATCH_MOUNTS", "/"), "comma-separated absolute Linux filesystem paths")
	var retries string
	fs.StringVar(&retries, "max-retries", env("NODEWATCH_MAX_RETRIES", "3"), "additional attempts, 0 through 5")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() != 0 {
		return c, errors.New("unexpected positional arguments")
	}
	if c.NodeID == "" {
		name, err := hostname()
		if err != nil {
			return c, fmt.Errorf("node ID requires NODEWATCH_NODE_ID or --node-id: %w", err)
		}
		c.NodeID = name
	}
	if !metrics.ValidNodeID(c.NodeID) {
		return c, errors.New("node ID must be 1-128 UTF-8 bytes without surrounding whitespace, control characters, path separators, or dot segments")
	}
	u, err := url.Parse(c.CollectorURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return c, errors.New("collector URL must be absolute HTTP(S), without credentials, query, or fragment")
	}
	c.Interval, err = time.ParseDuration(interval)
	if err != nil || c.Interval < time.Second || c.Interval > 24*time.Hour {
		return c, errors.New("interval must be between 1s and 24h")
	}
	c.HTTPTimeout, err = time.ParseDuration(timeout)
	if err != nil || c.HTTPTimeout <= 0 || c.HTTPTimeout > time.Minute {
		return c, errors.New("HTTP timeout must be greater than zero and at most 1m")
	}
	c.MaxRetries, err = strconv.Atoi(retries)
	if err != nil || c.MaxRetries < 0 || c.MaxRetries > 5 {
		return c, errors.New("max retries must be an integer from 0 to 5")
	}
	seen := map[string]bool{}
	for _, mount := range strings.Split(mounts, ",") {
		mount = strings.TrimSpace(mount)
		if !path.IsAbs(mount) || strings.ContainsAny(mount, "\x00\r\n") {
			return c, errors.New("mounts must contain absolute Linux paths")
		}
		mount = path.Clean(mount)
		if !seen[mount] {
			c.Mounts = append(c.Mounts, mount)
			seen[mount] = true
		}
	}
	return c, nil
}

const Usage = `NodeWatch Linux agent

Flags (override corresponding NODEWATCH_* environment variables):
  --collector-url URL  NODEWATCH_COLLECTOR_URL (http://localhost:8080/api/v1/metrics)
  --node-id ID         NODEWATCH_NODE_ID (hostname)
  --interval DURATION NODEWATCH_INTERVAL (10s; range 1s..24h)
  --http-timeout TIME  NODEWATCH_HTTP_TIMEOUT (5s; range >0..1m)
  --max-retries N      NODEWATCH_MAX_RETRIES (3; range 0..5)
  --mounts PATHS       NODEWATCH_MOUNTS (/; comma-separated absolute Linux paths)
`
