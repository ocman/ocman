package forge

import "context"

// Remark is forge-supplied, untrusted review or discussion content.
type Remark struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	Path      string `json:"path,omitempty"`
	URL       string `json:"url,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

// FeedbackReader is optional, so forges can expose review context incrementally.
type FeedbackReader interface {
	Feedback(context.Context, string, int) ([]Remark, error)
}
