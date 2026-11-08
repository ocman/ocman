# Remote ownership, projects and forges

Read `docs/features/multi-remote.md`, `spec/multi-remote-support/` and
`spec/pr-issue-sidebar/` for full designs.

## Ownership

The browser talks REST/SSE only to the hub. The hub connects to other ocman
instances over long-lived authenticated gRPC and re-emits events as SSE.

- Remote access is opt-in through `-remote-listen`. Persist a stable random
  instance ID and token in state.db; reveal them only from that instance's
  Settings. TLS uses `-remote-tls-cert`/`-remote-tls-key` or `grpcs://`.
- Register a compound remote platform, `r-<remoteID>:opencode`, and a remote Host.
  Use `platforms` helpers to parse IDs, never import `remote` just for parsing.
- Session work uses Platform/Registry. Git, worktrees, terminals and directory
  operations execute on the owning Host. Explicit owner resolution must fail
  with 503 if disconnected. Only inferred `ForDir` may degrade to the hub.
- Duplicate live instance IDs must publish no adapters. `publishAdapters` marks
  the connection `duplicate-identity` rather than rebinding existing ownership.
  Cloned state.db files are a common source of duplicates.
- `-remote-managed` makes `state.db` read an allowlisted hub snapshot
  (`state.ManagedSettings`) instead of local values and reject local writes
  with `ErrManagedSetting`. Add a setting only if it affects running sessions
  and is not a secret, credential, installed integration or machine path.
  Hubs send on connect and after a managed setting changes. One hub per
  managed remote is advised, not enforced: the last hub to send wins.
- Per-session settings stored by the owner, such as the auto-approve toggle
  and manual rules, route through owner RPCs. Never write them to the hub.
- Machine-aware creation uses `/api/sessions/resolve-targets` and the composer
  selector; the default is local. Keep the frontend host-agnostic.
  golangci-lint's `forbidigo` rule enforces handler boundaries.

`internal/remotesmod` owns auth-gated remote CRUD/reconnect and local remote-access
settings. Status never includes the token; revealing this instance's own token
is localhost-only. Its joined worker refreshes inventory every five minutes.
Read the narrow Manager dependency at call time, using an untyped nil when absent.
Core retains manager construction/start/stop, the gRPC listener, registry, Host
router and owner resolution. Stop the manager after `Server.Start` returns and
before state.db closes.

`internal/projectsmod` owns the project list, identity resolution, archive,
merge and project settings routes. Manual project merging is hub-local display
bookkeeping, not a session move.
`projects.merges` stores owner-qualified checkout mappings. Resolve them against
current upstream groups and preserve merges across Git remote changes. Upstream
membership plus saved merges forms connected components; crossed groups cannot
undo saved merges. Multiple destinations use the lowest stable checkout key.
Keep checkout membership and disconnected-target aliases private to the backend.
`GET /api/projects` returns only final projects with aggregated totals; directory
scope applies to those final records. `POST /api/projects/resolve` resolves known
session and draft directories to final identities without exposing merge metadata.
Never change session directories or owners to merge their display groups.

## PR and issue pane

`internal/projectforge` owns all project forge endpoints. Every request requires
`remoteId`, with `local` for the hub. Detect repositories and fetch cross-fork
refs on the owner Host. Forge metadata uses the hub's clients; launches use the
owner's compound platform. Keep archive/quick settings in `projectsmod` and
session start in `sessionlifecycle`.

- Keep the pane available with an empty state if no supported remote exists.
  Forgejo classification uses configured hub hosts. Auth uses `GITHUB_TOKEN`,
  `FORGEJO_TOKEN`/`GITEA_TOKEN`, falling back to `gh auth token` or tea config.
- Row expansion shows Markdown and a launch split-button. Default creates a
  session in the project; the menu creates a worktree on the PR source branch.
  Cross-fork PRs fetch the head into `ocman/pr-<n>` only after confirmation.
- Carry owner through upstream lists, checks, forge identity, branch highlighting
  and optimistic session seeding. Cache successful forge identities across tab
  remounts, but keep failures retryable.
- Key lists by owner plus OpenCode `projectId` through `useProjectTarget`, so
  sibling worktrees retain lists, filters and checks. Only highlighting and launch
  directory follow the active session. Disable launches until it resolves.
- Fetch checks only when visible; poll every five seconds until complete. Empty
  results settle after three successful reads spaced thirty seconds apart.
  Errors back off from five to sixty seconds. Honor rate-limit retry times across
  viewport changes; rate-limited reads never settle the result. Pause while hidden.
- Cache settled checks by `host/repo@sha` in `prChecksCache.ts` localStorage,
  capped at the newest 1,000. Refresh clears this cache.
- Launch prompts use customizable Settings templates persisted in state.db's
  `setting` table, not hard-coded client prompts.
