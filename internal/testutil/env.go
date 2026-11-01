package testutil

import (
	"os"
	"strings"
)

// IsolatedEnv returns the current environment without operator ocman
// settings, followed by extra. Test subprocesses must start from it rather
// than os.Environ(): a developer shell or the ocman dashboard that launched
// the test run may export OCMAN_CONVERSATIONS_DB, OCMAN_STATE_DB or similar,
// and a child ocman would then open and migrate that live database.
// OCMAN_TEST_* control variables are kept; explicit settings belong in extra.
func IsolatedEnv(extra ...string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "OCMAN_") && !strings.HasPrefix(key, "OCMAN_TEST_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}
