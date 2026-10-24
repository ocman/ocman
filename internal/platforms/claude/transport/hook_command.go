package claudecode

import (
	"flag"
	"io"
	"os"

	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider"
)

// RunHookCommand handles `ocman hook [--addr A] [--journal DIR] <event>`, run
// by the ocman Claude plugin's hooks. It always succeeds: a hook must never
// fail or block Claude, whether or not ocman is running (FR-6).
// History events are journaled in every session; status is posted only from
// an ocman-managed process. Without either it returns without reading stdin.
func RunHookCommand(args []string, stdin io.Reader) bool {
	if len(args) == 0 || args[0] != "hook" {
		return false
	}
	flags := flag.NewFlagSet("hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	addr := flags.String("addr", "127.0.0.1:8227", "ocman's loopback MCP listener")
	journal := flags.String("journal", "", "ocman's Claude history journal directory")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 1 {
		return true
	}
	managed := os.Getenv("OCMAN_MANAGED") == "1"
	if !managed && *journal == "" {
		return true
	}
	claudeprovider.HandleHook(*addr, *journal, flags.Arg(0), managed, stdin, claudeprovider.HookTimeout)
	return true
}
