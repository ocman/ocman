# ACP execution transport

`acpexec` is the shared ACP v1 execution client. Codex uses it by default inside
its existing supervisor. It is an execution transport, not another platform ID.
OpenCode and Claude execution paths are unchanged.

## Commands

`DefaultCommand` selects an already-installed executable:

| Platform ID | Executable | Arguments |
| --- | --- | --- |
| `opencode` | `opencode` | `acp` |
| `codex` | `codex-acp` | none |
| `claudecode` | `claude-agent-acp` | none |

Codex and Claude adapters are separate ecosystem packages,
`@agentclientprotocol/codex-acp` and
`@agentclientprotocol/claude-agent-acp`. This package installs nothing and never
runs an on-demand package manager. Callers can override the command with an
explicit path and arguments. Credentials and the effective native configuration
must be supplied on the owning machine through the child environment.

## Contract

- `Start` launches one process, negotiates ACP v1 and exposes its handshake through
  `Info`. A failed handshake closes the pipes and reaps the child. The start
  context controls only initialization; the supervisor must call `Close`.
- `NewSession`, `LoadSession` and `ResumeSession` preserve native session IDs and
  require absolute working directories. Load and resume are capability-gated.
  Load replays history; resume does not. There is no implicit substitution.
- `Prompt` streams typed SDK notifications into `Handlers.Update`. Notifications
  are ordered, and preceding callbacks finish before a successful prompt result.
  A failed update handler disconnects the transport and fails the prompt, so a
  persistence failure cannot appear to be a recorded successful turn.
- Permission callbacks receive the native request and offered options. A missing
  handler or cancelled request cancels permission. Invalid or unoffered choices
  are rejected. Handlers must honor cancellation and be concurrency-safe.
- `Cancel` requests interruption; a prompt's terminal response confirms settlement.
  Timeout, transport failure and cancellation never authorize automatic replay or
  native fallback. The client does not release session ownership on these errors.
- File, terminal and terminal-auth capabilities are not advertised. Form
  elicitation is advertised only when its optional handler is provided. The
  wire dispatcher preserves the session/tool scope omitted by the SDK's
  generated elicitation union, so concurrent session questions stay isolated.
  File and terminal callbacks reject requests rather than executing host actions.
  The agent remains responsible for its own tools. `Authenticate` accepts only
  advertised agent-handled auth methods.
- `Close` is idempotent, closes the transport and waits up to two seconds before
  killing and reaping an uncooperative adapter process. It is not a replacement
  for platform-specific descendant cleanup and supervisor lifecycle management.

The maintained [Go ACP SDK](https://github.com/coder/acp-go-sdk) owns the JSON-RPC
wire types and framing. A small dispatcher retains scoped form fields alongside
the generated SDK payload. Tests use a disposable test executable as the agent,
never an installed agent, credentials or writable platform databases.

## Platform integration requirements

Codex implements these requirements in `codex/provider/acp_*.go`. Other platforms
must meet them before enabling this transport:

1. Launch through the owning Host/runtime and retain one execution owner per
   session. Keep native platform IDs and the owner/source-scoped identity mapping.
2. Bridge notifications and permission callbacks to the existing supervisor spool
   and prompt lifecycle. Reconcile ACP live identities with the single native
   history converter before publishing messages.
3. Retain native history readers, accounting and unsupported controls. Keep
   browsing independent of execution process startup.
4. Add explicit opt-in configuration, adapter availability checks and a tested
   capability matrix. Pin supported adapter versions rather than auto-updating.
5. Test restart, disconnect, pending permissions, native-history reconciliation
   and background tasks before replacing any native execution operation.
