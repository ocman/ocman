package httpx

import (
	"net/http"
	"regexp"
	"strings"
)

var validIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func ValidID(id string) bool { return id != "" && len(id) <= 256 && validIDPattern.MatchString(id) }

func PlatformHint(r *http.Request) string { return strings.TrimSpace(r.URL.Query().Get("platform")) }

// SessionID returns the route's validated {id}, answering 400 when invalid.
func SessionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !ValidID(id) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return "", false
	}
	return id, true
}
