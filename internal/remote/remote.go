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
// v9 requires canonical session references throughout core state and RPCs.
// v10 removes the cross-machine webhook-inbox RPCs; v11 moves unary JSON
// operations onto the generic Call RPC.
// Raw-adapter compatibility servers advertise v8; canonical managers never
// publish their adapters or hosts.
const ProtocolVersion int32 = 11

const legacySessionProtocol int32 = 8
