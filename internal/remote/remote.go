// Package remote implements the hub<->remote gRPC channel for
// multi-remote support. It contains the gRPC contract (proto/), the
// remote-side server that exposes a local platforms.Registry +
// hostsvc.Host over gRPC, and (in later phases) the hub-side client
// adapters that implement platforms.Platform / hostsvc.Host by calling
// a remote over gRPC.
//
// See spec/multi-remote-support/architecture.md.
package remote

import (
	"errors"
	"strings"
)

// ErrRemoteOffline is returned by hub-side adapters when the remote
// connection is not currently established.
var ErrRemoteOffline = errors.New("remote: not connected")

// ProtocolVersion is the wire protocol version exchanged in Hello
// (AD-12). It is bumped on breaking wire or required semantic changes;
// additive db-type changes ride the JSON envelopes (AD-11) and do not bump it.
const ProtocolVersion int32 = 5

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
