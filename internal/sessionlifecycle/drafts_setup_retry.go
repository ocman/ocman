package sessionlifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
)

// Only a durable failure before allocation proves that setup can be repeated.
// Remote worktree calls report aggregate progress even for allocation failures,
// so their worktree/platform errors cannot establish this checkpoint.
func canRetryDraftSetup(d ConversationDraft) bool {
	if d.Phase != "failed" && d.Phase != "interrupted" || d.Steps[hostsvc.StepSession] != "" || d.Steps[hostsvc.StepPrompt] != "" {
		return false
	}
	var request startRequest
	var result struct{ SessionID string }
	if json.Unmarshal(d.Request, &request) != nil || request.Send == nil ||
		len(d.Result) != 0 && (json.Unmarshal(d.Result, &result) != nil || result.SessionID != "") {
		return false
	}
	if d.Steps["clone"] == hostsvc.StepError {
		return true
	}
	remote, _ := platforms.SplitPlatformID(request.Platform)
	if request.Worktree && (remote != "" || request.RemoteID != "" && request.RemoteID != "local") {
		return false
	}
	return d.Steps[hostsvc.StepPlatform] == hostsvc.StepError || d.Steps[hostsvc.StepWorktree] == hostsvc.StepError
}

// Explicit setup retry keeps the conversation identity, complete first payload
// and held queue. It never resets the draft into an editable/unsubmitted state.
func (m *Module) retryDraftSetup(w http.ResponseWriter, r *http.Request) {
	if !m.draftEnabled(w) {
		return
	}
	id := r.PathValue("id")
	unlock := m.lockDraftOutcome(id)
	defer unlock()
	draft, err := (draftStore{m.d.DB}).get(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		httpx.ServerError(w, "reading saved setup", err)
		return
	}
	if !draft.SetupRetryable {
		http.Error(w, "Setup cannot be safely repeated because session creation may have started. Your submitted message is still saved.", http.StatusConflict)
		return
	}
	var request startRequest
	if json.Unmarshal(draft.Request, &request) != nil || !request.ResolvePlatform(r.Context(), m, w, request.Send.Model) {
		return
	}
	claimed, err := m.d.DB.ExecContext(r.Context(), `UPDATE conversation_draft SET phase='accepted',error='',steps='{}'
		WHERE id=? AND phase=? AND error_version=? AND request=? AND result=?`, id, draft.Phase, draft.ErrorVersion, string(draft.Request), string(draft.Result))
	if err != nil {
		httpx.ServerError(w, "claiming setup retry", err)
		return
	}
	n, err := claimed.RowsAffected()
	if err != nil || n != 1 {
		http.Error(w, "setup has changed or is already being retried", http.StatusConflict)
		return
	}
	if !m.workers.Go(func(ctx context.Context) {
		m.runDraftWorker(ctx, id, func() { m.launchDraft(ctx, id, draft.Request) })
	}) {
		// No work was started. Restore the retryable receipt, not an uncertain
		// accepted state, so the next server can still offer the same action.
		write, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		steps, _ := json.Marshal(draft.Steps)
		_, err := m.d.DB.ExecContext(write, `UPDATE conversation_draft SET phase=?,error=?,steps=? WHERE id=? AND phase='accepted'`,
			draft.Phase, draft.Error, string(steps), id)
		if err != nil {
			httpx.ServerError(w, "restoring setup retry", err)
			return
		}
		http.Error(w, "server is stopping; your message is saved, retry when it is available", http.StatusServiceUnavailable)
		return
	}
	m.draftChanged()
	w.WriteHeader(http.StatusAccepted)
}
