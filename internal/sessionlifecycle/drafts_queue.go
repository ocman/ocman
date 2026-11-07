package sessionlifecycle

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
)

// Draft queue access authenticates admission, not a native binding that setup
// has not created yet. The committed request fixes the owner for every read.
func (m *Module) withDraftQueue(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !m.draftEnabled(w) {
			return
		}
		draft, err := (draftStore{m.d.DB}).get(r.Context(), r.PathValue("id"))
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			httpx.ServerError(w, "reading draft queue admission", err)
			return
		}
		var first startRequest
		if draft.Phase == "draft" || draft.Phase == "discarded" || len(draft.Request) == 0 {
			http.Error(w, "submit the first prompt before accessing follow-ups", http.StatusConflict)
			return
		}
		if err := json.Unmarshal(draft.Request, &first); err != nil || first.Platform == "" {
			http.Error(w, "draft queue owner unavailable", http.StatusServiceUnavailable)
			return
		}
		if next == nil {
			http.Error(w, "queue unavailable", http.StatusServiceUnavailable)
			return
		}
		resolved := r.Clone(r.Context())
		q := resolved.URL.Query()
		q.Set("platform", first.Platform)
		resolved.URL.RawQuery = q.Encode()
		next(w, resolved)
	}
}
