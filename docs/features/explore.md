---
title: Explore files
weight: 66
---

The folder icon left of the project label in a session's header opens
**Explore**: a searchable tree of the session's repository with the selected
file shown syntax-highlighted. It is rooted at the repository or worktree root
that contains the session's directory, and for a session on another machine
it reads that machine's files.

## What is shown

Explore lists what `git ls-files --cached --others --exclude-standard` lists:
every tracked file, plus untracked files that are not ignored. Ignore rules
only filter *untracked* files: an untracked `.env` or `node_modules/` never
appears, but a file that is already committed stays listed and readable even
if its name is later added to `.gitignore`. Ignore rules are not an access
control; `git rm --cached` a file to hide it.

A file opens only if it is one of those listed names and is a regular file
reached without following a symlink. A symlink, a FIFO, a `../` path, or a
path into `.git` is refused, even when the link's own name is listed.

## Limits

- A listing stops at 100,000 files or 8 MiB of path names; the header then
  shows the count with a `+`.
- Only the first 1 MiB of a file is shown.
- Binary files (a NUL byte in the first 8 KB) are not displayed.

## API

- `GET /api/git/files?dir=<abs>&remoteId=<id>` lists the files.
- `GET /api/git/file?dir=<abs>&path=<rel>&remoteId=<id>` reads one.

Both need an explicit `remoteId` (`local` for the hub) and return 503 when that
remote is disconnected, and 404 for a directory outside a repository or a
refused path. A git failure is a 502.
