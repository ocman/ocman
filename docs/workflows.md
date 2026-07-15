# Workflows

Workflows are source-controlled YAML or JSON DAGs. Publish creates an immutable
version; every run pins that version and its mapped subworkflow versions.

## Migration Preset

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
review findings are joined by the fixer through its two declared dependencies;
they are not a separate node type.

## Run It

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

## Diagnostics Fixture

`examples/workflows/diagnostic-partitions.yaml` captures compiler/test output
once, turns its Node Result into a stable partition list, and maps the existing
item workflow over that list. Do not put the expensive diagnostic command in
the per-item subworkflow: agents consume the partition Node Result rather than
rerunning discovery.

## Safety And Permissions

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

## Adapt It

Use a stable key that survives reordered discovery, tune run concurrency and
agent/compiler/commit pools to the host, and declare the smallest path leases.
Change the validation command to the project's real test command. Add an
approval before the commit coordinator when a human must inspect a batch.

## Troubleshooting

- **Map has no items:** inspect the source node's `output`; it must be a JSON
  array with unique non-empty keys.
- **A node waits:** inspect the resource pool and workspace lease sections;
  lower demand or raise an explicitly bounded capacity.
- **An item is unknown after restart:** pause, inspect its Node Result and
  historical artifacts, and resolve the attempt rather than blindly retrying a
  side effect.
- **Validation failed:** fix the item and rerun the version; never bypass the
  command gate by manually committing in a path-leased worker.

## Terminology

- **Definition/version/run:** authored DAG, immutable published revision, and
  one execution pinned to that revision.
- **Node/attempt/Node Result:** a graph step, one executor invocation, and the
  canonical `{id, name, started, ended, status, output}` result envelope.
- **Artifact:** an immutable historical file or payload, not a node-output
  channel.
- **Map item/join:** one stable-key child run and the ordered aggregate of item
  outcomes.
- **Pool/shard/lease:** bounded execution capacity, a run-owned worktree, and
  temporary exclusive or path-scoped ownership.
