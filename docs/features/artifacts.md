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

## Reading from an agent

Use the `artifacts` MCP tool, usually exposed as `ocman_artifacts`, to find
published artifacts. Do not search their original folders or old worktrees.
Uploaded files are immutable copies, and their source paths may no longer exist.

1. Call `{"action":"list","directory":"/repo"}` with the stored absolute
   project root, not a worktree path. If you don't know it, list without
   `directory` and identify the project using the returned `directory` fields.
   Omit `platform` and `session_id` to include other sessions
   in the project. Pass a nonempty `next_cursor` as `cursor` for the next page.
2. Select an artifact by its title, description, and items, then call
   `{"action":"get","artifact_id":"art_1"}` for its metadata and file URLs.
3. Fetch a file's `items[].url` to read its contents. Relative URLs resolve
   against the origin of the top-level `url` returned by `get`. For example,
   with `url: "http://127.0.0.1:8228/artifacts/art_1"` and item
   `url: "/api/artifacts/art_1/files/0"`, fetch
   `http://127.0.0.1:8228/api/artifacts/art_1/files/0`.

The top-level `url` opens the browser page; it is not file content. The MCP
listener's port is not the file server. Items with `kind: "link"` point to
external URLs. Use each file's `name`, `mime`, and `size` to choose a reader,
downloading images or PDFs if the reader requires a local file. If fetching
fails or requires authentication, report that error instead of substituting
a file from the original source directory.

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
  disabled; **Run scripts** opts that one file into an interactive preview
  (see below).
- The **Artifacts** tab in the session sidebar shows artifacts from this
  session and its subagents, or from the whole project. New artifacts appear
  live through the `ocman.artifact.created` event.

Deleting an artifact from its page first revokes all of its live shares.

### HTML previews

Artifact HTML is treated as untrusted, even when an agent wrote it.

- **Static (default).** The file is served with
  `Content-Security-Policy: sandbox` and framed with an empty `sandbox`
  attribute: an opaque origin, no script. Markup and inline CSS render, so
  pre-rendered pages are readable; pages that build their DOM in JavaScript
  stay blank, and controls driven by script do nothing.
- **Interactive (opt-in, artifact page only).** **Run scripts** reloads the
  frame from `/api/artifacts/<id>/files/<n>/interactive`. Only that route
  sends `sandbox allow-scripts` plus a preview CSP (`default-src 'none'`,
  inline script and style, `data:`/`blob:` images, fonts and media,
  `form-action 'none'`, `base-uri 'none'`, `frame-ancestors 'self'`), and the
  frame grants the same single `allow-scripts` token. `allow-same-origin` is
  never granted, so the page runs on an opaque origin: it cannot read ocman's
  DOM, cookies or storage, and cannot fetch, open WebSockets, load external
  scripts, styles or images, open popups, submit forms, or navigate the ocman
  window. The opt-in is per file and remembered in this browser until
  **Stop scripts**. The
  route is a path segment, so a page that navigates itself with `?page=…`
  stays interactive.
- **Shared views and the session sidebar** stay static and say so; download
  the file to use it interactively. Downloads (`?download=1`) carry the
  original bytes; response headers, including the sandbox, are not saved
  with the file, so it runs normally when opened locally.

Residual risks of the interactive preview:

- The page can navigate its own frame anywhere, including an external URL
  carrying data it holds (its own content, or what the user typed into it).
  No shipping CSP directive prevents this. It cannot reach ocman data, so
  that data is limited to the artifact.
- It can use CPU or memory until the user stops it or leaves the page.
- Script-started downloads are blocked by the sandbox (no `allow-downloads`);
  the e2e suite checks this in Chromium and WebKit only.
- Self-contained pages only: external scripts, stylesheets, fonts, images and
  CDNs are blocked.

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
