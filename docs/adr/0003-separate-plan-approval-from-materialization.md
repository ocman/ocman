# Approval creates and starts the approved work

Plan approval binds one exact immutable machine-readable manifest, materializes its implementation Issues and dependencies, and dispatches ready work. The user approves once and receives a toast confirming that work is starting. Materialization remains an internal atomic, idempotent operation so retries cannot create duplicate work. A failed materialization can be retried against the same approved revision. Dispatch still respects paused Epics and capacity limits.
