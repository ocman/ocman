package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (w *sessionViewWriter) WriteRequestError(err error) { writeIdentityError(w, err) }

// ProjectRequest translates the route's declared wire fields, then applies a
// JSON patch to the decoded DTO. It does not inspect Go fields or walk payloads.
func (w *sessionViewWriter) ProjectRequest(r *http.Request, request any) error {
	names := sessionRequestFields(r)
	if len(names) == 0 {
		return nil
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var target struct {
		Platform string `json:"platform"`
		RemoteID string `json:"remoteId"`
	}
	if err := json.Unmarshal(raw, &target); err != nil {
		return err
	}
	if target.Platform == "" {
		target.Platform = r.URL.Query().Get("platform")
	}
	scope := newSessionReferenceScope(target.Platform, target.RemoteID)
	inferPlatform := scope.platform == ""
	patch := map[string]string{}
	for _, name := range names {
		field, ok := fields[name]
		if !ok {
			continue
		}
		var id string
		if err := json.Unmarshal(field, &id); err != nil {
			return err
		}
		nativeID, err := scope.resolve(r.Context(), w.server, id, name == "parentSessionId")
		if err != nil {
			return fmt.Errorf("resolving session reference: %w", err)
		}
		if nativeID != id {
			patch[name] = nativeID
		}
	}
	if inferPlatform && scope.platform != "" {
		patch["platform"] = scope.platform
		q := r.URL.Query()
		q.Set("platform", scope.platform)
		r.URL.RawQuery = q.Encode()
	}
	if len(patch) == 0 {
		return nil
	}
	raw, err = json.Marshal(patch)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, request)
}
