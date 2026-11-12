package mcpauth

const (
	CallerPlatformHeader = "X-Ocman-Caller-Platform"
	CallerSessionHeader  = "X-Ocman-Caller-Session"
)

// SessionHeaders carries correlation outside model arguments. The ordinary MCP
// bearer token still authenticates the client; these headers grant no authority.
func SessionHeaders(token, kind, sessionID string) map[string]string {
	headers := Headers(token)
	if sessionID != "" {
		headers[CallerPlatformHeader] = kind
		headers[CallerSessionHeader] = sessionID
	}
	return headers
}
