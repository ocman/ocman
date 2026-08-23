package gitexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCommandCapsConcurrentGitProcesses(t *testing.T) {
	const limit = 8
	dir := t.TempDir()
	markers := filepath.Join(dir, "markers")
	if err := os.Mkdir(markers, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch \"$GITEXEC_MARKERS/$$\"\nwhile [ ! -f \"$GITEXEC_RELEASE\" ]; do sleep 0.01; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(dir, "release")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GITEXEC_MARKERS", markers)
	t.Setenv("GITEXEC_RELEASE", release)

	var wg sync.WaitGroup
	for i := range limit + 1 {
		wg.Add(1)
		go func(method int) {
			defer wg.Done()
			cmd := Command(context.Background(), "status")
			var err error
			switch method % 3 {
			case 0:
				err = cmd.Run()
			case 1:
				_, err = cmd.Output()
			case 2:
				_, err = cmd.CombinedOutput()
			}
			if err != nil {
				t.Errorf("git: %v", err)
			}
		}(i)
	}

	deadline := time.Now().Add(10 * time.Second)
	started := 0
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(markers)
		if err != nil {
			t.Fatal(err)
		}
		started = len(entries)
		if started >= limit {
			time.Sleep(100 * time.Millisecond)
			entries, err = os.ReadDir(markers)
			if err != nil {
				t.Fatal(err)
			}
			started = len(entries)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	if started > limit {
		t.Fatalf("started %d concurrent git processes, want at most %d", started, limit)
	}
	if started < limit {
		t.Fatalf("started only %d git processes before timeout, test did not saturate limit %d", started, limit)
	}
}

func TestWithSlotHonorsPreCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	_, err := withSlot(ctx, func() (struct{}, error) {
		ran = true
		return struct{}{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if ran {
		t.Fatal("command ran despite cancellation")
	}
}

func TestWithSlotHonorsCancellationWhileWaiting(t *testing.T) {
	for range cap(processSlots) {
		processSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(processSlots) {
			<-processSlots
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ran := false
	_, err := withSlot(ctx, func() (struct{}, error) {
		ran = true
		return struct{}{}, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if ran {
		t.Fatal("command ran despite cancellation while waiting for a slot")
	}
}

func TestWithSlotReleasesAfterPanic(t *testing.T) {
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		_, _ = withSlot(context.Background(), func() (struct{}, error) {
			panic("boom")
		})
	}()
	if len(processSlots) != 0 {
		t.Fatalf("panic leaked a process slot: %d still occupied", len(processSlots))
	}
}
