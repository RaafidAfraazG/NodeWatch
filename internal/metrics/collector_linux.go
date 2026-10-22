//go:build linux

package metrics

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Collector is sampled sequentially by the agent; it is not concurrency safe.
type Collector struct {
	nodeID   string
	mounts   []string
	previous *CPUTimes
}

func NewCollector(nodeID string, mounts []string) *Collector {
	return &Collector{nodeID: nodeID, mounts: append([]string(nil), mounts...)}
}

func (c *Collector) Collect(ctx context.Context) Snapshot {
	s := Snapshot{SchemaVersion: 1, NodeID: c.nodeID, OS: runtime.GOOS, Architecture: runtime.GOARCH, CollectedAt: time.Now().UTC(), Errors: map[string]string{}}
	if err := ctx.Err(); err != nil {
		s.Errors["collection"] = err.Error()
		return s
	}
	host, err := os.Hostname()
	if err != nil {
		s.Errors["hostname"] = err.Error()
	} else {
		s.Hostname = host
	}
	read := func(name, path string, parse func(string) error) {
		if err := ctx.Err(); err != nil {
			s.Errors[name] = err.Error()
			return
		}
		data, err := os.ReadFile(path)
		if err == nil {
			err = parse(string(data))
		} else if name == "cpu" {
			c.previous = nil
		}
		if err != nil {
			s.Errors[name] = err.Error()
		}
	}
	read("cpu", "/proc/stat", func(data string) error {
		current, err := ParseCPU(strings.NewReader(data))
		if err != nil {
			c.previous = nil
			return err
		}
		previous := c.previous
		c.previous = &current
		if previous == nil {
			return errors.New("warming up: CPU requires two samples")
		}
		percent, err := CPUPercent(*previous, current)
		if err == nil {
			iowait, waitErr := IOWaitPercent(*previous, current)
			if waitErr != nil {
				return waitErr
			}
			s.CPU = &CPU{UtilizationPercent: percent, IOWaitPercent: &iowait}
		}
		return err
	})
	read("memory", "/proc/meminfo", func(data string) error {
		v, err := ParseMemory(strings.NewReader(data))
		if err == nil {
			s.Memory = &v
		}
		return err
	})
	read("load", "/proc/loadavg", func(data string) error {
		v, err := ParseLoad(strings.NewReader(data))
		if err == nil {
			s.Load = &v
		}
		return err
	})
	read("disks", "/proc/diskstats", func(data string) error {
		v, err := ParseDisks(strings.NewReader(data))
		s.Disks = v
		return err
	})
	for _, mount := range c.mounts {
		if err := ctx.Err(); err != nil {
			s.Errors["filesystem:"+mount] = err.Error()
			continue
		}
		var stat syscall.Statfs_t
		if err := syscall.Statfs(mount, &stat); err != nil {
			s.Errors["filesystem:"+mount] = err.Error()
			continue
		}
		if stat.Bsize <= 0 || stat.Blocks == 0 || stat.Bfree > stat.Blocks || stat.Bavail > stat.Bfree || stat.Blocks > ^uint64(0)/uint64(stat.Bsize) {
			s.Errors["filesystem:"+mount] = "invalid or unsupported filesystem counters"
			continue
		}
		blockSize := uint64(stat.Bsize)
		s.Filesystems = append(s.Filesystems, Filesystem{mount, stat.Blocks * blockSize, stat.Bfree * blockSize, stat.Bavail * blockSize, 100 * float64(stat.Blocks-stat.Bfree) / float64(stat.Blocks)})
	}
	return s
}
