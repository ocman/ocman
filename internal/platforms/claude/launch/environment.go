// Package claudecli shares the Agent SDK launch environment across Claude callers.
package claudecli

import (
	"os"
	"strings"
)

// SDKVersion is the Python Agent SDK transport revision this integration mirrors.
const SDKVersion = "0.2.166"

// Environment preserves credentials and settings while selecting SDK mode.
// config nil inherits CLAUDE_CONFIG_DIR; an empty value explicitly removes it.
func Environment(config *string, directory string) []string {
	values := map[string]string{
		"CLAUDE_CODE_ENTRYPOINT": "sdk-py", "CLAUDE_AGENT_SDK_VERSION": SDKVersion,
		"CLAUDE_CODE_SDK_READS_SESSION_STATE": "1",
	}
	if directory != "" {
		values["PWD"] = directory
	}
	if config != nil {
		values["CLAUDE_CONFIG_DIR"] = *config
	}
	env := make([]string, 0, len(os.Environ())+len(values))
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, replaced := values[key]; !replaced && key != "CLAUDECODE" {
			env = append(env, kv)
		}
	}
	for key, value := range values {
		if key != "CLAUDE_CONFIG_DIR" || value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}
