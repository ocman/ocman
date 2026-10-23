package db

// TextMatch is one text part whose prose contains a searched string.
type TextMatch struct {
	Platform  string `json:"-"` // set by the caller; the DB doesn't know its adapter ID
	SessionID string `json:"-"`
	PartID    string `json:"partId"`
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
	Snippet   string `json:"snippet"`
}
