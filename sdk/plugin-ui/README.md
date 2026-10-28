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
