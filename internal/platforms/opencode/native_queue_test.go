package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// v2Hit is one request the fake v2 server observed.
type v2Hit struct {
	method, path string
	body         map[string]any
}

// v2Fake is a fake OpenCode server. With v2 set it answers GET /api/info
// like OpenCode v2; otherwise /api/info is a 404 (OpenCode v1).
type v2Fake struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.Mutex
	hits   []v2Hit
	handle func(w http.ResponseWriter, r *http.Request) bool
}

func newV2Fake(t *testing.T, v2 bool, handle func(w http.ResponseWriter, r *http.Request) bool) *v2Fake {
	t.Helper()
	f := &v2Fake{t: t, handle: handle}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := v2Hit{method: r.Method, path: r.URL.Path}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &hit.body)
		}
		f.mu.Lock()
		f.hits = append(f.hits, hit)
		f.mu.Unlock()
		if r.Method == http.MethodGet && r.URL.Path == "/api/info" && v2 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"2.0.22"}`))
			return
		}
		if f.handle != nil && f.handle(w, r) {
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(func() {
		f.server.Close()
		ocv2.ForgetHost("127.0.0.1:" + f.Port())
	})
	return f
}

func (f *v2Fake) Port() string { return strings.TrimPrefix(f.server.URL, "http://127.0.0.1:") }

func (f *v2Fake) find(method, path string) (v2Hit, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.hits {
		if h.method == method && h.path == path {
			return h, true
		}
	}
	return v2Hit{}, false
}

func writeJSONBody(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// nativeQueueAdapter wires an Adapter whose only session runs on f.
func nativeQueueAdapter(t *testing.T, sid, dir string, f *v2Fake) *Adapter {
	t.Helper()
	withTestPort(t, dir, f.Port())
	return New(newTestDBWithSession(t, sid, dir), nil)
}

const inboxBody = `{"data":[
 {"id":"msg_q1","type":"user","delivery":"queue","time":{"created":111},"payload":{"text":"queued one","files":[{"uri":"data:x"}]}},
 {"id":"msg_s1","type":"user","delivery":"steer","time":{"created":222},"payload":{"text":"steered"}},
 {"id":"msg_c1","type":"compaction","delivery":"queue","time":{"created":333},"payload":{"text":"compact"}},
 {"id":"msg_q2","type":"user","delivery":"queue","time":{"created":444},"payload":{"text":"queued two"}}
]}`

func TestNativeQueued_Unsupported(t *testing.T) {
	const sid, dir = "sess-nq-unsupported", "/tmp/proj-nq-unsupported"
	for _, tc := range []struct {
		name      string
		installed bool
		v2Server  bool
	}{
		{"v1 server", true, false},
		{"v1 installed", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer ocv2.SetInstalledV2(tc.installed)()
			f := newV2Fake(t, tc.v2Server, func(w http.ResponseWriter, r *http.Request) bool {
				writeJSONBody(w, inboxBody)
				return true
			})
			a := nativeQueueAdapter(t, sid, dir, f)
			if _, err := a.NativeQueued(context.Background(), sid); !errors.Is(err, platforms.ErrUnsupported) {
				t.Fatalf("NativeQueued err = %v, want ErrUnsupported", err)
			}
			err := a.CancelNativeQueued(context.Background(), platforms.CancelNativeQueuedRequest{SessionID: sid, ID: "msg_q1"})
			if !errors.Is(err, platforms.ErrUnsupported) {
				t.Fatalf("CancelNativeQueued err = %v, want ErrUnsupported", err)
			}
			if _, hit := f.find(http.MethodGet, "/api/session/"+sid+"/inbox"); hit {
				t.Fatal("inbox must not be read on a non-v2 server")
			}
		})
	}
}

func TestNativeQueued_UnknownSession(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	f := newV2Fake(t, true, nil)
	a := nativeQueueAdapter(t, "sess-nq-known", "/tmp/proj-nq-known", f)
	if _, err := a.NativeQueued(context.Background(), "missing"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestNativeQueued_ListsOnlyQueuedUserItems(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	const sid, dir = "sess-nq-list", "/tmp/proj-nq-list"
	f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodGet && r.URL.Path == "/api/session/"+sid+"/inbox" {
			writeJSONBody(w, inboxBody)
			return true
		}
		return false
	})
	a := nativeQueueAdapter(t, sid, dir, f)
	got, err := a.NativeQueued(context.Background(), sid)
	if err != nil {
		t.Fatalf("NativeQueued: %v", err)
	}
	want := []platforms.NativeQueuedMessage{
		{ID: "msg_q1", Text: "queued one", HasImages: true, CreatedAt: 111},
		{ID: "msg_q2", Text: "queued two", HasImages: false, CreatedAt: 444},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NativeQueued = %+v, want %+v", got, want)
	}
}

func TestNativeQueued_EmptyAndErrors(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"empty", `{"data":[]}`, 200, false},
		{"bad json", `{"data":`, 200, true},
		{"upstream 500", `{}`, 500, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sid, dir := "sess-nq-"+strings.ReplaceAll(tc.name, " ", "-"), "/tmp/proj-nq-err"
			f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
				return true
			})
			a := nativeQueueAdapter(t, sid, dir, f)
			got, err := a.NativeQueued(context.Background(), sid)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && (got == nil || len(got) != 0) {
				t.Fatalf("empty inbox = %#v, want non-nil empty slice", got)
			}
		})
	}
}

func TestCancelNativeQueued(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	for _, tc := range []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"deleted", http.StatusNoContent, false},
		{"already gone", http.StatusNotFound, false},
		{"upstream failure", http.StatusInternalServerError, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sid, dir := "sess-nq-cancel-"+strings.ReplaceAll(tc.name, " ", "-"), "/tmp/proj-nq-cancel"
			path := "/api/session/" + sid + "/inbox/msg_q1"
			f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == http.MethodDelete && r.URL.Path == path {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(`{}`))
					return true
				}
				return false
			})
			a := nativeQueueAdapter(t, sid, dir, f)
			err := a.CancelNativeQueued(context.Background(), platforms.CancelNativeQueuedRequest{SessionID: sid, ID: "msg_q1"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if _, hit := f.find(http.MethodDelete, path); !hit {
				t.Fatalf("DELETE %s not received", path)
			}
		})
	}
}

func TestSendMessageQueueDelivery_V1Unsupported(t *testing.T) {
	for _, installed := range []bool{true, false} {
		t.Run(map[bool]string{true: "v2 installed, v1 server", false: "v1 installed"}[installed], func(t *testing.T) {
			defer ocv2.SetInstalledV2(installed)()
			const sid, dir = "sess-send-queue-v1", "/tmp/proj-send-queue-v1"
			f := newV2Fake(t, false, func(w http.ResponseWriter, r *http.Request) bool {
				w.WriteHeader(http.StatusNoContent)
				return true
			})
			a := nativeQueueAdapter(t, sid, dir, f)
			err := a.SendMessage(context.Background(), platforms.SendMessageRequest{SessionID: sid, Message: "later", Delivery: "queue"})
			if !errors.Is(err, platforms.ErrUnsupported) {
				t.Fatalf("SendMessage err = %v, want ErrUnsupported", err)
			}
			if got := preferredSessionPort(sid); got != f.Port() {
				t.Fatalf("session port after ErrUnsupported = %q, want %q (must not be forgotten)", got, f.Port())
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, h := range f.hits {
				if h.method == http.MethodPost {
					t.Fatalf("unexpected send %s %s on a v1 server", h.method, h.path)
				}
			}
		})
	}
}

func TestSendMessageQueueDelivery_V2(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	const sid, dir = "sess-send-queue-v2", "/tmp/proj-send-queue-v2"
	f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost && r.URL.Path == "/api/session/"+sid+"/prompt" {
			writeJSONBody(w, `{"data":{"id":"msg_new"}}`)
			return true
		}
		return false
	})
	a := nativeQueueAdapter(t, sid, dir, f)
	if err := a.SendMessage(context.Background(), platforms.SendMessageRequest{SessionID: sid, Message: "hold me", Delivery: "queue"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	hit, ok := f.find(http.MethodPost, "/api/session/"+sid+"/prompt")
	if !ok {
		t.Fatal("v2 prompt not received")
	}
	if hit.body["delivery"] != "queue" || hit.body["text"] != "hold me" {
		t.Fatalf("prompt body = %#v, want delivery queue + text", hit.body)
	}
	if got := preferredSessionPort(sid); got != f.Port() {
		t.Fatalf("session port = %q, want %q", got, f.Port())
	}
}
