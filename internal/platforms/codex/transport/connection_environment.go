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
		switch key {
		case "CODEX_HOME", "CODEX_API_KEY", "OPENAI_API_KEY", "CODEX_ACCESS_TOKEN",
			"OPENAI_IDENTITY_TOKEN_FILE", "OPENAI_FEDERATION_RULE_ID", "OPENAI_BASE_URL",
			"OPENAI_ORG_ID", "OPENAI_PROJECT_ID":
			continue
		}
		env = append(env, entry)
	}
	return append(env, "CODEX_HOME="+home)
}
