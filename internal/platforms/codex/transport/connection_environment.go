package codextransport

import "strings"

// connectionEnvironment pins a named connection to its own stored account.
// Legacy machine connections deliberately retain their inherited environment.
// Do not mutate os.Environ or change HOME; both readers and execution receive
// this same filtering policy, including when a tmux server predates Ocman.
func connectionEnvironment(base []string, home string) []string {
	env := make([]string, 0, len(base)+1)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "CODEX_") || strings.HasPrefix(key, "OPENAI_") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "CODEX_HOME="+home)
}
