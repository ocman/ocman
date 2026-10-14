package app

import (
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDefaultPlatforms(t *testing.T) {
	if os.Getenv("OCMAN_TEST_PLATFORM_HELP") == "1" {
		os.Args = []string{"ocman", "-h"}
		flag.CommandLine = flag.NewFlagSet("ocman", flag.ExitOnError)
		run()
		return
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDefaultPlatforms$")
	cmd.Env = append(os.Environ(), "OCMAN_TEST_PLATFORM_HELP=1", "WAILS_BINDINGS=", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("help: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `(default "opencode,claudecode")`) {
		t.Fatalf("both platforms must be enabled by default:\n%s", out)
	}
}
