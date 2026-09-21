# Go plugin SDK

Import `github.com/NoUseFreak/ocman/sdk/plugin`. The SDK uses aliases to the host's
canonical DTOs, codecs and stream validator, so there is one wire definition.
It adds no dependencies. Go 1.26.1 or newer is required by the ocman module.

The SDK is optional. Any language can read and write NDJSON according to the
[protocol contract](../../internal/plugins/README.md). `action.v1` is unary;
streaming is available to separately negotiated capabilities.

## Executable lifecycle

Build the working example from the repository root:

```sh
go build -o /tmp/ocman-plugin-fixture ./examples/ocman-plugin-fixture
go test ./sdk/plugin/...
```

An executable accepts exactly one argument, `describe` or `serve`, and reads
`OCMAN_PLUGIN_TOKEN`. Pass these to `plugin.Run` with the description, stdin,
stdout and handler. See [the example](../../examples/ocman-plugin-fixture/main.go).
Use stderr for diagnostics. Never print logs or launch tokens to stdout.

`Run` emits the token-bound description in either mode. Describe exits immediately.
Serve validates the host's negotiated versions before dispatching calls. It owns
and closes the supplied pipes, including on cancellation. Pipe `Close` methods
must unblock pending I/O. The helper does not supervise or restart processes.

Handlers receive the operation ID and absolute deadline through `Call`, plus a
context cancelled on deadline, cancel frame, shutdown or connection termination.
Handlers must honor that context. They may execute concurrently up to the declared
limit. Use `emit` sequentially and only before returning. The SDK assigns chunk
sequences and emits one terminal result. Shutdown abandons calls without further
output. The host still enforces its deadline and process-kill grace period if a
handler ignores cancellation.

Return `plugin.Failure(plugin.ErrorConflict)` or another documented category for
a normalized error. Context errors map to `cancelled` or `deadline_exceeded`.
Other errors become `internal`; diagnostic text never enters a result.

`ActionHandler` dispatches declared actions, rejects undeclared context fields,
and validates the closed action-result union. It accepts additive invocation
fields, but action results intentionally reject unknown fields. The host owns
approval, current grants, confirmation and deduplication. Requested grants are
declarations, never approval; plugins receive only the minimized context.

`RunWithEvents` is `Run` plus a plugin-initiated event source, for capabilities
whose inbound direction is not a response to a host call. Events for a capability
the host did not negotiate are dropped rather than failing the process; a closed
channel is not an error. `ConversationHandler` serves `conversation.v1` reply
calls and `NewConversationMessage` builds a validated inbound message event. See
[the Slack plugin](../../examples/ocman-plugin-slack/main.go) for both directions.

## Conformance tests for external plugins

Import `github.com/NoUseFreak/ocman/sdk/plugin/conformance` from a Go test package.
The executable being tested can be written in any language. Supply deterministic,
side-effect-free calls through `conformance.Cases`:

- `Success`: an action.v1 call returning a valid unary result.
- `Wait`: a call that waits for its deadline or cancellation.
- `Error` and `ErrorCategory`: a call returning the specified normalized error.
- `Stream`: an optional non-action call producing chunks followed by a result.

```go
func TestPlugin(t *testing.T) {
    conformance.Run(t, "/absolute/path/to/ocman-plugin-example", cases)
    conformance.RunActionGrants(t, "/absolute/path/to/ocman-plugin-example",
        plugin.ActionInvocation{
            ActionID: "session",
            Context: plugin.ActionContext{SessionID: "ses-1", OwnerID: "local"},
        })
}
```

`Run` verifies describe/serve identity and launch binding, negotiation and rejected
offers, additive fields, framing and stream order, expired and elapsed deadlines,
cancellation, normalized errors and shutdown with work in flight. Each subprocess
has a five-second test timeout and a minimal environment.

`RunActionGrants` uses the real host broker to verify denied admission, granted
dispatch, context minimization, deduplication and revoked cached results. Select
an action with nonempty required grants and valid context for its placement.
Include extra context fields to exercise minimization. Confirmation-required
actions are supported. No grant approval is sent over the wire.

`RunConversationGrants` covers `conversation.v1`: the declaration contract, a
denied and then granted reply dispatch through the real host broker, and denial
of an inbound message naming a project other than the configured one. Pass the
project directory the test configuration approves.

The fixture's additional `fixture.v1` capability offers `stream`, `wait` and
`error` solely for testing. Production plugins do not need that capability.
The integration test also discovers the built fixture and launches it through
the real host process supervisor. Neither the SDK nor the fixture implements
service integrations, installation, registries, updates or reattachment.
