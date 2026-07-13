---
name: ocman-workflows
description: Use when authoring, starting, inspecting, or controlling ocman DAG workflows through MCP.
---

# Ocman Workflows

Workflows are immutable, versioned DAG definitions. Runs pin one version, so
publishing a later revision never changes an active or historical run.

## Authoring

The current tracer supports JSON definitions containing approval nodes only:

```json
{
  "id": "release",
  "name": "Release",
  "version": "1",
  "concurrency": 1,
  "nodes": [
    {"id": "review", "name": "Review", "type": "approval"},
    {"id": "ship", "name": "Ship", "type": "approval"}
  ],
  "dependencies": [{"from": "review", "to": "ship"}]
}
```

Use stable, meaningful workflow and node IDs. `concurrency` must be positive.
Definitions must be acyclic. Secret values do not belong in definitions;
future secret-aware nodes will reference secret names only.

## Safe Workflow

1. Call `validate_workflow` and fix every returned error.
2. Call `publish_workflow`; retain its immutable `version_id`.
3. Call `start_workflow` with `version_id` for reproducibility. Use
   `workflow_id` only when intentionally selecting the current active version.
4. Use `list_workflow_runs` and `inspect_workflow_run` for compact status.
5. Approve only a node shown as ready/waiting.
6. Pause before investigating uncertain side effects. Resolve an `unknown`
   attempt as `successful` or `failed` only after external verification.
7. Cancel when no further nodes should start.

Inspection returns stable IDs and artifact metadata, never artifact or secret
values. The current approval-only tracer has no artifacts, so the list is empty.

## Tools

- `validate_workflow`, `publish_workflow`, `list_workflows`
- `start_workflow`, `list_workflow_runs`, `inspect_workflow_run`
- `pause_workflow_run`, `resume_workflow_run`, `cancel_workflow_run`
- `approve_workflow_node`, `resolve_unknown_attempt`

Agent Loops remain a separate compatibility surface. Do not replace loop tool
calls with workflow tools until the compatibility migration ships.
