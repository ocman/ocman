# Backend implementation rules

Read [repository.md](repository.md) to locate ownership. Application packages
live under `internal/`. The public plugin SDK is `sdk/plugin`; its DTOs alias
canonical `internal/plugins` types rather than duplicating wire definitions.

## Feature modules

A feature owning routes, background work, MCP tools or event reactions is a
module under `internal/module`, not a collection of `*Server` methods.
`Server.modules()` in `internal/server/modules.go` is the only registry.

- `Starter` runs before serving. `Launcher` runs after the MCP listener starts,
  for work whose agents call MCP. `Runner` workers are joined at shutdown.
  During initial canonical cutover the server defers session consumers behind
  `sessionPreparation` while HTTP/auth/task status serve. Do not start a session
  worker outside `startSessionServices` or bypass the HTTP/MCP/gRPC fences.
- `Closer` runs in reverse order after HTTP/MCP listeners stop and before workers
  are joined. `ToolProvider` supplies MCP tools; `Subscriber` uses the typed bus.
  `Finalizer` releases startup resources after workers join, even when startup
  fails before `Run` or `Launch`; cleanup must tolerate unopened resources.
- Factory is the Launcher/Closer and verifies launched sessions' MCP connection.
  Auto-approve comes first to seed judge delay and run the headless watcher.
  Plugins come next, binding their lifetime and discovering in `Start`, then
  pumping replies and reconciling in `Run`. Preserve the server's deferred plugin
  process stop after listeners and launched modules close, before worker joins.
- A module never imports `internal/server` or another module. Inject narrow
  interfaces and functions through `Deps`. Webhooks reach routines through
  `webhook.Routines`, for example.
- Read mutable dependencies such as relay URL, auth and remotes at call time
  through functions. Build modules once with the server's `lazy[T].get(build)`;
  tests can `set` before first use. Use an untyped nil for absent interfaces,
  never a nil pointer wrapped in an interface.

## Routes and owner boundaries

- Session mutations go through `sessionsvc` for both REST and remote gRPC.
- Session operations use `platforms.Platform` and the registry. Directory/host
  operations use `hostsvc.Host` and its router. Handlers must not call git, tmux
  or whisper directly; owner-side integration lives in `hostsvc/local`.
- Every application git subprocess must use `internal/gitexec`. It strips
  `GIT_DIR`/`GIT_INDEX_FILE` and disables terminal prompts and optional locks.
- Resolve explicit `remoteId` through `Server.resolveOwner`; disconnected named
  remotes return 503. Only inferred `ForDir` ownership may fall back to the hub.
- Enforce methods with Go mux method patterns such as `GET /api/projects` or
  `GET /api/session/{id}/shares`; the mux answers a wrong method with 405. API
  routes live on their own mux so the SPA fallback cannot shadow that 405.
  Privileged host-control routes also use `requireLocalhost`, checking both the
  peer and browser origin.
- Module routes are guarded by the router: `Router.Handle`/`Setting` require a
  login, `Router.Local`/`LocalSetting` are localhost-only. `Router.Public` is
  for routes that are open by design or guard subroutes themselves with
  `Router.Guards()`. Tests mount modules with `module/moduletest`.
- Use `internal/httpx` for shared JSON, request-body, inert-file and platform
  error behavior. Do not make module responses diverge from server responses.

## Events and lifetime

`module.Bus` is typed and synchronous; subscribers run in order. Start slow
work through `module.Workers`, bound in `Start` and joined in `Run`, not a bare
goroutine on `context.Background()` that could outlive state.db. Periodic work
uses `module.Every`; other background bodies recover with
`module.RunWithRecover`.

Add an event type only when a module needs it. `FirstPromptAdded` starts
owner-local naming after the first accepted input. `SessionIdle` is published
after quota cooldown recording and follow-up flush admission. Factory's
subscriber only signals dispatch. Plugin reply reads run under their own panic
recovery because the bus cannot recover a panic in a separate goroutine.

## Persistence

Queries own persistence. Existing tables stay in `internal/state` and its
append-only core `schema_version` sequence. Never edit or renumber shipped
migrations.

A module with new tables queried in its own package declares a
`state.ModuleSchema` with a stable `Module` key, `Tables` and append-only
`Migrations`. Version n is `Migrations[n-1]`. Query through `state.DB.SQL()` and
register in `server.ModuleSchemas()`. Tests open
`state.Open(statetest.Path(t), <pkg>.Schema)`.

`state.Open` runs core then module schemas in list order in one transaction,
tracked in `module_schema_version`. Back up existing data first unless the core
batch already did. Fail closed on duplicate modules/tables, nil migrations,
declared tables not created, or foreign keys into another module's tables. Log
and skip schemas newer than this build, as with a newer core schema.

Read [development.md](development.md) before opening writable development data.
