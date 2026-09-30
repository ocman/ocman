---
title: Artifacts
weight: 6
---

Artifacts are the reports, screenshots, generated files, and links a session
produces that are worth keeping after the conversation scrolls away. Each
artifact belongs to a project and, optionally, to the session that made it.

## Publishing from an agent

Agents publish through the `artifacts` MCP tool (see [MCP](mcp.md#tools)) and
the installed `ocman-artifacts` skill. The tool has four actions: `help`,
`create`, `list`, and `get`. Agents cannot delete artifacts.

```json
{"action":"create","directory":"/repo","platform":"opencode","session_id":"ses_1",
 "title":"Coverage report",
 "files":[{"path":"/repo/coverage.html"},{"name":"notes.md","content":"# Notes"}],
 "links":[{"url":"https://example.com/pr/1","label":"PR"}]}
```

- `directory` must be an absolute path inside a project ocman knows about,
  including its worktrees. It is stored as the project root.
- Files are an absolute `path`, copied at publish time, or an inline `name` +
  `content`. The MIME type comes from `mime`, the file extension, or a content
  sniff, in that order.
- Links must be `http` or `https`.
- `create` returns the artifact `id`, its ocman `url`, per-item URLs, and a
  markdown snippet the agent can paste into its reply. The transcript renders
  the tool call as an artifact card.

Artifacts are immutable. Publish a new one instead of editing an old one.

## Storage

Artifact metadata lives in `state.db`. File bytes live next to it in
`artifacts/blobs/` (by default `~/.local/share/ocman/artifacts/blobs/`), named
by SHA-256, so identical content is stored once. The directory is created
with mode `0700`.

Each file may be at most **50 MB**. Larger files are rejected at publish
time.

## Browsing

- **Artifacts** in the main navigation (`/artifacts`) lists every artifact
  newest first, with a project filter, search, a count of stored bytes, and
  pagination. The command palette opens the same page.
- An artifact's page (`/artifacts/<id>`) previews its files, lists its links,
  links back to the originating session, and offers download, share, and
  delete. HTML files (`text/html`) render in a sandboxed frame with scripts
  disabled.
- The **Artifacts** tab in the session sidebar shows artifacts from this
  session and its subagents, or from the whole project. New artifacts appear
  live through the `ocman.artifact.created` event.

Deleting an artifact from its page first revokes all of its live shares.

## Sharing

**Share** on an artifact page publishes an encrypted copy to the share relay
(the same `ocman-relay` used for conversation shares). Anyone with the link can
view and download it; the key stays in the URL fragment, so the relay only
stores ciphertext. Before creating a link, the dialog lists every link URL that
will become visible and every file with its size.

- A share is a snapshot written once. It does not follow later changes,
  and artifacts do not change anyway.
- The relay caps the encrypted size of each share (32 MiB by default) and the
  number of chunks. Ocman checks the artifact against the limits the relay
  reports before uploading and refuses the share rather than leaving a partial
  one. So a single 50 MB file can be stored locally but not shared through a
  default relay.
- **Revoke** deletes the share from the relay immediately. The link stops
  working; the local artifact stays.
- Sharing needs a configured relay (`-relay-url`, `OCMAN_RELAY_URL`, or the
  built-in default). Share management is localhost-only because it hands out
  decryption keys.

## Limitations

Artifacts are local to the ocman instance that stored them. A hub does not see
artifacts published on a connected remote, and an agent publishing through the
hub's MCP server can only target projects on the hub's machine.
