package claudecode

import (
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/claudecli"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSDKUpgradeReplacesLegacySupervisor(t *testing.T) {
	e := newRecoveryEnv(t)
	legacy, stop := e.start(t, "")
	runtime := legacy.projects.mgr.Runtime
	identity := runtime.Identity
	runtime.Identity = func() string { return strings.TrimSuffix(identity(), "\x00sdk-py/"+claudecli.SDKVersion) }
	if _, err := legacy.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: e.repo}); err != nil {
		t.Fatal(err)
	}
	oldEndpoint := e.endpoint(t)
	stop()
	upgraded, stop := e.start(t, "")
	defer stop()
	eventually(t, "SDK upgrade replaces the legacy supervisor", func() bool {
		if _, err := upgraded.EnsureRuntime(t.Context(), e.repo); errors.Is(err, platforms.ErrBusy) {
			return false // restart recovery drains asynchronously
		} else if err != nil {
			t.Fatal(err)
		}
		endpoint := e.endpoint(t)
		return endpoint != "" && endpoint != oldEndpoint
	})
}
