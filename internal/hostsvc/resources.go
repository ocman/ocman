package hostsvc

import "context"

// ResourceViewer is optional for hosts that can inspect their managed processes.
type ResourceViewer interface {
	Resources(context.Context) (ResourceSnapshot, error)
}

type ResourceProcess struct {
	PID         int     `json:"pid"`
	ParentPID   int     `json:"parentPid"`
	Name        string  `json:"name"`
	Command     string  `json:"command,omitempty"`
	SessionID   string  `json:"sessionId,omitempty"`
	CPUPercent  float64 `json:"cpuPercent"`
	MemoryBytes uint64  `json:"memoryBytes"`
}

type ResourcePoint struct {
	Timestamp   int64             `json:"timestamp"`
	CPUPercent  float64           `json:"cpuPercent"`
	MemoryBytes uint64            `json:"memoryBytes"`
	Processes   []ResourceProcess `json:"processes,omitempty"`
}

type ResourceSnapshot struct {
	ResourcePoint
	Processes []ResourceProcess `json:"processes"`
	History   []ResourcePoint   `json:"history"`
}
