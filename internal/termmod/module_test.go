package termmod

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/module/moduletest"
	"github.com/NoUseFreak/ocman/internal/term"
)

// fakeHost implements only the terminal methods; the embedded nil Host
// panics on anything else.
type fakeHost struct {
	hostsvc.Host
	calls    int
	attach   func(context.Context, hostsvc.TermAttachRequest, hostsvc.TermConn) error
	killed   string
	windows  []hostsvc.TermWindow
	createAs string
}

func (h *fakeHost) TermWindows(context.Context, string) ([]hostsvc.TermWindow, error) {
	h.calls++
	return h.windows, nil
}

func (h *fakeHost) TermCreateWindow(context.Context, string) (string, error) {
	h.calls++
	return h.createAs, nil
}

func (h *fakeHost) TermKillWindow(_ context.Context, _, window string) error {
	h.calls++
	h.killed = window
	return nil
}

func (h *fakeHost) TermAttach(ctx context.Context, req hostsvc.TermAttachRequest, c hostsvc.TermConn) error {
	h.calls++
	return h.attach(ctx, req, c)
}

func newMux(host *fakeHost, privileged func(*http.Request) bool) *http.ServeMux {
	mux := http.NewServeMux()
	NewModule(Deps{
		ResolveOwner: func(w http.ResponseWriter, _, remoteID string) (hostsvc.Host, bool) {
			if remoteID == "gone" {
				http.Error(w, "remote gone is not connected", http.StatusServiceUnavailable)
				return nil, false
			}
			return host, true
		},
		Privileged: privileged,
	}).Routes(moduletest.Router{Mux: mux})
	return mux
}

func allow(*http.Request) bool { return true }

func serve(mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

func TestWindowsValidation(t *testing.T) {
	dir := "/home/u/proj"
	foreign := "ocman-deadbeef00-1" // valid shape, wrong hash for dir
	tests := []struct {
		name, method, target, body string
		want                       int
	}{
		{"list missing dir", http.MethodGet, "/api/term/windows", "", http.StatusBadRequest},
		{"list relative dir", http.MethodGet, "/api/term/windows?dir=rel", "", http.StatusBadRequest},
		{"create relative dir", http.MethodPost, "/api/term/windows", `{"dir":"rel"}`, http.StatusBadRequest},
		{"create bad json", http.MethodPost, "/api/term/windows", `{`, http.StatusBadRequest},
		{"delete relative dir", http.MethodDelete, "/api/term/windows", `{"dir":"rel","window":"` + foreign + `"}`, http.StatusBadRequest},
		{"delete cross-dir window", http.MethodDelete, "/api/term/windows", `{"dir":"` + dir + `","window":"` + foreign + `"}`, http.StatusNotFound},
		{"method", http.MethodPut, "/api/term/windows", "", http.StatusMethodNotAllowed},
		{"list gone owner", http.MethodGet, "/api/term/windows?dir=" + dir + "&remoteId=gone", "", http.StatusServiceUnavailable},
		{"ws relative dir", http.MethodGet, "/api/term/ws?dir=rel", "", http.StatusBadRequest},
		{"ws foreign window", http.MethodGet, "/api/term/ws?dir=" + dir + "&window=" + foreign, "", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := &fakeHost{}
			if rec := serve(newMux(host, allow), tt.method, tt.target, tt.body); rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
			if host.calls != 0 {
				t.Fatalf("host called %d times on a rejected request", host.calls)
			}
		})
	}
}

