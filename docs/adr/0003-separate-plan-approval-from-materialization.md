# Separate Plan approval from materialization

Plan approval binds one exact immutable machine-readable manifest but never mutates the execution graph. A dedicated service-only Materialization Issue applies that manifest atomically and idempotently, preventing unapproved or partially created work at the cost of an explicit control node between approval and execution.
