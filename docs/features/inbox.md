---
title: Inbox
weight: 45
---

The Inbox collects messages that need attention across connected machines.
Press **Alt+I**, or **Option+I** on macOS, to open it from anywhere in the app.
Choose **All**, **Primary**, **Factory**, **Routines**, or **Permissions** in the
message-type group. One type is selected at a time; its icon and label are shown,
while the others show icons with hover titles. Combine the selected type with
All, Unread, or Read. Older messages appear under Primary.

Opening a message marks it read. Mark unread returns it to the unread list.
Messages show their source machine in the reading pane, but there is no machine
filter.

## Permission requests

Every observed permission request creates one durable Inbox message on its owning
machine, including requests from subagent sessions and sessions with auto-approval
disabled. Open the message to review its command or patterns and choose **Allow
once**, **Allow always**, or **Reject**. Allow always uses the same confirmation
step as the session's permission prompt. Open session takes you to the requesting
session.

Successful replies archive the message automatically, whether answered in the
Inbox, a session, another browser, the OpenCode TUI, or by auto-approval. A failed
reply keeps the message available for retry. A background check every 30 seconds
archives requests that disappeared after an aborted turn or a missed event;
failed checks against an unavailable owner leave the message intact.

Permission toast and system-notification links open the permission-filtered
Inbox. Question notifications still open the session.

## Other messages

Factory delivery notifications use the Factory category. Routine runs create a
Routine message when they succeed, fail, or are interrupted.

Agents can use the `inbox` MCP tool to send messages with an optional `category`
of `general`, `factory`, or `routine`. The default, `general`, appears as Primary
in the dashboard. The `permission`
category is reserved for actual permission requests and cannot be supplied by
an agent. Use the tool's `help` action for its current schema.
