---
name: ocman-artifacts
description: Use when finding or reading project artifacts, reports, screenshots, or files from earlier sessions, or publishing files and links to keep in ocman.
---

# Ocman Artifacts

Use the `artifacts` MCP tool, usually exposed as `ocman_artifacts`. Start with
`{"action":"help"}` for current schemas, validation rules, examples, and output shapes.

## Finding and reading artifacts

Discover published artifacts through MCP. Do not scan original folders,
old worktrees, or ocman's blob storage to find them. Uploaded files are
immutable copies; the original paths may have changed or disappeared.

1. List the project's artifacts with `{"action":"list","directory":"/repo"}`.
   Use the stored absolute project root, not a worktree path. If you don't
   know it, list without `directory` and use the returned artifacts' `directory`
   fields to identify the project. Omit `platform` and
   `session_id` to include artifacts from other sessions in the project.
   Pass a nonempty `next_cursor` as `cursor` to read the next page.
2. Read titles, descriptions, and `items` to select an artifact, then call
   `{"action":"get","artifact_id":"..."}`. This returns metadata, not file contents.
3. Read files by fetching `items[].url` with an HTTP fetch tool. A file URL
   such as `/api/artifacts/art_1/files/0` is relative: resolve it against the
   origin of the top-level `url` returned by `get`. For example, if `url` is
   `http://127.0.0.1:8228/artifacts/art_1`, fetch
   `http://127.0.0.1:8228/api/artifacts/art_1/files/0`.
   The top-level `url` is a browser page, not the file. Do not use the MCP
   listener's port as the file server. For `kind: "link"`, follow the item's
   external URL. Use `name`, `mime`, and `size` to choose a suitable reader;
   download images or PDFs for an image/PDF reader if needed.

If fetching fails or requires authentication, report that error rather than
guessing a source path or substituting a file from disk. Artifacts are local
to the connected ocman instance and are not routed across remotes.

## Publishing artifacts

Publish an artifact when work yields something worth keeping outside the
conversation: a report, a screenshot, a generated file, or a set of relevant
links (PRs, dashboards, CI runs). Do not publish scratch files, intermediate
output, or content that already fits in your reply.

Always pass `directory` (your absolute project or worktree directory) and your
own `platform` and `session_id`, so the artifact is attached to this project and
session:

`{"action":"create","directory":"/repo","platform":"opencode","session_id":"ses_1","title":"Coverage report","files":[{"path":"/repo/coverage.html"}]}`

Files are `{"path":"/abs/file"}` or inline `{"name":"notes.md","content":"...","mime":"text/markdown"}`.
Links are `{"url":"https://...","label":"..."}`.

For a file already committed and pushed, prefer a link to a forge permalink
pinned to the commit SHA (for example `.../blob/<sha>/path`) over uploading a
copy. Upload files that are generated, uncommitted, or not in the repository.

Artifacts are immutable and this tool cannot delete them. After `create`, share
the returned `url` or paste the returned `markdown` snippet in your reply.
