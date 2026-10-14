package previews

import "net/http"

func (m *Module) handlePreviewImage(w http.ResponseWriter, r *http.Request) {
	if !m.previewAccessAllowed(r) {
		http.Error(w, "app access required", http.StatusForbidden)
		return
	}
	body, contentType, err := m.linkPreviews().OpenGraphImage(r.Context(), r.URL.Query().Get("url"))
	if err != nil {
		http.Error(w, "preview image unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=60")
	_, _ = w.Write(body)
}
