package ocv2

import (
	"net/http"
	"testing"
)

func TestCompatCreatePreservesPlannedSessionID(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("POST /api/session", http.StatusOK, fakeSessionJSON)
	status, body := cmpDo(t, http.MethodPost, f.URL+"/session", nil, `{"id":"ses_planned","title":"T","directory":"/repo"}`)
	if status != http.StatusOK {
		t.Fatalf("create: %d %s", status, body)
	}
	cmpJSON(t, "planned creation", f.one("POST /api/session").Body,
		`{"id":"ses_planned","title":"T","location":{"directory":"/repo"}}`)
}
