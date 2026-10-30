# Read-only source sessions in plugin workspaces

## Existing approaches and limitation

- `session` 1.2 workspace turns deny every tool. Source larger than the 64 KiB
  prompt must be split between unrelated sessions; one session cannot inspect a
  larger prepared corpus itself.
- Project sessions can use named permission profiles, but select a registered
  owner project. They cannot select prepared data in a plugin's private workspace.
  A user-managed profile also does not establish an immutable host policy.
- Conversation threads require a configured project and do not provide a
  workspace-scoped read-only file interface.

## Extension

Session 1.4 adds `readOnly` for workspace runs and the separate
`session.source` grant. The caller supplies a nonempty relative `subpath` under
the existing private workspace directory. It must prepare that directory first.
The host resolves containment and refuses links and special files before creation.

The host pins deny-all rules, then allows only file reads below the resolved
source directory. Commands, writes, network tools, MCP calls, delegation and
out-of-scope reads remain denied. Caller-selected permission profiles cannot
override this mode. An inventory file prepared by the plugin lets a session
discover and read a corpus without allowing unrestricted search tools.

Existing `noTools` sessions retain their current rules and do not gain source
access. Unsupported versions or missing grants fail before session creation.
Source sessions use the existing creation receipts and completion outbox.

This is a tool-permission policy, not an operating-system sandbox. Native plugins
remain trusted code and own the preparation and lifetime of their supplied files.

## Independent examples

- A documentation indexer prepares Markdown files plus a file inventory, starts
  one read-only session, and receives an outline covering several documents.
- A dependency inventory prepares manifests from several packages and receives
  one structured report without allowing installation or command execution.

Both use the same workspace/subpath interface and host-pinned file-read rules.
The host does not interpret their files or output schemas.
