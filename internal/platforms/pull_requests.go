package platforms

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/ocman/ocman/internal/forge"
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

// PullRequestSidebarLink is display-only owner state. Watch checkpoints and
// pending messages never leave the owner in the sidebar snapshot.
type PullRequestSidebarLink struct {
	SessionID string       `json:"sessionId"`
	Directory string       `json:"directory"`
	Remote    forge.Remote `json:"remote"`
	Number    int          `json:"number"`
	PR        forge.PR     `json:"pr"`
	Watching  bool         `json:"watching"`
	LastError string       `json:"lastError,omitempty"`
}

// PullRequestSidebarReader reads the last synced snapshot without doing I/O
// to the owner. ready distinguishes an empty snapshot from an unavailable one.
type PullRequestSidebarReader interface {
	PullRequestSidebar() (links []PullRequestSidebarLink, ready bool)
}
