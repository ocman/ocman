package projectforge

import "net/http"

// PRToolHandler reuses project handlers after MCP authentication. It excludes
// settings, credentials and issue operations; callers cannot choose a route.
func (m *Module) PRToolHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/project/upstreams", m.handleUpstreams)
	mux.HandleFunc("GET /api/project/prs", m.handlePRs)
	mux.HandleFunc("GET /api/project/pr-checks", m.handlePRChecks)
	mux.HandleFunc("GET /api/project/pr-mergeability", m.handlePRMergeability)
	mux.HandleFunc("POST /api/project/handle", m.handleHandle)
	return mux
}
