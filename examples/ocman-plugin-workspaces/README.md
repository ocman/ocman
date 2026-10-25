# Workspace contribution example

This plugin contributes a service configuration form, a list and linked item
details, all rendered with Ocman's existing components. It also demonstrates
static rules, submit-time validation, revisions and project-isolated private
storage. It has no external dependencies or service credentials.

See the [plugin contribution guide](../../docs/features/plugin-contributions.md)
for the supported options and installation walkthrough.

```sh
go test ./examples/ocman-plugin-workspaces
go build -trimpath -o ./ocman-plugin-workspaces ./examples/ocman-plugin-workspaces
```

Run these commands from the repository root. The executable belongs in the
selected owner's plugin directory. Discovery and enablement follow the normal
Settings flow. Do not launch `serve` against a shared working directory; Ocman
assigns it a private directory and launch configuration.
