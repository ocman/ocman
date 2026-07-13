# Workflow UI Prototype Decisions

Ticket: #312, tracer for #311.

This is a disposable, fixture-only prototype. It has no workflow API, state store, graph library, persistence, SSE connection, or production capability flag. Delete `WorkflowPrototype.tsx`, its CSS/test, this document, and the two `App.tsx` route/navigation lines when production workflow UI work begins.

## Production Tracer Must Preserve

1. Source is authoritative. Authoring starts in editable YAML/JSON and the graph stays read-only; visual graph editing is not required.
2. Run and definition share one spatial model. Columns communicate dependency order, while node cards carry type, state, duration, and selection.
3. Large maps disclose progressively. The outer map summarizes aggregate state; item branches expand independently; nested maps remain visible as nested scopes. Collapsing never clears the selected node or inspector context.
4. Inspection stays adjacent to navigation. Selection opens attempts, logs, artifacts, resource occupancy/waits, and workspace shard/path ownership without replacing the graph.
5. State is never color-only. Every state has persistent text and a short symbol. Production must preserve pending, ready, running, waiting, successful, failed, skipped, blocked, unknown, paused, and canceled as distinct values.
6. Wide graphs pan horizontally. Narrow viewports stack the inspector below a bounded graph viewport instead of shrinking nodes into illegibility.
7. Large runs need direct navigation. Production should support jump-to-failure and indexed node/item search rather than requiring canvas traversal.
8. Aggregate counts must describe hidden work. Collapsed maps show item and descendant-node counts plus an outcome distribution.
9. Immutable identity remains visible. Run ID, workflow version, node ID, mapped stable key, and pinned subworkflow version belong in the inspection surface.
10. Production must virtualize or paginate mapped items and logs. This prototype intentionally renders only one expanded item pipeline at a time and must not become the data/rendering architecture.

## Fixture Shape

The migration fixture models discovery, 18 mapped migration units, implement and dual adversarial-review branches, join/fix/validation, three inspectable test shards per unit, resource contention, retries, artifacts, workspace leases, integration fan-in, and a serialized Git gate. Its generated structure totals 166 node runs so collapsed and narrow behavior can be judged without introducing an execution backend.