func TestWindowsCRUD(t *testing.T) {
	dir := "/home/u/proj"
	host := &fakeHost{createAs: term.WindowPrefix(dir) + "2"}
	mux := newMux(host, allow)

	if rec := serve(mux, http.MethodGet, "/api/term/windows?dir="+dir, ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"windows":[]}` {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	if rec := serve(mux, http.MethodPost, "/api/term/windows", `{"dir":"`+dir+`"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"window":"`+host.createAs+`"`) {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	win := term.WindowPrefix(dir) + "1"
	if rec := serve(mux, http.MethodDelete, "/api/term/windows", `{"dir":"`+dir+`","window":"`+win+`"}`); rec.Code != http.StatusNoContent || host.killed != win {
		t.Fatalf("delete = %d, killed %q", rec.Code, host.killed)
	}
}

// dial opens the terminal WebSocket and returns the handshake status.
func dial(t *testing.T, srv *httptest.Server, query string) (*websocket.Conn, int, error) {
	t.Helper()
	ws, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/term/ws?"+query, nil)
	status := 0
	if resp != nil {
		status = resp.StatusCode
		resp.Body.Close()
	}
	if ws != nil {
		t.Cleanup(func() { ws.Close() })
	}
	return ws, status, err
}

// A rejected owner must answer plain HTTP before any upgrade: the dial
// fails with the owner's status and no shell is attached.
func TestWSRejectsGoneOwnerBeforeUpgrade(t *testing.T) {
	host := &fakeHost{}
	srv := httptest.NewServer(newMux(host, allow))
	defer srv.Close()
	_, status, err := dial(t, srv, "dir=/repo&remoteId=gone")
	if err == nil || status != http.StatusServiceUnavailable {
		t.Fatalf("dial err = %v, status = %d; want 503", err, status)
	}
	if host.calls != 0 {
		t.Fatal("attached a terminal for a disconnected owner")
	}
}

func TestWSCheckOriginUsesPrivilegedGuard(t *testing.T) {
	for name, privileged := range map[string]func(*http.Request) bool{
		"refused": func(*http.Request) bool { return false },
		"nil":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			host := &fakeHost{}
			srv := httptest.NewServer(newMux(host, privileged))
			defer srv.Close()
			_, status, err := dial(t, srv, "dir=/repo")
			if err == nil || status != http.StatusForbidden {
				t.Fatalf("dial err = %v, status = %d; want 403", err, status)
			}
			if host.calls != 0 {
				t.Fatal("attached without a privileged request")
			}
		})
	}
}

// The bridge classifies frames (resize JSON text, other text, binary),
// passes window and readonly through, sends PTY output back as binary,
// and the attach ends when the browser goes away.
func TestWSFramingReadonlyAndClientClose(t *testing.T) {
	dir := "/home/u/proj"
	win := term.WindowPrefix(dir) + "3"
	got := make(chan []string, 1)
	ended := make(chan error, 1)
	host := &fakeHost{attach: func(_ context.Context, req hostsvc.TermAttachRequest, c hostsvc.TermConn) error {
		frames := []string{req.Dir + "|" + req.Window + "|" + strconv.FormatBool(req.Readonly)}
		for range 3 {
			f, err := c.Recv()
			if err != nil {
				ended <- err
				return err
			}
			if f.Resize != nil {
				frames = append(frames, "resize:"+strconv.Itoa(int(f.Resize.Cols))+"x"+strconv.Itoa(int(f.Resize.Rows)))
			} else {
				frames = append(frames, "data:"+string(f.Data))
			}
		}
		got <- frames
		if err := c.Write([]byte("OUT")); err != nil {
			return err
		}
		_, err := c.Recv() // blocks until the browser closes
		ended <- err
		return err
	}}
	srv := httptest.NewServer(newMux(host, allow))
	defer srv.Close()

	ws, _, err := dial(t, srv, "dir="+dir+"&window="+win+"&readonly=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":80,"rows":24}`))
	_ = ws.WriteMessage(websocket.TextMessage, []byte("ls\n"))
	_ = ws.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02})

	want := []string{dir + "|" + win + "|true", "resize:80x24", "data:ls\n", "data:\x01\x02"}
	if frames := <-got; strings.Join(frames, ",") != strings.Join(want, ",") {
		t.Fatalf("frames = %q, want %q", frames, want)
	}
	if mt, data, err := ws.ReadMessage(); err != nil || mt != websocket.BinaryMessage || string(data) != "OUT" {
		t.Fatalf("output = %d %q %v, want binary OUT", mt, data, err)
	}
	ws.Close()
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("Recv returned nil after the browser closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attach did not end after the browser closed")
	}
}

// An attach failure closes the socket with an internal-error close frame.
func TestWSAttachErrorSendsCloseFrame(t *testing.T) {
	host := &fakeHost{attach: func(context.Context, hostsvc.TermAttachRequest, hostsvc.TermConn) error {
		return errors.New("no tmux")
	}}
	srv := httptest.NewServer(newMux(host, allow))
	defer srv.Close()
	ws, _, err := dial(t, srv, "dir=/repo")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ws.ReadMessage()
	var ce *websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != websocket.CloseInternalServerErr || ce.Text != "terminal error" {
		t.Fatalf("read err = %v, want close 1011 terminal error", err)
	}
}

func TestModuleName(t *testing.T) {
	if got := NewModule(Deps{}).Name(); got != "terminals" {
		t.Fatalf("Name() = %q", got)
	}
}

func TestWSForwardsCancellationAndClosesAfterAttach(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	attached := make(chan struct{})
	host := &fakeHost{attach: func(got context.Context, req hostsvc.TermAttachRequest, c hostsvc.TermConn) error {
		if req.Readonly || req.Window != "" {
			t.Errorf("default attach = %#v", req)
		}
		close(attached)
		<-got.Done()
		return got.Err()
	}}
	mux := newMux(host, allow)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer srv.Close()
	ws, _, err := dial(t, srv, "dir=/repo")
	if err != nil {
		t.Fatal(err)
	}
	<-attached
	cancel()
	if err := ws.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _, err = ws.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseInternalServerErr) {
		t.Fatalf("cancelled attach = %v, want close 1011", err)
	}
}

// An idle terminal ends with its request: shutdown cancels the request
// context, but never closes a hijacked socket, so the handler closes it and
// a host blocked reading keystrokes returns.
func TestWSClosesIdleTerminalWhenRequestEnds(t *testing.T) {
	attached, returned := make(chan struct{}), make(chan struct{})
	host := &fakeHost{attach: func(_ context.Context, _ hostsvc.TermAttachRequest, c hostsvc.TermConn) error {
		close(attached)
		defer close(returned)
		for { // like AttachLocalPTY's keystroke loop: ignores ctx
			if _, err := c.Recv(); err != nil {
				return err
			}
		}
	}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv := httptest.NewUnstartedServer(newMux(host, allow))
	srv.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	srv.Start()
	defer srv.Close()
	ws, _, err := dial(t, srv, "dir=/repo")
	if err != nil {
		t.Fatal(err)
	}
	<-attached
	cancel() // the server's lifetime ends; the browser stays idle
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("an idle terminal kept its handler running after the request ended")
	}
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("the terminal stream stayed open")
	}
}
