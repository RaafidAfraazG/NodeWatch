package metrics

import (
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ValidNodeID preserves the agent's existing IDs while excluding path separators
// and dot segments, which cannot be safely addressed by the host API routes.
func ValidNodeID(id string) bool {
	return validText(id, 128) && id != "." && id != ".." && !strings.ContainsAny(id, "/\\")
}

func validText(s string, max int) bool {
	return s != "" && len(s) <= max && utf8.ValidString(s) && strings.TrimSpace(s) == s && strings.IndexFunc(s, unicode.IsControl) < 0
}

func Validate(s Snapshot, now time.Time) error {
	bad := func(message string) error { return errors.New(message) }
	if s.SchemaVersion != 1 {
		return bad("schema_version must be 1")
	}
	if !ValidNodeID(s.NodeID) {
		return bad("invalid node_id")
	}
	if !validText(s.OS, 32) || !validText(s.Architecture, 32) || (s.Hostname != "" && !validText(s.Hostname, 255)) {
		return bad("invalid host metadata")
	}
	if s.CollectedAt.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || s.CollectedAt.After(now.Add(2*time.Minute)) || s.CollectedAt.Year() > 2261 {
		return bad("collected_at must be from 2000 onward and no more than 2 minutes in the future")
	}
	percent := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 100 }
	if s.CPU != nil && (!percent(s.CPU.UtilizationPercent) || (s.CPU.IOWaitPercent != nil && !percent(*s.CPU.IOWaitPercent))) {
		return bad("invalid CPU percentage")
	}
	if m := s.Memory; m != nil {
		if m.TotalBytes == 0 || m.AvailableBytes > m.TotalBytes || !percent(m.UsedPercent) || math.Abs(m.UsedPercent-100*float64(m.TotalBytes-m.AvailableBytes)/float64(m.TotalBytes)) > 0.01 {
			return bad("inconsistent memory metrics")
		}
	}
	if l := s.Load; l != nil {
		for _, v := range []float64{l.One, l.Five, l.Fifteen} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return bad("invalid load average")
			}
		}
	}
	if len(s.Filesystems) > 256 || len(s.Disks) > 4096 || len(s.Errors) > 512 {
		return bad("too many metric entries")
	}
	seen := map[string]bool{}
	for _, f := range s.Filesystems {
		if !validText(f.MountPoint, 4096) || !strings.HasPrefix(f.MountPoint, "/") || seen[f.MountPoint] || f.TotalBytes == 0 || f.FreeBytes > f.TotalBytes || f.AvailableBytes > f.FreeBytes || !percent(f.UsedPercent) || math.Abs(f.UsedPercent-100*float64(f.TotalBytes-f.FreeBytes)/float64(f.TotalBytes)) > 0.01 {
			return bad("invalid filesystem metrics")
		}
		seen[f.MountPoint] = true
	}
	seen = map[string]bool{}
	for _, d := range s.Disks {
		if !validText(d.Device, 255) || seen[d.Device] {
			return bad("invalid or duplicate disk device")
		}
		seen[d.Device] = true
	}
	for k, v := range s.Errors {
		if !validText(k, 4096) || !validText(v, 8192) {
			return bad("invalid metric error description")
		}
	}
	if s.CPU == nil && s.Memory == nil && s.Load == nil && len(s.Filesystems) == 0 && len(s.Disks) == 0 && len(s.Errors) == 0 {
		return bad("snapshot must contain measurements or explicit metric errors")
	}
	return nil
}
