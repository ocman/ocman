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
		fixed := key == "CLAUDE_CONFIG_DIR" || key == "CLAUDE_CODE_ENTRYPOINT" || key == "CLAUDE_AGENT_SDK_VERSION" || key == "CLAUDE_CODE_SDK_READS_SESSION_STATE"
		if strings.HasPrefix(key, "ANTHROPIC_") || (strings.HasPrefix(key, "CLAUDE_") && !fixed) {
			continue
		}
		env = append(env, entry)
	}
	return env
}
