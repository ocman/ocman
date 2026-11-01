package analytics

import (
	"net/http"
	"strings"
)

// loopbackOnly stands in for the server's localhost guard.
func loopbackOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	}
}
