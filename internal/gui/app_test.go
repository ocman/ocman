package gui

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBootstrapHandlerRedirectsToBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer backend.Close()

	rec := httptest.NewRecorder()
	newBootstrapHandler(backend.URL, time.Second).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "wails://wails/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if want := `location.replace("` + backend.URL + `/")`; !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("body %q does not contain %q", rec.Body.String(), want)
	}
}

func TestBootstrapHandlerReportsDeadBackend(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + ln.Addr().String()
	ln.Close()

	rec := httptest.NewRecorder()
	newBootstrapHandler(dead, 100*time.Millisecond).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "wails://wails/", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "location.replace") {
		t.Fatal("dead backend must not redirect into a blank window")
	}
}

func TestLoopbackURL(t *testing.T) {
	for _, tc := range []struct {
		addr net.Addr
		want string
	}{
		{&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5000}, "http://127.0.0.1:5000"},
		{&net.TCPAddr{IP: net.IPv4zero, Port: 5000}, "http://127.0.0.1:5000"},
		{&net.TCPAddr{IP: net.IPv6unspecified, Port: 5000}, "http://127.0.0.1:5000"},
		{&net.TCPAddr{IP: net.IPv6loopback, Port: 5000}, "http://[::1]:5000"},
	} {
		if got := loopbackURL(tc.addr); got != tc.want {
			t.Errorf("loopbackURL(%v) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

func TestSingleInstanceID(t *testing.T) {
	a := singleInstanceID("/home/u/.local/share/ocman/state.db")
	if a != singleInstanceID("/home/u/.local/share/ocman/../ocman/state.db") {
		t.Error("equivalent paths must share one lock")
	}
	if a == singleInstanceID("/tmp/other/state.db") {
		t.Error("a different state DB must get its own lock")
	}
	if !strings.HasPrefix(a, "ocman-") || len(a) != len("ocman-")+16 {
		t.Errorf("unexpected id %q", a)
	}
}
