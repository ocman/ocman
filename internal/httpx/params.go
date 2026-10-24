package httpx

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// QueryInt reads an integer query parameter, or fallback when it is absent,
// empty or not an integer that fits T.
func QueryInt[T ~int | ~int64](r *http.Request, name string, fallback T) T {
	n, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get(name)), 10, 64)
	if err != nil || int64(T(n)) != n {
		return fallback
	}
	return T(n)
}

// AbsDir reads the required ?dir= query parameter and validates that it is
// an absolute path, writing the HTTP error itself on failure. Returns the
// directory and whether to proceed.
func AbsDir(w http.ResponseWriter, r *http.Request) (string, bool) {
	dir := NormaliseDir(r.URL.Query().Get("dir"))
	if dir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return "", false
	}
	if !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return "", false
	}
	return dir, true
}

// NormaliseDir trims surrounding whitespace and a single trailing slash
// from a directory-prefix filter.
func NormaliseDir(raw string) string {
	dir := strings.TrimSpace(raw)
	if dir == "" {
		return ""
	}
	if strings.HasSuffix(dir, "/") && dir != "/" {
		dir = dir[:len(dir)-1]
	}
	return dir
}

// RemoteID requires an explicit owner ("local" for the hub), so a
// directory-scoped request never silently runs on an inferred machine.
func RemoteID(w http.ResponseWriter, raw string) (string, bool) {
	remoteID := strings.TrimSpace(raw)
	if remoteID == "" {
		http.Error(w, "remoteId is required", http.StatusBadRequest)
		return "", false
	}
	return remoteID, true
}
