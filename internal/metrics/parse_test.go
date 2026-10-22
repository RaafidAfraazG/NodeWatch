package metrics

import (
	"math"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestCPUPercent(t *testing.T) {
	for _, tt := range []struct {
		name              string
		previous, current CPUTimes
		want              float64
		invalid           bool
	}{
		{"mixed", CPUTimes{Total: 100, Idle: 40}, CPUTimes{Total: 200, Idle: 65}, 75, false},
		{"idle", CPUTimes{Total: 100, Idle: 40}, CPUTimes{Total: 200, Idle: 140}, 0, false},
		{"busy", CPUTimes{Total: 100, Idle: 40}, CPUTimes{Total: 200, Idle: 40}, 100, false},
		{"unchanged", CPUTimes{Total: 100, Idle: 40}, CPUTimes{Total: 100, Idle: 40}, 0, true},
		{"reset", CPUTimes{Total: 100, Idle: 40}, CPUTimes{Total: 90, Idle: 30}, 0, true},
		{"idle reset", CPUTimes{Total: 100, Idle: 40}, CPUTimes{Total: 200, Idle: 30}, 0, true},
		{"bad delta", CPUTimes{Total: 100, Idle: 40}, CPUTimes{Total: 110, Idle: 60}, 0, true},
		{"bad total", CPUTimes{Total: 10, Idle: 20}, CPUTimes{Total: 100, Idle: 30}, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CPUPercent(tt.previous, tt.current)
			if (err != nil) != tt.invalid || math.Abs(got-tt.want) > 0.0001 {
				t.Fatalf("got %v, %v; want %v, invalid=%v", got, err, tt.want, tt.invalid)
			}
		})
	}
}

func TestIOWaitPercent(t *testing.T) {
	got, err := IOWaitPercent(CPUTimes{Total: 100, Idle: 40, IOWait: 10}, CPUTimes{Total: 200, Idle: 90, IOWait: 35})
	if err != nil || got != 25 {
		t.Fatalf("got %v, %v; want 25", got, err)
	}
	if _, err := IOWaitPercent(CPUTimes{Total: 100, Idle: 40, IOWait: 20}, CPUTimes{Total: 200, Idle: 60, IOWait: 10}); err == nil {
		t.Fatal("expected invalid iowait counter")
	}
}

func TestParseFixtures(t *testing.T) {
	cpu, err := ParseCPU(fixture(t, "stat"))
	if err != nil || cpu.Total != 635 || cpu.Idle != 450 || cpu.IOWait != 50 {
		t.Fatalf("CPU: %+v %v", cpu, err)
	}
	mem, err := ParseMemory(fixture(t, "meminfo"))
	if err != nil || mem.TotalBytes != 8388608 || mem.AvailableBytes != 2097152 || mem.UsedPercent != 75 {
		t.Fatalf("memory: %+v %v", mem, err)
	}
	load, err := ParseLoad(fixture(t, "loadavg"))
	if err != nil || load != (Load{0.25, 1.5, 2.75}) {
		t.Fatalf("load: %+v %v", load, err)
	}
	disks, err := ParseDisks(fixture(t, "diskstats"))
	if err != nil || len(disks) != 2 {
		t.Fatalf("disks: %+v %v", disks, err)
	}
	if disks[0] != (Disk{"sda", 10, 20, 51200, 102400}) || disks[1].WrittenBytes != 204800 {
		t.Fatalf("wrong disk units: %+v", disks)
	}
}

func TestInvalidInput(t *testing.T) {
	for name, inputs := range map[string][]string{
		"cpu":    {"", "cpu0 1 2 3 4", "cpu 1 2", "cpu 1 -2 3 4", "cpu 18446744073709551615 1 0 0"},
		"memory": {"", "MemTotal: 10 kB", "MemTotal: 0 kB\nMemAvailable: 0 kB", "MemTotal: 10 MB\nMemAvailable: 1 kB", "MemTotal: 1 kB\nMemAvailable: 2 kB", "MemTotal: 18446744073709551615 kB\nMemAvailable: 0 kB", "MemTotal: 1 kB\nMemTotal: 1 kB\nMemAvailable: 0 kB"},
		"load":   {"", "1 2", "NaN 1 2", "+Inf 1 2", "-1 2 3", "x 2 3"},
		"disks":  {"", "8 0 sda 1", "8 0 sda x 0 1 0 2 0 3 0 0 0 0", "8 0 sda 1 0 18446744073709551615 0 2 0 3 0 0 0 0"},
	} {
		for _, input := range inputs {
			t.Run(name+"/"+input, func(t *testing.T) {
				r := strings.NewReader(input)
				var err error
				switch name {
				case "cpu":
					_, err = ParseCPU(r)
				case "memory":
					_, err = ParseMemory(r)
				case "load":
					_, err = ParseLoad(r)
				case "disks":
					_, err = ParseDisks(r)
				}
				if err == nil {
					t.Fatal("expected error")
				}
			})
		}
	}
}

func TestDiskPartialResults(t *testing.T) {
	disks, err := ParseDisks(strings.NewReader("broken\n8 0 sda 1 0 2 0 3 0 4 0 0 0 0\n"))
	if err == nil || len(disks) != 1 || disks[0].ReadBytes != 1024 {
		t.Fatalf("got %+v, %v", disks, err)
	}
}
