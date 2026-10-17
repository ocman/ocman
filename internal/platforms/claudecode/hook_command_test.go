package claudecode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunHookCommandManagedOnly(t *testing.T) {
	for _, marker := range []string{"", "0", "1"} {
		t.Run("marker="+marker, func(t *testing.T) {
			t.Setenv("OCMAN_MANAGED", marker)
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
			}))
			defer srv.Close()
			if !RunHookCommand([]string{"hook", "--addr", strings.TrimPrefix(srv.URL, "http://"), "Stop"}, strings.NewReader(`{"session_id":"s1"}`)) {
				t.Fatal("hook not handled")
			}
			want := int32(0)
			if marker == "1" {
				want = 1
			}
			if got := requests.Load(); got != want {
				t.Fatalf("requests = %d, want %d", got, want)
			}
		})
	}
}
