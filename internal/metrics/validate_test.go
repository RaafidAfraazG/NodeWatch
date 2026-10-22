package metrics

import (
	"math"
	"testing"
	"time"
)

func TestValidateSnapshot(t *testing.T) {
	now := time.Now().UTC()
	for _, tt := range []struct {
		name string
		edit func(*Snapshot)
	}{
		{"future", func(s *Snapshot) { s.CollectedAt = now.Add(3 * time.Minute) }},
		{"old", func(s *Snapshot) { s.CollectedAt = time.Time{} }},
		{"NaN", func(s *Snapshot) { s.CPU = &CPU{UtilizationPercent: math.NaN()} }},
		{"infinite load", func(s *Snapshot) { s.Load = &Load{math.Inf(1), 0, 0} }},
		{"negative load", func(s *Snapshot) { s.Load = &Load{-1, 0, 0} }},
		{"memory total", func(s *Snapshot) { s.Memory = &Memory{0, 0, 0} }},
		{"memory available", func(s *Snapshot) { s.Memory = &Memory{100, 101, 0} }},
		{"memory percentage", func(s *Snapshot) { s.Memory = &Memory{100, 50, 20} }},
		{"filesystem", func(s *Snapshot) { s.Filesystems = []Filesystem{{"/", 100, 50, 60, 50}} }},
		{"duplicate disks", func(s *Snapshot) { s.Disks = []Disk{{Device: "sda"}, {Device: "sda"}} }},
		{"empty", func(s *Snapshot) { s.CPU = nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := Snapshot{SchemaVersion: 1, NodeID: "host-a", OS: "linux", Architecture: "amd64", CollectedAt: now, CPU: &CPU{UtilizationPercent: 0}}
			if err := Validate(s, now); err != nil {
				t.Fatal(err)
			}
			tt.edit(&s)
			if err := Validate(s, now); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	for _, id := range []string{"", ".", "..", "a/b", "a\\b", " host", "a\x00b"} {
		if ValidNodeID(id) {
			t.Fatalf("accepted %q", id)
		}
	}
	for _, id := range []string{"host-a", "host.example", "host:1", "test host", "服务器"} {
		if !ValidNodeID(id) {
			t.Fatalf("rejected existing ID format %q", id)
		}
	}
}
