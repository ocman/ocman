package prworkflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
)

type createdLink struct {
	Remote string
	Number int
	Known  []*forge.PR
}

func (m *Module) remote(t Target) (platforms.PullRequestWorkflow, error) {
	if m.d.Remote == nil {
		return nil, nil
	}
	return m.d.Remote(t)
}

func responseError(response platforms.PullRequestResponse, err error) error {
	if err != nil {
		return err
	}
	if response.Status < 200 || response.Status >= 300 {
		return fmt.Errorf("owner PR operation failed (%d): %s", response.Status, response.Body)
	}
	return nil
}

func (m *Module) forward(w http.ResponseWriter, r *http.Request, t Target, operation string) bool {
	owner, err := m.remote(t)
	if err == nil && owner == nil {
		return false
	}
	if err != nil {
		http.Error(w, "PR workflow owner unavailable", http.StatusServiceUnavailable)
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(body) > 4096 || (len(body) > 0 && !json.Valid(body)) {
		http.Error(w, "Invalid PR request", http.StatusBadRequest)
		return true
	}
	m.mu.Lock()
	err = m.migrate(r.Context(), t, owner)
	m.mu.Unlock()
	if err != nil {
		http.Error(w, "Could not transfer PR state to its owner", http.StatusServiceUnavailable)
		return true
	}
	response, err := owner.PullRequestOperation(r.Context(), platforms.PullRequestRequest{Operation: operation, SessionID: t.SessionID, Query: r.URL.Query(), Body: body})
	if err != nil {
		http.Error(w, "PR workflow owner unavailable", http.StatusServiceUnavailable)
		return true
	}
	if operation == "change" && response.Status >= 200 && response.Status < 300 && m.d.Changed != nil {
		m.d.Changed(t.Platform, t.SessionID)
	}
	// Wire results use owner-local identity; REST callers keep the compound
	// identity they used to select this session.
	if response.Status == http.StatusOK {
		switch operation {
		case "list":
			var result struct {
				PullRequests []Link `json:"pullRequests"`
			}
			if json.Unmarshal(response.Body, &result) == nil {
				for i := range result.PullRequests {
					result.PullRequests[i].Platform, result.PullRequests[i].RemoteID = t.Platform, t.RemoteID
				}
				response.Body, _ = json.Marshal(result)
			}
		case "change":
			var link Link
			if json.Unmarshal(response.Body, &link) == nil {
				link.Platform, link.RemoteID = t.Platform, t.RemoteID
				response.Body, _ = json.Marshal(link)
			}
		}
	}
	w.Header().Set("Content-Type", response.ContentType)
	w.WriteHeader(response.Status)
	_, _ = w.Write(response.Body)
	return true
}

// migrate transfers legacy hub-owned links before any new operation. The owner
// inserts only absent rows, so retrying after a lost acknowledgement cannot
// reset a watch or overwrite changes made directly on the owner.
// Caller holds m.mu; no forge reads or message delivery happen on the hub.
func (m *Module) migrate(ctx context.Context, t Target, owner platforms.PullRequestWorkflow) error {
	links, err := m.links(ctx, t.Platform, t.SessionID)
	if err != nil {
		return err
	}
	for _, l := range links {
		body, err := json.Marshal(persisted{l, l.Snapshot, l.Pending, l.Failures, l.NextAt, l.CommentWakes})
		if err != nil {
			return err
		}
		response, err := owner.PullRequestOperation(ctx, platforms.PullRequestRequest{Operation: "import", SessionID: t.SessionID, Body: body})
		if err = responseError(response, err); err != nil {
			return err
		}
		if err := m.remove(ctx, l); err != nil {
			return err
		}
	}
	return nil
}

// Operation serves authenticated owner-local RPCs through the same validation
// and feature handlers as REST and MCP. There is no caller-selected URL/method.
func (m *Module) Operation(ctx context.Context, platform string, req platforms.PullRequestRequest) platforms.PullRequestResponse {
	w := httptest.NewRecorder()
	method := http.MethodGet
	if req.Operation == "change" || req.Operation == "feedback" {
		method = http.MethodPost
	}
	r := httptest.NewRequestWithContext(ctx, method, "/", bytes.NewReader(req.Body))
	q := maps.Clone(req.Query)
	if q == nil {
		q = make(map[string][]string)
	}
	q.Set("platform", platform)
	q.Del("remoteId")
	r.URL.RawQuery = q.Encode()
	r.SetPathValue("id", req.SessionID)
	switch req.Operation {
	case "counts":
		counts, err := BackgroundCounts(ctx, m.d.DB)
		if err != nil {
			http.Error(w, "Could not read PR watches", http.StatusInternalServerError)
			break
		}
		result := map[string]int{}
		for key, count := range counts {
			if key.Platform == platform {
				result[key.SessionID] = count
			}
		}
		httpx.WriteJSON(w, result)
	case "list":
		m.list(w, r)
	case "change":
		m.change(w, r)
	case "checks":
		m.checks(w, r)
	case "feedback":
		m.feedback(w, r)
	case "created", "import":
		t, ok := m.d.Target(w, r, req.SessionID)
		if !ok {
			break
		}
		if req.Operation == "created" {
			var input createdLink
			if json.Unmarshal(req.Body, &input) != nil || input.Number <= 0 || input.Remote == "" {
				http.Error(w, "Invalid PR link", http.StatusBadRequest)
			} else if err := m.LinkCreated(ctx, t, input.Remote, input.Number, input.Known...); err != nil {
				http.Error(w, "Could not link PR", http.StatusBadGateway)
			}
		} else {
			m.importLink(w, r, t, req.Body)
		}
	default:
		http.Error(w, "Unknown PR operation", http.StatusBadRequest)
	}
	return recordedResponse(w)
}

func recordedResponse(w *httptest.ResponseRecorder) platforms.PullRequestResponse {
	return platforms.PullRequestResponse{Status: w.Code, ContentType: w.Header().Get("Content-Type"), Body: w.Body.Bytes()}
}

func (m *Module) importLink(w http.ResponseWriter, r *http.Request, t Target, body []byte) {
	var p persisted
	if json.Unmarshal(body, &p) != nil || p.Number <= 0 || p.Remote.Host == "" || p.Remote.Repo == "" {
		http.Error(w, "Invalid PR state", http.StatusBadRequest)
		return
	}
	p.Platform, p.SessionID, p.Directory, p.RemoteID = t.Platform, t.SessionID, t.Directory, t.RemoteID
	p.Link.Snapshot, p.Link.Pending, p.Link.Failures, p.Link.NextAt, p.Link.CommentWakes = p.Snapshot, p.Pending, p.Failures, p.NextAt, p.CommentWakes
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.d.DB == nil {
		http.Error(w, "PR storage unavailable", http.StatusServiceUnavailable)
		return
	}
	err := m.saveImport(r.Context(), p, body)
	if err != nil {
		http.Error(w, "Could not import PR state", http.StatusInternalServerError)
		return
	}
	httpx.WriteJSON(w, map[string]bool{"imported": true})
}

func (m *Module) saveImport(ctx context.Context, p persisted, original []byte) error {
	encoded, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := m.d.DB.SQL().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Include the resolved local identity so a receipt cannot cross sessions.
	hash := sha256.Sum256(append([]byte(p.Platform+"\x00"+p.SessionID+"\x00"), original...))
	result, err := tx.ExecContext(ctx, `INSERT INTO pr_workflow_import VALUES(?) ON CONFLICT DO NOTHING`, hex.EncodeToString(hash[:]))
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted > 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO session_pull_request VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, p.Platform, p.SessionID, p.Remote.Host, p.Remote.Repo, p.Number, string(encoded))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
