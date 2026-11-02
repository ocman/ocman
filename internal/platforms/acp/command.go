// Package acpexec provides an opt-in ACP execution transport. It does not
// register platforms, ingest history, install adapters or manage host ownership.
package acpexec

// Command launches an already-installed agent. Args are passed directly to the
// executable, never through a shell or an on-demand package installer.
type Command struct {
	Executable string
	Args       []string
}
