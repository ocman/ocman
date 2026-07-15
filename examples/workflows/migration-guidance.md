# Migration Guidance

Keep work items small, stable, and independently reviewable. Preserve external
behavior, prefer direct translations over speculative cleanup, and record
unsupported constructs in the review findings. Workers may edit only their
declared path lease and must never stage, reset, stash, commit, or push.

Reviewers receive the implementation Node Result containing the exact Git diff and this policy only. They must
return structured, reproducible findings rather than changing files. The fixer
addresses joined findings; the validation command, not an agent claim, gates
the serialized commit coordinator.
