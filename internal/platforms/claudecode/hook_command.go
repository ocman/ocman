package claudecode

import (
	"flag"
	"io"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
)

// RunHookCommand handles `ocman hook [--addr A] <event>`, run by the ocman
// Claude plugin's hooks. It always succeeds: a hook must never fail or
// block Claude, whether or not ocman is running (FR-6).
func RunHookCommand(args []string, stdin io.Reader) bool {
	if len(args) == 0 || args[0] != "hook" {
		return false
	}
	flags := flag.NewFlagSet("hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	addr := flags.String("addr", "127.0.0.1:8227", "ocman's loopback MCP listener")
	if flags.Parse(args[1:]) == nil && flags.NArg() == 1 {
		claudeprovider.RunHook(*addr, flags.Arg(0), stdin, claudeprovider.HookTimeout)
	}
	return true
}
