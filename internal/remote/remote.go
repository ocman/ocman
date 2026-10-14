// Package remote implements the hub<->remote gRPC channel for
// multi-remote support. It contains the gRPC contract (proto/), the
// remote-side server that exposes a local platforms.Registry +
// hostsvc.Host over gRPC, and (in later phases) the hub-side client
// adapters that implement platforms.Platform / hostsvc.Host by calling
// a remote over gRPC.
//
// See spec/multi-remote-support/architecture.md.
package remote

import "github.com/NoUseFreak/ocman/internal/platforms"

// ErrRemoteOffline aliases platforms.ErrRemoteOffline so errors.Is matches
// across both names. Callers outside this package use platforms directly.
var ErrRemoteOffline = platforms.ErrRemoteOffline

// ProtocolVersion is the wire protocol version exchanged in Hello
// (AD-12). It is bumped on breaking wire or required semantic changes;
// additive db-type changes ride the JSON envelopes (AD-11) and do not bump it.
const ProtocolVersion int32 = 7

// Compound platform-id helpers live in internal/platforms so modules can
// use them without importing the gRPC client. These thin aliases keep this
// package's own call sites unchanged.
const PlatformIDSeparator = platforms.PlatformIDSeparator

var (
	CompoundPlatformID = platforms.CompoundPlatformID
	SplitPlatformID    = platforms.SplitPlatformID
)
