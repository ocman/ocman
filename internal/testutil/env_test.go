package testutil

import (
	"slices"
	"strings"
	"testing"
)

func TestIsolatedEnvDropsOperatorSettings(t *testing.T) {
	t.Setenv("OCMAN_CONVERSATIONS_DB", "/live/state_conv.db")
	t.Setenv("OCMAN_STATE_DB", "/live/state.db")
	t.Setenv("OCMAN_TEST_MARKER", "kept")
	t.Setenv("PATH_FOR_TEST_ENV", "kept")

	env := IsolatedEnv("OCMAN_TEST_EXTRA=1", "OCMAN_AUTH_PASSWORD=explicit")

	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if key == "OCMAN_CONVERSATIONS_DB" || key == "OCMAN_STATE_DB" {
			t.Fatalf("operator setting %s leaked into the child environment", kv)
		}
	}
	for _, want := range []string{"OCMAN_TEST_MARKER=kept", "PATH_FOR_TEST_ENV=kept", "OCMAN_TEST_EXTRA=1", "OCMAN_AUTH_PASSWORD=explicit"} {
		if !slices.Contains(env, want) {
			t.Fatalf("env missing %q", want)
		}
	}
}
