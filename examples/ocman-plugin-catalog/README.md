# ocman-plugin-catalog

A working in-app UI plugin (`ui.v1`) with synthetic data. Its React index in
`ui/` uses `@ocman/plugin-ui` for a framed table, severity and status pills, a
filter, nested detail routes, a status mutation and a confirmed action that
restores the sample data.

```sh
make install-plugin PLUGIN=catalog   # builds ui/ with pnpm, then the executable
go test ./examples/ocman-plugin-catalog
```

Without a UI build, the executable embeds `fallback.js`, a minimal index that
asks you to build the UI, so `go build ./...` and the conformance tests work in
a clean checkout. Rescan in **Settings → Plugins**, review the disclosure and
bundle checksum, approve `ui.app`, and open **Catalog** in the navigation. The
interface loads only when the plugin runs on the hub machine.
