package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPermissionProfileNamesMatchSessionContract(t *testing.T) {
	_, handler := permissionProfileHTTPServer(t, &fakePlatform{id: "opencode"})
	created := doRoutineRequest(t, handler, http.MethodPost, "/api/permission-profiles", readOnlyProfileBody)
	var profile struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(created.Body.Bytes(), &profile) != nil || profile.ID == "" {
		t.Fatal(created.Body.String())
	}
	for _, name := range []string{strings.Repeat("x", 129), "line\nbreak", "tab\tname", "control\x7f", strings.Repeat("é", 65)} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(method+"/"+name, func(t *testing.T) {
				raw, _ := json.Marshal(name)
				body := strings.Replace(readOnlyProfileBody, `"read-only"`, string(raw), 1)
				path := "/api/permission-profiles"
				if method == http.MethodPut {
					path += "/" + profile.ID
				}
				if rec := doRoutineRequest(t, handler, method, path, body); rec.Code != http.StatusBadRequest {
					t.Fatalf("invalid name accepted: %d %s", rec.Code, rec.Body.String())
				}
			})
		}
	}
}
