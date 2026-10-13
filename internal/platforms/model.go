package platforms

import (
	"fmt"
	"strings"
)

// Model references are "[<platform>:]<provider>/<model>". No prefix means
// OpenCode, so every reference stored before other platforms existed keeps
// its meaning. The prefix is a base platform id, never remote-qualified: the
// machine is chosen separately.

// SplitModel splits a model reference into its platform (empty when
// unprefixed) and the plain provider/model the platform understands. Only a
// colon before the first slash is a prefix; model ids may contain colons
// (openrouter/meta-llama/llama-3:free).
func SplitModel(ref string) (platform ID, model string) {
	slash := strings.IndexByte(ref, '/')
	if slash < 0 {
		return "", ref
	}
	if colon := strings.IndexByte(ref[:slash], ':'); colon > 0 {
		return ID(ref[:colon]), ref[colon+1:]
	}
	return "", ref
}

// QualifyModel prefixes a plain provider/model with its base platform;
// OpenCode references stay unprefixed.
func QualifyModel(platform ID, model string) string {
	if model == "" || platform == "" || platform == OpenCodeID {
		return model
	}
	return string(platform) + ":" + model
}

// ModelPlatform is the base platform a reference selects for a new session:
// its prefix, else OpenCode. An empty reference selects nothing.
func ModelPlatform(ref string) ID {
	if ref == "" {
		return ""
	}
	if platform, _ := SplitModel(ref); platform != "" {
		return platform
	}
	return OpenCodeID
}

// ModelFor returns the plain model to send to a session on base platform.
// An unprefixed reference is the session's own model; a prefix naming
// another platform is refused, since a session cannot change runtime.
func ModelFor(base ID, ref string) (string, error) {
	platform, model := SplitModel(ref)
	if platform != "" && platform != base {
		return "", fmt.Errorf("model %q belongs to platform %s, not %s", ref, platform, base)
	}
	return model, nil
}

// Base is the owner-local platform of an id: "r-<remote>:claudecode" is
// "claudecode" on its owner.
func (id ID) Base() ID {
	return id[strings.LastIndexByte(string(id), ':')+1:]
}

// PlatformForModel picks a new session's platform from an explicit request
// (possibly remote-qualified) and a model reference. An empty request takes
// the model's platform (unprefixed: OpenCode); with a request, an unprefixed
// model is that platform's own and only a prefix naming another platform is
// an error. Both empty stays empty, for the caller's default.
func PlatformForModel(requested, model string) (string, error) {
	if requested == "" {
		return string(ModelPlatform(model)), nil
	}
	if _, err := ModelFor(ID(requested).Base(), model); err != nil {
		return "", err
	}
	return requested, nil
}
