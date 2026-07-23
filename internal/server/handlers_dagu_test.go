package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/dagu"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

type daguStatusHost struct {
	hostsvc.Host
	result dagu.Result
}

func (h daguStatusHost) RemoteID() string                       { return "remote-1" }
func (h daguStatusHost) DaguStatus(context.Context) dagu.Result { return h.result }

func TestDaguStatusRoutesToOwner(t *testing.T) {
	s := New(nil, nil, "", nil, nil)
	s.router().RegisterRemote("remote-1", daguStatusHost{result: dagu.Result{Status: dagu.Compatible, Version: "2.1.0"}})
	req := httptest.NewRequest(http.MethodGet, "/api/dagu/status?remoteId=remote-1", nil)
	rec := httptest.NewRecorder()

	s.handleDaguStatus(rec, req)

	var got dagu.Result
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil || got.Status != dagu.Compatible || got.Version != "2.1.0" {
		t.Fatalf("response = %+v, err = %v", got, err)
	}
}
