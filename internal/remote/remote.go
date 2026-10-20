// Package remote implements the hub<->remote gRPC channel for
// multi-remote support. It contains the gRPC contract (proto/), the
// remote-side server that exposes a local platforms.Registry +
// hostsvc.Host over gRPC, and (in later phases) the hub-side client
// adapters that implement platforms.Platform / hostsvc.Host by calling
// a remote over gRPC.
//
// See spec/multi-remote-support/architecture.md.
package remote

// ProtocolVersion is the wire protocol version exchanged in Hello
// (AD-12). It is bumped on breaking wire or required semantic changes;
// additive db-type changes ride the JSON envelopes (AD-11) and do not bump it.
// v8 requires owner-allocated session identities and the identity batch RPC.
// Reject v7 before publishing adapters or launching a native session whose
// public identity the hub cannot retrieve.
const ProtocolVersion int32 = 8
