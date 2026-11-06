# @ocman/plugin-ui

TypeScript declarations for the ocman-native components and hooks a plugin UI
bundle may import. ocman provides the implementation at runtime from
`/plugin-shared/ocman-plugin-ui.js`. Build with
[`@ocman/plugin-vite`](../plugin-vite/README.md), which keeps this module and
React, `react-router-dom` and `@tanstack/react-query` external.

`index.d.ts` is the contract. The host implementation in
`frontend/src/plugin-ui` is type-checked against it, and a test keeps the
runtime exports identical. Versions follow semver: additions are minor releases,
and removals or changed meaning are major releases. Declare the range you built
against in the plugin description (`plugin.NewUIDescriptor("^1.0.0", ...)`). ocman
refuses to load an incompatible bundle and explains why.

See the [contribution guide](../../docs/features/plugin-contributions.md#in-app-ui-with-uiv1)
for the component list and hooks, and
[ADR 0009](../../docs/adr/0009-let-plugins-ship-in-app-react-ui.md) for the design.

## Changes

- **1.3.0**: Adds `PluginCallError` with HTTP status, safe error category and
  parsed `Retry-After` delay. Adds opt-in `retry` and `retryDelay` query options.
  Mutations explicitly disable retries, including retrying query-client defaults.
- **1.2.0**: Adds `useSessionDraft`, which opens a new conversation draft in a
  project with a title and composer text for the user to review and send.
- **1.1.0**: Adds `PageState`, `InlineAlert`, `SkeletonTable`, `SkeletonLines`,
  `DataTableGroup`, `DataTableRow`, `RouteTabs`, `Icon`, `RelativeTime`,
  `formatExactDate`, `usePageTitle`, `useConfirm` and `useUnsavedChangesGuard`.
  `Page` now sets its title in the app header instead of rendering a second
  heading. Fixes `@ocman/plugin-ui` styles, such as `Pill`, `Section` spacing
  and `CodeBlock`, not loading in production builds.
- **1.0.0**: Initial release.

## Status-aware read retries

`usePluginCall` and `usePluginMutation` expose non-401 HTTP failures as
`PluginCallError`, an `Error` subclass with `status`, `category` and
`retryAfterMs`. Unknown or malformed categories and absent or invalid
`Retry-After` headers produce `null`. Delays are milliseconds, including HTTP-date
headers measured at response handling. Error messages never include response
diagnostics. Authentication, network and abort errors keep their existing types.

Requests beyond a process's advertised concurrency return HTTP 503 with category
`unavailable`. This also covers unavailable processes, so it is not proof of
saturation. The host does not predict when a slot will become free and does not
invent a `Retry-After` header. Increasing concurrency is a plugin declaration
decision, not a host override, and does not repair handlers that ignore cancellation.

Query retries default to `false`. Opt in only for methods known to be read-only.
For example, a catalog browser and a document index can use the same bounded
policy without the host knowing either workflow:

```tsx
import { PluginCallError, usePluginCall } from '@ocman/plugin-ui';

const readOptions = {
  retry: (failures: number, error: Error) => failures < 2 &&
    error instanceof PluginCallError &&
    (error.status === 429 || error.status === 503) &&
    (error.category === null || error.category === 'unavailable') &&
    (error.retryAfterMs === null || error.retryAfterMs <= 30_000),
  retryDelay: (attempt: number, error: Error) => Math.max(
    error instanceof PluginCallError ? error.retryAfterMs ?? 0 : 0,
    Math.min(30_000, 500 * 2 ** attempt + Math.random() * 250),
  ),
};

const items = usePluginCall('items.list', {}, readOptions);
const documents = usePluginCall('documents.list', {}, readOptions);
```

Callbacks use TanStack Query's zero-based failure/attempt counts. `retry: true`
means unlimited retries, so prefer a finite count or bounded callback. The query
passes its abort signal to the transport; unmounting the last observer cancels
the request and pending retries. Polling, focus refetches and explicit refetches
remain separate from retries. Plugins can gate queries with `enabled` during a
shared cooldown.

`usePluginMutation` never automatically retries. For an imperative operation
known to be read-only, a caller can inspect `PluginCallError` from `mutateAsync`
and implement its own finite retry budget and shared cooldown. Check cancellation
before each attempt and during waits. Do not retry saves or other mutations based
solely on an HTTP status; the server may already have applied their effects.
