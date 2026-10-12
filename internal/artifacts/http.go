package artifacts

import (
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/state"
)

func (m *Module) handleDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := m.revokeAllShares(r.Context(), id); err != nil {
		writeArtifactShareError(w, m.RelayURL(), err)
		return
	}
	if err := m.Store.DeleteArtifact(r.Context(), id); err != nil {
		writeArtifactError(w, "deleting artifact", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) handleGet(w http.ResponseWriter, r *http.Request, id string) {
	a, err := m.Store.GetArtifact(r.Context(), id)
	if err != nil {
		writeArtifactError(w, "getting artifact", err)
		return
	}
	httpx.WriteJSON(w, view(a))
}

func writeArtifactError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, state.ErrArtifactInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, state.ErrArtifactNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		httpx.ServerError(w, operation, err)
	}
}

func (m *Module) handleList(w http.ResponseWriter, r *http.Request) {
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
		if q.Get("includeDescendants") == "1" && m.Descendants != nil {
			ids, err := m.Descendants(r.Context(), id)
			if err != nil {
				httpx.ServerError(w, "resolving descendant sessions", err)
				return
			}
			f.SessionIDs = ids
		}
	}
	list, next, err := m.Store.ListArtifacts(r.Context(), f)
	if err != nil {
		writeArtifactError(w, "listing artifacts", err)
		return
	}
	for i := range list {
		list[i] = view(list[i])
	}
	httpx.WriteJSON(w, map[string]any{"artifacts": list, "nextCursor": next})
}

func (m *Module) handleStats(w http.ResponseWriter, r *http.Request) {
	count, err := m.Store.CountArtifacts(r.Context())
	if err != nil {
		httpx.ServerError(w, "counting artifacts", err)
		return
	}
	bytes, err := m.Store.TotalArtifactBytes(r.Context())
	if err != nil {
		httpx.ServerError(w, "sizing artifacts", err)
		return
	}
	httpx.WriteJSON(w, map[string]int64{"count": int64(count), "totalBytes": bytes})
}

// InteractivePreviewCSP lets an HTML artifact run its own inline script and
// nothing else: the sandbox keeps it on an opaque origin (no app cookies,
// storage, DOM or same-origin requests, no popups, forms or top navigation),
// and default-src 'none' blocks fetch/XHR/WebSocket and every external
// subresource. The iframe's sandbox attribute must grant the same single
// allow-scripts token. A frame can still navigate itself, which no shipping
// CSP directive prevents; see docs/features/artifacts.md.
const InteractivePreviewCSP = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"

// serveFile streams one artifact file. interactive serves an HTML
// file under InteractivePreviewCSP instead of the inert sandbox; it is
// always inline and refuses any other type.
func (m *Module) serveFile(w http.ResponseWriter, r *http.Request, id, ordinal string, interactive bool) {
	a, err := m.Store.GetArtifact(r.Context(), id)
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
	if base, _, _ := mime.ParseMediaType(item.MIME); interactive && base != "text/html" {
		http.Error(w, "interactive preview is only available for HTML files", http.StatusNotFound)
		return
	}
	f, err := m.Store.OpenArtifactBlob(item.SHA256)
	if err != nil {
		writeArtifactError(w, "opening artifact file", err)
		return
	}
	defer f.Close()
	disposition := httpx.FileDisposition(item.MIME)
	if r.URL.Query().Get("download") == "1" && !interactive {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", item.MIME)
	httpx.SetInertFileHeaders(w)
	if interactive {
		w.Header().Set("Content-Security-Policy", InteractivePreviewCSP)
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": item.Name}))
	http.ServeContent(w, r, "", a.CreatedAt, f)
}
