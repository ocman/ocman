package server

import (
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/state"
)

// handleArtifacts routes /api/artifacts, /api/artifacts/stats,
// /api/artifacts/{id}, /api/artifacts/{id}/files/{ordinal} and the
// /api/artifacts/{id}/share(s) relay routes.
func (s *Server) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/artifacts"), "/"), "/")
	switch {
	case parts[0] == "" && len(parts) == 1:
		requireGET(s.requireAuth(s.handleArtifactList))(w, r)
	case parts[0] == "stats" && len(parts) == 1:
		requireGET(s.requireAuth(s.handleArtifactStats))(w, r)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			if err := s.revokeAllArtifactShares(r.Context(), parts[0]); err != nil {
				writeArtifactShareError(w, s.relayURL, err)
				return
			}
			if err := s.stateDB.DeleteArtifact(r.Context(), parts[0]); err != nil {
				writeArtifactError(w, "deleting artifact", err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
	case len(parts) == 1:
		requireGET(s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			a, err := s.stateDB.GetArtifact(r.Context(), parts[0])
			if err != nil {
				writeArtifactError(w, "getting artifact", err)
				return
			}
			writeJSON(w, artifactView(a))
		}))(w, r)
	// Share routes hand out decryption keys, so they are localhost-only.
	case len(parts) == 2 && parts[1] == "share":
		requirePOST(s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) { s.handleArtifactShareCreate(w, r, parts[0]) }))(w, r)
	case len(parts) == 2 && parts[1] == "shares":
		requireGET(s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) { s.handleArtifactShareList(w, r, parts[0]) }))(w, r)
	case len(parts) == 3 && parts[1] == "share" && r.Method == http.MethodDelete:
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) { s.handleArtifactShareRevoke(w, r, parts[0], parts[2]) })(w, r)
	case len(parts) == 3 && parts[1] == "files":
		requireGET(s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			s.serveArtifactFile(w, r, parts[0], parts[2])
		}))(w, r)
	default:
		http.NotFound(w, r)
	}
}

func writeArtifactError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, state.ErrArtifactInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, state.ErrArtifactNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		serverError(w, operation, err)
	}
}

func (s *Server) handleArtifactList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := state.ArtifactFilter{Directory: q.Get("directory"), Platform: q.Get("platform"), Q: q.Get("q"), Cursor: q.Get("cursor")}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		f.Limit = n
	}
	if id := q.Get("sessionId"); id != "" {
		f.SessionIDs = []string{id}
		if q.Get("includeDescendants") == "1" && s.db != nil {
			ids, err := s.db.GetSessionDescendantIDs(r.Context(), id)
			if err != nil {
				serverError(w, "resolving descendant sessions", err)
				return
			}
			f.SessionIDs = ids
		}
	}
	list, next, err := s.stateDB.ListArtifacts(r.Context(), f)
	if err != nil {
		writeArtifactError(w, "listing artifacts", err)
		return
	}
	for i := range list {
		list[i] = artifactView(list[i])
	}
	writeJSON(w, map[string]any{"artifacts": list, "nextCursor": next})
}

func (s *Server) handleArtifactStats(w http.ResponseWriter, r *http.Request) {
	count, err := s.stateDB.CountArtifacts(r.Context())
	if err != nil {
		serverError(w, "counting artifacts", err)
		return
	}
	bytes, err := s.stateDB.TotalArtifactBytes(r.Context())
	if err != nil {
		serverError(w, "sizing artifacts", err)
		return
	}
	writeJSON(w, map[string]int64{"count": int64(count), "totalBytes": bytes})
}

func (s *Server) serveArtifactFile(w http.ResponseWriter, r *http.Request, id, ordinal string) {
	a, err := s.stateDB.GetArtifact(r.Context(), id)
	if err != nil {
		writeArtifactError(w, "getting artifact", err)
		return
	}
	n, err := strconv.Atoi(ordinal)
	if err != nil || n < 0 || n >= len(a.Items) || a.Items[n].Kind != state.ArtifactItemFile {
		http.NotFound(w, r)
		return
	}
	item := a.Items[n]
	f, err := s.stateDB.OpenArtifactBlob(item.SHA256)
	if err != nil {
		writeArtifactError(w, "opening artifact file", err)
		return
	}
	defer f.Close()
	disposition := fileDisposition(item.MIME)
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", item.MIME)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Same posture as handleFileProxy: active content (SVG, HTML) must not
	// run with the dashboard's origin when opened as a document.
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": item.Name}))
	http.ServeContent(w, r, "", a.CreatedAt, f)
}
