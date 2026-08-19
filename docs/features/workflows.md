---
title: Workflows
weight: 4
---

Workflows are source-controlled YAML or JSON DAGs. Publishing one creates an
immutable version, and every run pins that version plus its mapped subworkflow
versions.

## The runner

Install Dagu 2.x separately. Ocman starts one private loopback-only Dagu
server on demand under `~/.local/share/ocman/dagu` and uses it to execute
workflow runs.

Ocman keeps everything except execution: authoring, validation, immutable
versions, triggers and their overlap policy, run history, artifacts, and the
UI. Dagu resolves the graph, runs steps, passes outputs, and propagates
skips. Because ocman owns scheduling, a compiled spec carries no `schedule`
and Dagu runs no scheduler of its own.

Runs reach the run view through a one-way mirror: ocman polls Dagu and
projects run, node, and attempt state back into `state.db`. Polling rather
than callbacks means a run survives an ocman restart and cannot miss an
event. Nothing reads execution state back out to drive scheduling.

Dagu only runs shell commands, so `ocman workflow-step` executes agent,
approval, join, and conditional nodes, invoked by the compiled spec. The real
node configuration stays in ocman, so prompts, models, and credentials never
reach a spec or a Dagu step log, and the private Dagu process gets a minimal
environment with no LLM credentials.

Some features have no verified translation yet: resource pools, managed
workspaces, path leases, secrets, run limits, fail-fast, and repeat. Those
keep running on ocman's native dispatcher. The compiler declines a definition
using them and routes it there automatically, so nothing has to change in the
workflow.

Cron triggers accept a `timezone`, so a schedule means the same wall-clock
time wherever ocman runs.

## Migration preset

`examples/workflows/adversarial-migration.yaml` is the reusable Bun-style
campaign. Publish `migration-item.yaml` first, then publish and activate the
campaign. Its discovery command prints a stable JSON array with an `id` and
`path`; the map consumes that Node Result and uses `id` as the restart-safe key.
`migration-guidance.md` is the shared, source-controlled implementation and
review policy referenced by the prompts.

Each item runs `migration-item.yaml`: implement, two independent reviews, fix,
command validation, then a commit coordinator. The implementer prompt names
only the item and shared guidance. Review prompts name only the implementation
diff and their policy, never the implementer session. The fixer has declared
dependencies on both structured findings. The bounded repeat policy prevents a
review/fix campaign from looping forever.

The generic map join aggregates item outcomes in stable input order. Sibling
review findings are joined by the fixer through its two declared dependencies,
so they are not a separate node type.

## Agent output

Agent completion does not require JSON by default. Set `agent.outputSchema` when
downstream nodes need structured output. Validate and publish compile the JSON
Schema, and each run includes it in the agent prompt and validates the final
response against it. A surrounding Markdown code fence is ignored. External
schema references are rejected, so use local `$defs` and fragment `$ref` values.

## Run it

1. Copy the examples into the repository being migrated and replace
   `/workspace/repository` with its absolute path.
2. Make the discovery script emit stable JSON, for example
   `[{"id":"parser","path":"src/parser.ts"}]`.
3. Validate and publish `migration-item.yaml`, activate it, then validate,
   publish, activate, and start `adversarial-migration.yaml` in the Workflows
   page or through `validate_workflow`, `publish_workflow`, and
   `start_workflow`.
4. Watch phases, stable map items and their Node Results, attempts, historical
   artifacts, resource waits, and held workspace leases in the run view. A
   failed validation blocks its commit.

## Diagnostics fixture

`examples/workflows/diagnostic-partitions.yaml` captures compiler/test output
once, turns its Node Result into a stable partition list, and maps the existing
item workflow over that list. Do not put the expensive diagnostic command in
the per-item subworkflow: agents consume the partition Node Result rather than
rerunning discovery.

## Safety and permissions

- Command nodes need explicit `bash` allow rules. There is no workflow-only
  permission bypass.
- Path leases declare non-overlapping writable scopes. Path-leased work is
  denied repository-wide Git mutations such as reset, stash, commit, and push.
- The commit coordinator is an exclusive lease and requests the one-capacity
  `commits` pool. It is the only node allowed to stage or commit.
- Keep shared guidance and review policy in prompts or checked-in files; pass
  secrets only by named environment references, never definition values.
- Command validation is the gate. An agent claiming completion does not let a
  commit run when validation fails.

## Adapt it

Use a stable key that survives reordered discovery, tune run concurrency and
agent/compiler/commit pools to the host, and declare the smallest path leases.
Change the validation command to the project's real test command. Add an
approval before the commit coordinator when a human must inspect a batch.

## Retry from a node

After a run succeeds or fails, select a node and choose **Retry from**. Ocman
creates a new run on the workflow's active immutable version, reuses successful
nodes before that point, and executes the selected node and its descendants.
Publish and activate an adjusted version first when fixing the workflow itself.
The new run links to the source run, and every reused attempt records its source
attempt ID.

Nodes and dependencies before the retry point must be unchanged and successful.
Retries are currently limited to static approval, command, and agent DAGs
without managed workspaces; map, join, and workspace workflows must be started
again. Active, paused, canceled, and unknown runs cannot be used as retry
sources.

## Troubleshooting

- **Map has no items.** Inspect the source node's `output`; it must be a JSON
  array with unique non-empty keys.
- **A node waits.** Inspect the resource pool and workspace lease sections;
  lower demand or raise an explicitly bounded capacity.
- **An item is unknown after restart.** Pause, inspect its Node Result and
  historical artifacts, and resolve the attempt rather than blindly retrying a
  side effect.
- **Validation failed.** Fix the item and rerun the version. Never bypass the
  command gate by manually committing in a path-leased worker.

## Terminology

- **Definition, version, run.** The authored DAG, an immutable published
  revision, and one execution pinned to that revision.
- **Node, attempt, Node Result.** A graph step, one executor invocation, and
  the canonical `{id, name, started, ended, status, output}` result envelope.
- **Artifact.** An immutable historical file or payload, not a node-output
  channel.
- **Map item, join.** One stable-key child run, and the ordered aggregate of
  item outcomes.
- **Pool, shard, lease.** Bounded execution capacity, a run-owned worktree,
  and temporary exclusive or path-scoped ownership.
