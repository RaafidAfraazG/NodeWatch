package metrics

import "time"

// Snapshot is the version 1 wire contract. Missing metrics are omitted and
// explained by Errors; a missing value must never be interpreted as zero.
type Snapshot struct {
	SchemaVersion int               `json:"schema_version"`
	NodeID        string            `json:"node_id"`
	Hostname      string            `json:"hostname,omitempty"`
	OS            string            `json:"os"`
	Architecture  string            `json:"architecture"`
	CollectedAt   time.Time         `json:"collected_at"`
	CPU           *CPU              `json:"cpu,omitempty"`
	Memory        *Memory           `json:"memory,omitempty"`
	Load          *Load             `json:"load,omitempty"`
	Filesystems   []Filesystem      `json:"filesystems,omitempty"`
	Disks         []Disk            `json:"disks,omitempty"`
	Errors        map[string]string `json:"errors,omitempty"`
}

type CPU struct {
	UtilizationPercent float64 `json:"utilization_percent"`
	// IOWaitPercent is the fraction of aggregate CPU time spent waiting for I/O
	// in the sampling interval. Older agents omit it.
	IOWaitPercent *float64 `json:"iowait_percent,omitempty"`
}
type Memory struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}
type Load struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}
type Filesystem struct {
	MountPoint     string  `json:"mount_point"`
	TotalBytes     uint64  `json:"total_bytes"`
	FreeBytes      uint64  `json:"free_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}
type Disk struct {
	Device          string `json:"device"`
	ReadsCompleted  uint64 `json:"reads_completed"`
	WritesCompleted uint64 `json:"writes_completed"`
	ReadBytes       uint64 `json:"read_bytes"`
	WrittenBytes    uint64 `json:"written_bytes"`
}
