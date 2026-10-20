package server

import (
	"net/http"
	"strings"
)

// Query parameters are normalized only on routes whose contract names them as
// session references. A search string or arbitrary plugin payload is untouched.
func (w *sessionViewWriter) projectSessionQuery(r *http.Request) error {
	if r.URL.Path != "/api/artifacts" && r.URL.Path != "/api/plugins/actions" {
		return nil
	}
	const field = "sessionId"
	q := r.URL.Query()
	ids := strings.Split(q.Get(field), ",")
	scope := newSessionReferenceScope(q.Get("platform"), "")
	for i, id := range ids {
		nativeID, err := scope.resolve(r.Context(), w.server, id, false)
		if err != nil {
			return err
		}
		ids[i] = nativeID
	}
	if q.Get(field) != "" {
		q.Set(field, strings.Join(ids, ","))
	}
	if scope.platform != "" {
		q.Set("platform", scope.platform)
		w.projector.Platform = scope.platform
	}
	r.URL.RawQuery = q.Encode()
	return nil
}
