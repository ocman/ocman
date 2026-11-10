package claudecli

import "strings"

// AccountEnvironment uses an explicitly selected account directory, never the
// owner's inherited authentication. The caller must validate and canonicalize
// config before opening account state. Native settings can still supply auth;
// setup must inspect the effective account before advertising isolation.
func AccountEnvironment(config, directory string) []string {
	base := Environment(&config, directory)
	env := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		// Drop the whole Anthropic namespace, including inherited model,
		// gateway, profile and billing overrides. Account-local native settings
		// remain authoritative. Cloud credentials stay available to user tools,
		// but cannot select a model service through inherited Claude flags.
		if strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "CLAUDE_CODE_USE_") ||
			strings.HasPrefix(key, "CLAUDE_CODE_OAUTH_") || key == "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR" {
			continue
		}
		env = append(env, entry)
	}
	return env
}
