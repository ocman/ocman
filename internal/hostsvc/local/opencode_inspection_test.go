package local

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestInspectOpenCodeConnectionUsesOnlyTemporaryPrivateRuntime(t *testing.T) {
	for _, fail := range []bool{false, true} {
		runtime := &fakeRuntime{endpoint: "http://127.0.0.1:8123"}
		root := t.TempDir()
		inspected := false
		result, err := InspectOpenCodeConnection(t.Context(), root, runtime, func(_ context.Context, endpoint string) (platforms.ConnectionInspection, error) {
			inspected = true
			if endpoint != runtime.endpoint || filepath.Dir(runtime.spec().RepoRoot) != root || runtime.spec().RepoRoot == root {
				t.Fatal("inspection did not allocate a private invocation runtime")
			}
			if fail {
				return platforms.ConnectionInspection{}, errors.New("inspection failed")
			}
			return platforms.ConnectionInspection{Ready: true}, nil
		})
		if (err != nil) != fail || result.Ready == fail {
			t.Fatalf("inspection result=%+v err=%v", result, err)
		}
		if !inspected || runtime.launchCount() != 1 || runtime.stopCount() != 1 {
			t.Fatal("private inspection runtime not launched and stopped exactly once")
		}
	}
	if _, err := InspectOpenCodeConnection(t.Context(), t.TempDir(), nil, nil); err == nil {
		t.Fatal("inspector/runtime defaulted")
	}
}

func TestOpenCodeInspectionCancellationCleansOwnedRuntime(t *testing.T) {
	runtime := &fakeRuntime{endpoint: "http://127.0.0.1:8123"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := InspectOpenCodeConnection(ctx, t.TempDir(), runtime, func(ctx context.Context, _ string) (platforms.ConnectionInspection, error) {
		cancel()
		return platforms.ConnectionInspection{}, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || runtime.stopCount() != 1 || runtime.lastStopCtxErr() != nil {
		t.Fatalf("cancellation cleanup err=%v stops=%d stopContext=%v", err, runtime.stopCount(), runtime.lastStopCtxErr())
	}
}

func TestOpenCodeInspectionSerializesWritersAndCancelsWaiters(t *testing.T) {
	root := t.TempDir()
	first := &fakeRuntime{endpoint: "http://127.0.0.1:8123"}
	second := &fakeRuntime{endpoint: "http://127.0.0.1:8124"}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := InspectOpenCodeConnection(t.Context(), root, first, func(context.Context, string) (platforms.ConnectionInspection, error) {
			close(entered)
			<-release
			return platforms.ConnectionInspection{}, nil
		})
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := InspectOpenCodeConnection(ctx, root, second, func(context.Context, string) (platforms.ConnectionInspection, error) {
		t.Error("cancelled inspection ran")
		return platforms.ConnectionInspection{}, nil
	})
	close(release)
	if !errors.Is(err, context.Canceled) || second.launchCount() != 0 {
		t.Fatal("concurrent reservation writer launched")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	previous := first.spec().RepoRoot
	_, err = InspectOpenCodeConnection(t.Context(), root, second, func(context.Context, string) (platforms.ConnectionInspection, error) {
		return platforms.ConnectionInspection{}, nil
	})
	if err != nil || second.spec().RepoRoot == previous || second.spec().RepoRoot == root {
		t.Fatal("inspection invocation reused an existing runtime identity")
	}
}
