package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

func TestParseResourceProcesses(t *testing.T) {
	rows, err := parseResourceProcesses(" 10 1 2048 01:02.50 /usr/bin/my process\n11 10 0 2-03:04:05 helper\n")
	if err != nil || len(rows) != 2 || rows[10].Name != "my process" || rows[10].MemoryBytes != 2097152 || rows[10].cpuSeconds != 62.5 || rows[11].cpuSeconds != 183845 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	for _, input := range []string{"", "1 2", "bad 1 0 00:00 x", "1 bad 0 00:00 x", "1 0 -1 00:00 x", "1 0 1 bad x", "0 0 0 00:00 x", "1 -1 0 00:00 x"} {
		if _, err := parseResourceProcesses(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestParseResourceCPUTime(t *testing.T) {
	for _, input := range []string{"-1:00", "NaN:00", "Inf:00", "1:2:3:4", "x-01:00", "00:x", "1000000000000:00"} {
		if _, err := parseCPUTime(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestResourceSamplerTreesAndDeltas(t *testing.T) {
	s := &resourceSampler{}
	start := time.Unix(100, 0)
	rows, _ := parseResourceProcesses("10 1 100 00:01 ocman\n11 10 50 00:02 child\n12 11 25 00:00 grandchild\n20 1 10 00:00 detached\n21 20 5 00:00 agent\n99 1 999 00:00 unrelated")
	roots := map[int]bool{10: true, 11: true, 20: true, 404: true}
	first := s.update(start, rows, roots)
	if len(first.Processes) != 5 || first.MemoryBytes != 190*1024 || first.CPUPercent != 0 {
		t.Fatalf("first=%+v", first)
	}
	next, _ := parseResourceProcesses("10 1 100 00:02 ocman\n11 10 50 00:04 child\n12 11 25 00:00 grandchild\n20 1 10 00:00 detached\n21 20 5 00:00 agent")
	second := s.update(start.Add(time.Second), next, roots)
	if second.CPUPercent != 300 || len(second.History) != 2 || first.History[0].CPUPercent != 0 {
		t.Fatalf("second=%+v", second)
	}
	// Reused PIDs, counter resets and cycles must not produce negative CPU or hang.
	reused, _ := parseResourceProcesses("10 11 1 00:00 replacement\n11 10 1 00:01 child")
	third := s.update(start.Add(2*time.Second), reused, roots)
	if third.CPUPercent != 0 || len(third.Processes) != 2 {
		t.Fatalf("third=%+v", third)
	}
	for i := 3; i < 70; i++ {
		s.update(start.Add(time.Duration(i)*time.Second), reused, roots)
	}
	if len(s.snapshot.History) != 60 || len(first.History) != 1 {
		t.Fatal("history not bounded or mutated earlier snapshot")
	}
}

func TestHostResources(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	write("tmux", "echo '200 201'")
	write("ps", fmt.Sprintf("echo '%d 1 100 00:01 ocman\n200 1 20 00:00 tmux\n201 200 10 00:00 supervisor\n300 1 30 00:00 service\n301 300 5 00:00 agent'", os.Getpid()))
	h := &Host{deps: Deps{OpenCodeService: func() (ocv2.Service, error) { return ocv2.Service{PID: 300}, nil }}}
	result, err := h.Resources(context.Background())
	if err != nil || len(result.Processes) != 5 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	write("ps", "exit 1")
	if cached, err := h.Resources(context.Background()); err != nil || cached.Timestamp != result.Timestamp {
		t.Fatalf("cache=%+v err=%v", cached, err)
	}
	h.resources.at = time.Time{}
	if _, err := h.Resources(context.Background()); err == nil {
		t.Fatal("expected ps failure")
	}
	write("ps", "echo invalid")
	if _, err := h.Resources(context.Background()); err == nil {
		t.Fatal("expected parse failure")
	}
	write("tmux", "exit 1")
	write("ps", fmt.Sprintf("echo '%d 1 100 00:01 ocman'", os.Getpid()))
	h.deps.OpenCodeService = func() (ocv2.Service, error) { return ocv2.Service{}, fmt.Errorf("no service") }
	if result, err := h.Resources(context.Background()); err != nil || len(result.Processes) != 1 {
		t.Fatalf("missing optional runtimes: %+v %v", result, err)
	}
}
