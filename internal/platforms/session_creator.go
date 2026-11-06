package platforms

// SessionCreator identifies the session that launched this independent session.
// It does not imply native ancestry, inherited permissions or shared lifecycle.
type SessionCreator struct {
	Platform  string `json:"platform"`
	SessionID string `json:"sessionId"`
	CreatedAt int64  `json:"createdAt"`
}
