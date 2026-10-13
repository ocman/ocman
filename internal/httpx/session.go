package httpx

import (
	"net/http"
	"regexp"
	"strings"
)

var validIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func ValidID(id string) bool { return id != "" && len(id) <= 256 && validIDPattern.MatchString(id) }

func PlatformHint(r *http.Request) string { return strings.TrimSpace(r.URL.Query().Get("platform")) }

func WithSessionPath(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, string)) {
	path, found := strings.CutPrefix(r.URL.Path, "/api/session/")
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	id, rest, _ := strings.Cut(path, "/")
	if !ValidID(id) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	fn(w, r, id, rest)
}
