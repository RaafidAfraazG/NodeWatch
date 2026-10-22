// Command demo submits an explicitly simulated NodeWatch snapshot for demos.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
)

func main() {
	endpoint := flag.String("url", "http://localhost:8080/api/v1/metrics", "collector ingestion URL")
	nodeID := flag.String("node-id", "demo-simulated-host", "simulated host ID")
	filesystemUsed := flag.Float64("filesystem-used-percent", 96, "simulated filesystem use percentage")
	flag.Parse()
	if *filesystemUsed < 0 || *filesystemUsed > 100 {
		fmt.Fprintln(os.Stderr, "filesystem-used-percent must be 0..100")
		os.Exit(2)
	}
	now := time.Now().UTC()
	snapshot := metrics.Snapshot{
		SchemaVersion: 1, NodeID: *nodeID, Hostname: "demo-simulated-host", OS: "linux", Architecture: "amd64", CollectedAt: now,
		CPU:         &metrics.CPU{UtilizationPercent: 12},
		Filesystems: []metrics.Filesystem{{MountPoint: "/demo", TotalBytes: 1000, FreeBytes: 40, AvailableBytes: 40, UsedPercent: *filesystemUsed}},
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		panic(err)
	}
	request, err := http.NewRequest(http.MethodPost, *endpoint, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer response.Body.Close()
	result, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	fmt.Printf("simulated snapshot response: %s\n", response.Status)
	fmt.Println(string(result))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		os.Exit(1)
	}
}
