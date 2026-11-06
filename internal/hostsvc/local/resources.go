package local

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/term"
	"github.com/ocman/ocman/internal/tmux"
)

type processReading struct {
	hostsvc.ResourceProcess
	cpuSeconds float64
}

type resourceSampler struct {
	mu       sync.Mutex
	at       time.Time
	previous map[int]processReading
	snapshot hostsvc.ResourceSnapshot
}

// Resources includes ocman's own tree, detached managed tmux trees and the
// registered machine service. Only executable names are exposed, never argv.
func (h *Host) Resources(ctx context.Context) (hostsvc.ResourceSnapshot, error) {
	s := &h.resources
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.at) < 750*time.Millisecond {
		return s.snapshot, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	roots := map[int]bool{os.Getpid(): true}
	for _, pid := range term.ResourcePIDs(ctx) {
		roots[pid] = true
	}
	if out, err := tmux.Output(ctx, "-L", tmux.ManagedServer, "list-panes", "-a", "-F", "#{pid} #{pane_pid}"); err == nil {
		for _, field := range strings.Fields(string(out)) {
			if pid, err := strconv.Atoi(field); err == nil && pid > 0 {
				roots[pid] = true
			}
		}
	}
	if h.deps.OpenCodeService != nil {
		if service, err := h.deps.OpenCodeService(); err == nil && service.PID > 0 {
			roots[service.PID] = true
		}
	}
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,rss=,time=,comm=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return hostsvc.ResourceSnapshot{}, fmt.Errorf("read process resources: %w", err)
	}
	readings, err := parseResourceProcesses(string(out))
	if err != nil {
		return hostsvc.ResourceSnapshot{}, err
	}
	return s.update(time.Now(), readings, roots), nil
}

func parseResourceProcesses(output string) (map[int]processReading, error) {
	result := make(map[int]processReading)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 5 {
			return nil, fmt.Errorf("invalid process resource row")
		}
		pid, e1 := strconv.Atoi(fields[0])
		parent, e2 := strconv.Atoi(fields[1])
		rss, e3 := strconv.ParseUint(fields[2], 10, 64)
		cpu, e4 := parseCPUTime(fields[3])
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || pid <= 0 || parent < 0 {
			return nil, fmt.Errorf("invalid process resource values")
		}
		result[pid] = processReading{ResourceProcess: hostsvc.ResourceProcess{
			PID: pid, ParentPID: parent, MemoryBytes: rss * 1024,
			Name: filepath.Base(strings.Join(fields[4:], " ")),
		}, cpuSeconds: cpu}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("empty process resource inventory")
	}
	return result, nil
}

// ps uses [days-][hours:]minutes:seconds, with fractional seconds on macOS.
func parseCPUTime(raw string) (float64, error) {
	days := 0.0
	if day, rest, ok := strings.Cut(raw, "-"); ok {
		value, err := strconv.ParseUint(day, 10, 32)
		if err != nil {
			return 0, err
		}
		days, raw = float64(value)*86400, rest
	}
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid CPU time")
	}
	seconds := 0.0
	for _, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= 1e12 {
			return 0, fmt.Errorf("invalid CPU time component")
		}
		seconds = seconds*60 + value
	}
	return days + seconds, nil
}

func (s *resourceSampler) update(now time.Time, readings map[int]processReading, roots map[int]bool) hostsvc.ResourceSnapshot {
	children := make(map[int][]int)
	for pid, reading := range readings {
		children[reading.ParentPID] = append(children[reading.ParentPID], pid)
	}
	selected := make(map[int]bool)
	var visit func(int)
	visit = func(pid int) {
		if selected[pid] {
			return
		}
		selected[pid] = true
		for _, child := range children[pid] {
			visit(child)
		}
	}
	for root := range roots {
		visit(root)
	}
	point := hostsvc.ResourcePoint{Timestamp: now.UnixMilli()}
	processes := []hostsvc.ResourceProcess{}
	for pid := range selected {
		reading, ok := readings[pid]
		if !ok {
			continue
		}
		if previous, ok := s.previous[pid]; ok && previous.Name == reading.Name && previous.ParentPID == reading.ParentPID && now.After(s.at) {
			reading.CPUPercent = max(0, (reading.cpuSeconds-previous.cpuSeconds)/now.Sub(s.at).Seconds()*100)
		}
		point.CPUPercent += reading.CPUPercent
		point.MemoryBytes += reading.MemoryBytes
		processes = append(processes, reading.ResourceProcess)
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].PID < processes[j].PID })
	history := append(append([]hostsvc.ResourcePoint{}, s.snapshot.History...), point)
	if len(history) > 60 {
		history = history[len(history)-60:]
	}
	s.at, s.previous = now, readings
	s.snapshot = hostsvc.ResourceSnapshot{ResourcePoint: point, Processes: processes, History: history}
	return s.snapshot
}
