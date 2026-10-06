package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitBranchesOwnerOverridesDirectoryInference(t *testing.T) {
	for _, owner := range []string{"local", "B"} {
		t.Run(owner, func(t *testing.T) {
			router, hosts := ownerRouter("A")
			srv := &Server{hostRouter: router}
			rr := httptest.NewRecorder()
			srv.handleGitBranches(rr, httptest.NewRequest(http.MethodGet, "/api/git/branches?dir=/repo&remoteId="+owner, nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
			}
			var response struct {
				Branches []string `json:"branches"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Branches) != 1 || response.Branches[0] != owner+"/main" {
				t.Fatalf("branches = %v, want owner %s", response.Branches, owner)
			}
			if got := served(hosts); len(got) != 1 || got[0] != owner {
				t.Fatalf("served = %v, want only %s", got, owner)
			}
		})
	}
}

func TestGitBranchesOwnerFailsClosedWhenDisconnected(t *testing.T) {
	router, hosts := ownerRouter("A")
	srv := &Server{hostRouter: router}
	rr := httptest.NewRecorder()
	srv.handleGitBranches(rr, httptest.NewRequest(http.MethodGet, "/api/git/branches?dir=/repo&remoteId=gone", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if got := served(hosts); len(got) != 0 {
		t.Fatalf("served = %v, want no host calls", got)
	}
}
