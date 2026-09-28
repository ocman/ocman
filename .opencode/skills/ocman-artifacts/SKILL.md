---
name: ocman-artifacts
description: Use when you produce a report, screenshot, generated file, or relevant link the user should be able to open later from ocman.
---

# Ocman Artifacts

Use only the `artifacts` MCP tool. Start with `{"action":"help"}` for the
current schemas, validation rules, examples, and output shapes.

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
Use `{"action":"list","directory":"/repo"}` and
`{"action":"get","artifact_id":"..."}` to find earlier artifacts.
