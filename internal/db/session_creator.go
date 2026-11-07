package db

// SessionCreator is application launch provenance, not native ancestry.
// Platform includes the machine scope needed to identify the creator.
type SessionCreator struct {
	Platform  string `json:"platform"`
	SessionID string `json:"sessionId"`
	CreatedAt int64  `json:"createdAt"`
}
