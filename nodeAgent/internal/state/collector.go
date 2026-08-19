// Package state collects the local machine information the agent reports to
// the control plane and determines the agent's own status.
package state

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// AgentStatus is a strongly typed representation of the agent's own health.
// It is separate from the node's infrastructure state.
type AgentStatus string

// Agent lifecycle statuses.
const (
	StatusStarting AgentStatus = "STARTING"
	StatusReady    AgentStatus = "READY"
	StatusDegraded AgentStatus = "DEGRADED"
	StatusStopping AgentStatus = "STOPPING"
)

// NodeState is the observed state of the local machine reported to the
// control plane.
type NodeState struct {
	Status        AgentStatus `json:"status"`
	Hostname      string      `json:"hostname"`
	OS            string      `json:"os"`
	Architecture  string      `json:"architecture"`
	CPUCount      int         `json:"cpu_count"`
	MemoryBytes   uint64      `json:"memory_bytes"`
	UptimeSeconds int64       `json:"uptime_seconds"`
	AgentVersion  string      `json:"agent_version"`
	// IPAddress is filled in by the agent runtime, which knows the control
	// plane endpoint used to detect the local address.
	IPAddress string `json:"ip_address"`
}

// Collector gathers state from the local machine.
type Collector interface {
	// Collect returns the current observed node state.
	Collect(ctx context.Context) (NodeState, error)
}

// LocalCollector gathers state using Go runtime and Linux /proc interfaces.
// It never shells out to system commands. Values that cannot be read are
// reported as zero rather than failing the whole collection.
type LocalCollector struct {
	AgentVersion string
}

// NewLocalCollector returns a Collector for the local machine.
func NewLocalCollector(version string) *LocalCollector {
	return &LocalCollector{AgentVersion: version}
}

// Collect returns the current observed node state.
func (c *LocalCollector) Collect(_ context.Context) (NodeState, error) {
	hostname, _ := os.Hostname()
	return NodeState{
		Status:        StatusReady,
		Hostname:      hostname,
		OS:            runtime.GOOS,
		Architecture:  runtime.GOARCH,
		CPUCount:      runtime.NumCPU(),
		MemoryBytes:   ReadMemTotalBytes(),
		UptimeSeconds: ReadUptimeSeconds(),
		AgentVersion:  c.AgentVersion,
	}, nil
}

// ReadMemTotalBytes parses MemTotal from /proc/meminfo (Linux) and converts
// it to bytes. It returns 0 when the file is unavailable.
func ReadMemTotalBytes() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	return parseMemTotal(string(data))
}

// parseMemTotal extracts MemTotal (in kB) from the given /proc/meminfo
// content and returns it as bytes.
func parseMemTotal(content string) uint64 {
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kilobytes * 1024
	}
	return 0
}

// ReadUptimeSeconds parses the system uptime (first field) from /proc/uptime
// (Linux). It returns 0 when the file is unavailable.
func ReadUptimeSeconds() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	return parseUptime(string(data))
}

// parseUptime extracts the first field of /proc/uptime content.
func parseUptime(content string) int64 {
	fields := strings.Fields(content)
	if len(fields) < 1 {
		return 0
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return int64(seconds)
}

// ValidateStatus reports whether s is a known agent status.
func ValidateStatus(s AgentStatus) error {
	switch s {
	case StatusStarting, StatusReady, StatusDegraded, StatusStopping:
		return nil
	default:
		return &invalidStatusError{status: s}
	}
}

type invalidStatusError struct {
	status AgentStatus
}

func (e *invalidStatusError) Error() string {
	return "unknown agent status " + string(e.status)
}
