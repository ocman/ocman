# Scheduled Prompts

Open a project's detail page to schedule a prompt once, at an interval, or with
a standard five-field cron expression. Recurring schedules use an explicit IANA
timezone and show their next occurrence.

Each schedule shows its prompt, run time, and durable state. While it is still
scheduled, it can be canceled or run immediately. Completed and failed
schedules retain their execution details across restarts; once a session has
been created, **Open session** links to it even if sending the prompt failed.

Schedules can be disabled without deleting them. Fresh mode creates a new
OpenCode session for every occurrence. Reuse mode keeps the first schedule-owned
session and queues later prompts there while it is busy.
