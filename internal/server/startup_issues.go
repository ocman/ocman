package server

// StartupIssue is a non-fatal problem detected at startup that the
// doctor endpoint reports (e.g. a missing OpenCode database).
type StartupIssue struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// WithStartupIssues records the non-fatal issues found at startup.
func (s *Server) WithStartupIssues(issues ...StartupIssue) *Server {
	s.startupIssues = append(s.startupIssues, issues...)
	return s
}

// StartupIssues returns the non-fatal issues recorded at startup.
func (s *Server) StartupIssues() []StartupIssue { return s.startupIssues }
