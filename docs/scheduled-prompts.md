# Scheduled Prompts

Open a project's detail page to schedule one prompt for a future local time.
Ocman stores the prompt exactly as entered and creates a fresh OpenCode session
in that project when the schedule becomes due.

Each schedule shows its prompt, run time, and durable state. While it is still
scheduled, it can be canceled or run immediately. Completed and failed
schedules retain their execution details across restarts; once a session has
been created, **Open session** links to it even if sending the prompt failed.

One-time schedules are terminal after they run or are canceled. They do not
repeat or reuse an existing session.
