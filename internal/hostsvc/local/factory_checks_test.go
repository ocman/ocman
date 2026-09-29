package local

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func factoryCheckRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"}, {"checkout", "-q", "-b", "factory/e1"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

func TestRunFactoryChecksReportsEveryCommand(t *testing.T) {
	repo := factoryCheckRepo(t)
	h := &Host{}
	passed, report, err := h.RunFactoryChecks(t.Context(), repo, "factory/e1", "main", []string{"echo hello; echo CI=$CI", "echo broken >&2; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	if passed {
		t.Fatal("a failing command reported a pass")
	}
	for _, want := range []string{"$ echo hello", "exit 0", "CI=1", "exit 3", "broken", "Test-integrity scan: no deleted tests"} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	passed, _, err = h.RunFactoryChecks(t.Context(), repo, "factory/e1", "main", []string{"true"})
	if err != nil || !passed {
		t.Fatalf("passing checks = %v, %v", passed, err)
	}
	if _, _, err := h.RunFactoryChecks(t.Context(), repo, "factory/missing", "main", []string{"true"}); err == nil {
		t.Fatal("missing worktree was accepted")
	}
	_, report, _ = h.RunFactoryChecks(t.Context(), repo, "factory/e1", "no-such-target", []string{"true"})
	if !strings.Contains(report, "Test-integrity scan unavailable") {
		t.Fatalf("unknown target report:\n%s", report)
	}
}

func TestRunFactoryCheckTruncatesAndTimesOut(t *testing.T) {
	repo := factoryCheckRepo(t)
	_, report, err := (&Host{}).RunFactoryChecks(t.Context(), repo, "factory/e1", "main", []string{"head -c 9000 /dev/zero | tr '\\0' x"})
	if err != nil || !strings.Contains(report, "…") || strings.Count(report, "x") > factoryCheckOutputTail+10 {
		t.Fatalf("long output was not truncated (%d bytes), %v", len(report), err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	out, code := runFactoryCheck(ctx, repo, "sleep 30 & wait")
	if code != -1 || !strings.Contains(out, "timed out") || time.Since(start) > 10*time.Second {
		t.Fatalf("timeout = %d %q after %s", code, out, time.Since(start))
	}
	if out, code := runFactoryCheck(t.Context(), "/definitely/missing/dir", "true"); code != -1 || out == "" {
		t.Fatalf("start failure = %d %q", code, out)
	}
}

func TestRunFactoryChecksPinsACleanHead(t *testing.T) {
	repo := factoryCheckRepo(t)
	h := &Host{}
	passed, report, err := h.RunFactoryChecks(t.Context(), repo, "factory/e1", "main", []string{"touch leftover.txt"})
	if err != nil || passed || !strings.Contains(report, "changed while the checks ran") {
		t.Fatalf("leftover file = %v %v:\n%s", passed, err, report)
	}
	passed, report, err = h.RunFactoryChecks(t.Context(), repo, "factory/e1", "main", []string{"true"})
	if err != nil || passed || !strings.Contains(report, "uncommitted changes before the checks ran") {
		t.Fatalf("dirty before = %v %v:\n%s", passed, err, report)
	}
}

func TestFactoryCheckEnvWithholdsOcmanSecrets(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "leak-gh")
	t.Setenv("OCMAN_AUTH_PASSWORD", "leak-pw")
	t.Setenv("FACTORY_CHECK_KEEP", "kept")
	repo := factoryCheckRepo(t)
	_, report, err := (&Host{}).RunFactoryChecks(t.Context(), repo, "factory/e1", "main", []string{"env | grep -E 'leak|FACTORY_CHECK_KEEP'"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report, "=leak-") || !strings.Contains(report, "FACTORY_CHECK_KEEP=kept") {
		t.Fatalf("environment leaked or dropped too much:\n%s", report)
	}
}
