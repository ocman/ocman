package platforms

import (
	"context"
	"encoding/json"
	"net/url"
)

// PullRequestRequest is a closed feature operation, never an HTTP proxy.
// Platform and ownership are supplied by the resolved adapter, not the payload.
type PullRequestRequest struct {
	Operation string          `json:"operation"`
	SessionID string          `json:"sessionId"`
	Query     url.Values      `json:"query,omitempty"`
	Body      json.RawMessage `json:"body,omitempty"`
}

type PullRequestResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"contentType"`
	Body        []byte `json:"body"`
}

// PullRequestWorkflow routes application PR state to the session owner.
type PullRequestWorkflow interface {
	PullRequestOperation(context.Context, PullRequestRequest) (PullRequestResponse, error)
}
