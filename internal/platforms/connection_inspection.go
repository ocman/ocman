package platforms

import "context"

// ConnectionInspection describes a native account without requesting a model,
// logging in or refreshing credentials. Ready includes isolation policy checks.
// Messages and labels must never contain native credential or command output.
type ConnectionInspection struct {
	Ready              bool
	AccountLabel       string
	AuthMode           string
	NativeVersion      string
	IsolationSupported bool
	Message            string
}

// ConnectionInspector is optional. Legacy machine integrations keep their
// existing behavior and do not gain readiness through subscription usage probes.
type ConnectionInspector interface {
	InspectConnection(context.Context) (ConnectionInspection, error)
}
