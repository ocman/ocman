---
title: Scheduled prompts
weight: 5
---

Open a project's detail page to schedule a prompt once, at an interval, or with
a standard five-field cron expression. Recurring schedules use an explicit IANA
timezone and show their next occurrence.

Each schedule shows its prompt, run time, and durable state. While it is still
scheduled, you can cancel it or run it immediately. Completed and failed
schedules keep their execution details across restarts, and once a session
exists, **Open session** links to it even if sending the prompt failed.

You can disable a schedule without deleting it. Fresh mode creates a new
OpenCode session for every occurrence. Reuse mode keeps the first
schedule-owned session and queues later prompts there while it is busy.
