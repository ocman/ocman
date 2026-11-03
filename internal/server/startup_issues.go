package server

// StartupIssue is a non-fatal problem detected at startup that the
// doctor endpoint reports (e.g. a missing OpenCode database).
type StartupIssue struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Startup issue IDs the doctor turns into checks.
const (
	IssueOpenCodeDBMissing   = "opencode-db-missing"
	IssueOpenCodeUnsupported = "opencode-unsupported-version"
)

// WithStartupIssues records the non-fatal issues found at startup.
func (s *Server) WithStartupIssues(issues ...StartupIssue) *Server {
	s.startupIssues = append(s.startupIssues, issues...)
	return s
}
func (s *Server) startupIssue(id string) (StartupIssue, bool) {
	for _, issue := range s.startupIssues {
		if issue.ID == id {
			return issue, true
		}
	}
	return StartupIssue{}, false
}
