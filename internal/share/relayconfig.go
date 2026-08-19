package share

import (
	"fmt"
	"net/url"
	"strings"
)

// RelayURLEnv supplies the share relay's base URL. Set it (or
// -relay-url) to enable cross-machine conversation sharing; when unset,
// share links stay local to this machine.
const RelayURLEnv = "OCMAN_RELAY_URL"

// DefaultRelayURL is the relay baked into release builds. It is empty
// until a relay is deployed, so cross-machine sharing is off by default
// and existing installs are unchanged.
//
// Deliberately only a URL: no upload credential is baked in, because a
// secret shipped inside a distributed binary is public. The relay is
// protected by rate limits and size caps instead.
const DefaultRelayURL = ""

// Where a resolved relay URL came from, reported to the Settings page so
// an operator can tell a flag from an env var from the built-in default.
const (
	RelaySourceFlag    = "flag"
	RelaySourceEnv     = "env"
	RelaySourceBuiltin = "builtin"
)

// ResolveRelayURL picks the share relay endpoint: the flag wins, then the
// environment, then the value baked into the build. It returns the
// normalised URL and the name of the source that supplied it, or an empty
// URL and source when no relay is configured.
//
// An invalid value is an error rather than a silent fallback: a typo'd
// relay should stop startup, not quietly turn cross-machine sharing off
// and look like a broken feature later.
func ResolveRelayURL(flagValue, envValue, builtin string) (relayURL, source string, err error) {
	for _, candidate := range []struct {
		value  string
		source string
	}{
		{flagValue, RelaySourceFlag},
		{envValue, RelaySourceEnv},
		{builtin, RelaySourceBuiltin},
	} {
		trimmed := strings.TrimSpace(candidate.value)
		if trimmed == "" {
			continue
		}
		normalised, err := normaliseRelayURL(trimmed)
		if err != nil {
			return "", "", fmt.Errorf("invalid relay URL from %s: %w", candidate.source, err)
		}
		return normalised, candidate.source, nil
	}
	return "", "", nil
}

// normaliseRelayURL validates a relay base URL and strips a trailing
// slash so callers can concatenate paths onto it.
func normaliseRelayURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parsing %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%q must use http or https", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%q has no host", raw)
	}
	// The relay serves its API at the origin root, so a path would
	// silently produce unreachable share URLs.
	if path := strings.Trim(u.Path, "/"); path != "" {
		return "", fmt.Errorf("%q must be an origin with no path", raw)
	}
	u.Path = ""
	return strings.TrimRight(u.String(), "/"), nil
}
