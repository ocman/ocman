package claudeprovider

import (
	"github.com/ocman/ocman/internal/providerprotocol"
	"strings"
)

// toolPermission maps a Claude tool to OpenCode's permission key and
// patterns, so preflight, rules, Allow-always inheritance and the judge agree.
func toolPermission(name string, input map[string]any) (string, []string) {
	// Exact patterns grant the literal argument, not its wildcard expansion.
	arg := func(key string) []string {
		if s, _ := input[key].(string); s != "" {
			return []string{providerprotocol.ExactPattern(s)}
		}
		return []string{"*"}
	}
	switch name {
	case "Bash":
		return "bash", arg("command")
	case "Edit", "Write", "MultiEdit":
		return "edit", arg("file_path")
	case "NotebookEdit":
		return "edit", arg("notebook_path")
	case "Read":
		return "read", arg("file_path")
	case "Glob":
		return "glob", arg("pattern")
	case "Grep":
		return "grep", arg("pattern")
	case "WebFetch":
		return "webfetch", arg("url")
	case "WebSearch":
		return "websearch", arg("query")
	case "Task", "Agent":
		return "task", arg("subagent_type")
	case "Skill":
		return "skill", arg("skill")
	case "TodoWrite":
		return "todowrite", []string{"*"}
	}
	return strings.ToLower(name), []string{"*"}
}
