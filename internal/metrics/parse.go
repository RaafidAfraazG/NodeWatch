package metrics

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

type CPUTimes struct{ Total, Idle, IOWait uint64 }

func ParseCPU(r io.Reader) (CPUTimes, error) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) == 0 || f[0] != "cpu" {
			continue
		}
		if len(f) < 5 {
			return CPUTimes{}, errors.New("incomplete aggregate CPU counters")
		}
		var c CPUTimes
		// guest and guest_nice are already included in user and nice.
		for i := 1; i < len(f) && i <= 8; i++ {
			n, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil || n > math.MaxUint64-c.Total {
				return CPUTimes{}, errors.New("invalid CPU counter")
			}
			c.Total += n
			if i == 4 || i == 5 {
				c.Idle += n
			}
			if i == 5 {
				c.IOWait = n
			}
		}
		return c, nil
	}
	if err := s.Err(); err != nil {
		return CPUTimes{}, err
	}
	return CPUTimes{}, errors.New("aggregate CPU counters missing")
}

func CPUPercent(previous, current CPUTimes) (float64, error) {
	if previous.Idle > previous.Total || current.Idle > current.Total || previous.IOWait > previous.Idle || current.IOWait > current.Idle || current.Total <= previous.Total || current.Idle < previous.Idle || current.IOWait < previous.IOWait {
		return 0, errors.New("CPU counters reset, invalid, or did not advance")
	}
	total, idle := current.Total-previous.Total, current.Idle-previous.Idle
	if idle > total {
		return 0, errors.New("CPU idle delta exceeds total delta")
	}
	return 100 * float64(total-idle) / float64(total), nil
}

func IOWaitPercent(previous, current CPUTimes) (float64, error) {
	if _, err := CPUPercent(previous, current); err != nil {
		return 0, err
	}
	total := current.Total - previous.Total
	return 100 * float64(current.IOWait-previous.IOWait) / float64(total), nil
}

func ParseMemory(r io.Reader) (Memory, error) {
	s := bufio.NewScanner(r)
	values := make(map[string]uint64)
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) == 0 || (f[0] != "MemTotal:" && f[0] != "MemAvailable:") {
			continue
		}
		if len(f) != 3 || f[2] != "kB" {
			return Memory{}, errors.New("invalid memory field or unit")
		}
		if _, exists := values[f[0]]; exists {
			return Memory{}, errors.New("duplicate memory field")
		}
		n, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil || n > math.MaxUint64/1024 {
			return Memory{}, errors.New("invalid memory counter")
		}
		values[f[0]] = n * 1024
	}
	if err := s.Err(); err != nil {
		return Memory{}, err
	}
	total, ok := values["MemTotal:"]
	available, haveAvailable := values["MemAvailable:"]
	if !ok || !haveAvailable || total == 0 || available > total {
		return Memory{}, errors.New("missing or inconsistent memory counters")
	}
	return Memory{total, available, 100 * float64(total-available) / float64(total)}, nil
}

func ParseLoad(r io.Reader) (Load, error) {
	s := bufio.NewScanner(r)
	if !s.Scan() {
		if s.Err() != nil {
			return Load{}, s.Err()
		}
		return Load{}, errors.New("load averages missing")
	}
	f := strings.Fields(s.Text())
	if len(f) < 3 {
		return Load{}, errors.New("incomplete load averages")
	}
	var v [3]float64
	for i := range v {
		n, err := strconv.ParseFloat(f[i], 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return Load{}, errors.New("invalid load average")
		}
		v[i] = n
	}
	return Load{v[0], v[1], v[2]}, nil
}

// ParseDisks returns valid device rows even if other rows are malformed.
// Linux diskstats sectors are always 512 bytes, independent of device block size.
func ParseDisks(r io.Reader) ([]Disk, error) {
	s := bufio.NewScanner(r)
	var disks []Disk
	var problems []error
	line := 0
	for s.Scan() {
		line++
		f := strings.Fields(s.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) < 14 {
			problems = append(problems, fmt.Errorf("diskstats line %d: incomplete counters", line))
			continue
		}
		var v [14]uint64
		valid := true
		for i := 0; i < 14; i++ {
			if i == 2 {
				continue
			}
			n, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				valid = false
				break
			}
			v[i] = n
		}
		if !valid || v[5] > math.MaxUint64/512 || v[9] > math.MaxUint64/512 {
			problems = append(problems, fmt.Errorf("diskstats line %d: invalid counters", line))
			continue
		}
		disks = append(disks, Disk{f[2], v[3], v[7], v[5] * 512, v[9] * 512})
	}
	if err := s.Err(); err != nil {
		problems = append(problems, err)
	}
	if len(disks) == 0 && len(problems) == 0 {
		problems = append(problems, errors.New("disk counters missing"))
	}
	return disks, errors.Join(problems...)
}
