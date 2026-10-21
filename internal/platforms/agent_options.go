package platforms

import "context"

// AgentOptionsReader discovers live composer configuration. Directory hints
// come from stored history; native discovery and transport stay in the adapter.
type AgentOptionsReader interface {
	AgentOptions(context.Context, []string) []string
}
