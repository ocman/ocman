package platforms

import (
	"errors"
	"strings"
)

// ErrRemoteOffline is returned by hub-side adapters when the remote
// connection is not currently established.
var ErrRemoteOffline = errors.New("remote: not connected")

// PlatformIDSeparator joins a remote ID and a base platform id into the
// compound platform key, e.g. "r-abc123:opencode" (AD-2).
const PlatformIDSeparator = ":"

// remotePlatformPrefix is the prefix on a compound platform id.
const remotePlatformPrefix = "r-"

// CompoundPlatformID builds the compound platform key for a remote's
// base platform, e.g. CompoundPlatformID("abc", "opencode") ->
// "r-abc:opencode".
func CompoundPlatformID(remoteID, base string) string {
	return remotePlatformPrefix + remoteID + PlatformIDSeparator + base
}

// SplitPlatformID parses a (possibly compound) platform id into its
// remote ID and base platform. A bare id with no "r-...:" prefix means
// the local machine and returns ("", id) — local sessions keep their
// backward-compatible bare platform key (AD-2).
//
//	SplitPlatformID("r-abc:opencode") -> ("abc", "opencode")
//	SplitPlatformID("opencode")        -> ("",    "opencode")
func SplitPlatformID(id string) (remoteID, base string) {
	if !strings.HasPrefix(id, remotePlatformPrefix) {
		return "", id
	}
	rest := strings.TrimPrefix(id, remotePlatformPrefix)
	sep := strings.Index(rest, PlatformIDSeparator)
	if sep < 0 {
		// Malformed (prefix but no separator); treat the whole thing as base.
		return "", id
	}
	return rest[:sep], rest[sep+1:]
}
