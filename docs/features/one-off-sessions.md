---
title: One-off sessions
weight: 3
---

Choose **one-off** in the new-session project picker, or press **Option+O**
on macOS and **Alt+O** elsewhere. Ocman creates a private temporary folder
and opens the composer there. You do not need to register a project.
The virtual project is titled **one-off**, without the temporary folder's
random name. Selecting it again creates a fresh folder, not a shared workspace.

Write your prompt and submit it as usual. Opening the composer does not start
an agent. Draft text survives reloads, and the session uses the same model,
permission and delivery controls as other conversations.

The action uses this machine by default. If the current route explicitly
selects a remote machine, the folder is created there. A disconnected remote
returns an error rather than creating a local folder instead.

Archiving a session does not delete its files, but the operating system may
clean temporary folders. Move files into a permanent location to keep them.
A temporary working directory is not a sandbox; normal session permissions
still apply.
