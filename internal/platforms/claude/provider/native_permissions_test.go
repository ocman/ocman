package claudeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestNativeAutoVersionGate(t *testing.T) {
	for version, want := range map[string]bool{
		"2.1.220": false, "2.1.298": false, "2.1.299": true, "v2.1.300": true,
		"2.2.0": true, "3.0.0": true, "unavailable": false, "2.1": false, "2.-1.300": false, "2.1.300-beta": false,
	} {
		if got := supportsNativeAuto(version); got != want {
			t.Errorf("%q=%v want=%v", version, got, want)
		}
	}
}

func TestSavedNativeAutoFailsClosedAfterCLIDowngrade(t *testing.T) {
	l := &live{owned: map[string]*owned{"s": {NativeMode: "auto"}}}
	if _, err := l.spawnLocked("s", "", ""); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("saved Auto must not launch without preflight support: %v", err)
	}
}

func TestNativeModePersistsWithoutChangingOcmanRules(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_VERSION", "2.1.299")
	h, _ := promptHarness(t, writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"done"}}`), nil)
	id := h.create(t)
	rules := []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "deny"}}
	if err := h.adapter.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: id, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	auto := "auto"
	if err := h.adapter.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: id, NativeMode: &auto}); err != nil {
		t.Fatal(err)
	}
	state, err := h.adapter.NativePermissionState(t.Context(), id)
	if err != nil || state.Mode != "auto" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	got, err := h.adapter.PermissionRules(t.Context(), id)
	if err != nil || !slices.Equal(got, rules) {
		t.Fatalf("rules=%v err=%v", got, err)
	}
	restarted := &live{opts: h.live.opts}
	restored, err := restarted.nativePermissionState(id)
	if err != nil || restored.Mode != "auto" {
		t.Fatalf("restart=%+v err=%v", restored, err)
	}
	h.turn(t, id, 1)
	args := h.launchArgs(t)
	if args[slices.Index(args, "--permission-mode")+1] != "auto" {
		t.Fatal(args)
	}
	if !strings.Contains(stdinLog(t), preflightCallback) {
		t.Fatal("auto child launched without pre-execution enforcement")
	}
}

func TestNativeModeLiveControlAndModelFallback(t *testing.T) {
	for _, refused := range []bool{false, true} {
		t.Run(map[bool]string{false: "acknowledged", true: "unsupported model"}[refused], func(t *testing.T) {
			l := &live{owned: map[string]*owned{"s": {}}, procs: map[string]*proc{}}
			p := &proc{id: "s", controls: map[string]chan controlResult{}, done: make(chan struct{}), stderr: &tail{}}
			l.procs["s"] = p
			p.stdin = sdkWriter{func(raw []byte) (int, error) {
				var request struct {
					ID      string                         `json:"request_id"`
					Request struct{ Subtype, Mode string } `json:"request"`
				}
				if err := json.Unmarshal(raw, &request); err != nil {
					t.Fatal(err)
				}
				if request.Request.Subtype != "initialize" && (request.Request.Subtype != "set_permission_mode" || request.Request.Mode != "auto") {
					t.Fatal(string(raw))
				}
				subtype := "success"
				if refused && request.Request.Subtype != "initialize" {
					subtype = "error"
				}
				response, _ := json.Marshal(map[string]any{"response": map[string]any{"request_id": request.ID, "subtype": subtype, "error": "auto mode unavailable for this model"}})
				l.receiveControl(p, response)
				return len(raw), nil
			}}
			auto := "auto"
			err := l.setNativeMode(t.Context(), "s", &auto)
			if refused {
				if err == nil || errStatus(err) != http.StatusUnprocessableEntity || l.owned["s"].NativeMode != "" {
					t.Fatalf("refused update=%v mode=%s", err, l.owned["s"].NativeMode)
				}
				return
			}
			if err != nil || p.nativeMode != "auto" {
				t.Fatalf("mode=%s err=%v", p.nativeMode, err)
			}
			l.observeNativeMode(p, []byte(`{"subtype":"status","permissionMode":"default"}`))
			state, err := l.nativePermissionState("s")
			if err != nil || state.Mode != "default" || l.owned["s"].NativeMode != "default" {
				t.Fatalf("fallback=%+v err=%v", state, err)
			}
		})
	}
}

func TestNativePermissionErrors(t *testing.T) {
	l := &live{owned: map[string]*owned{"s": {}, "foreign": {Config: "/elsewhere"}}}
	for _, mode := range []*string{nil, ptrNative("yolo")} {
		if err := l.setNativeMode(t.Context(), "s", mode); err == nil {
			t.Fatal("invalid mode accepted")
		}
	}
	if _, err := l.nativePermissionState("missing"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := l.nativePermissionState("foreign"); err == nil {
		t.Fatal("foreign config accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := l.setNativeMode(ctx, "s", ptrNative("auto")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	l.opts.StatePath = filepath.Join(t.TempDir(), "missing", "sessions.json")
	if err := l.setNativeMode(t.Context(), "missing", ptrNative("auto")); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
}

func ptrNative(mode string) *string { return &mode }

func TestNativePermissionHTTP(t *testing.T) {
	l := &live{nativeAuto: true, owned: map[string]*owned{"s": {}}}
	for _, tc := range []struct {
		method, path, body string
		status             int
		handled            bool
	}{
		{"GET", "/sessions/s/native-permission", "", 200, true},
		{"GET", "/sessions/missing/native-permission", "", 404, true},
		{"POST", "/sessions/s/native-permission", `{"NativeMode":"auto"}`, 204, true},
		{"POST", "/sessions/s/native-permission", `{"NativeMode":"bad"}`, 400, true},
		{"POST", "/sessions/s/native-permission", `{broken`, 400, true},
		{"DELETE", "/sessions/s/native-permission", "", 405, true},
		{"GET", "/sessions/s", "", 200, false},
	} {
		rr := httptest.NewRecorder()
		if handled := l.serveNativePermission(rr, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))); handled != tc.handled || rr.Code != tc.status {
			t.Fatalf("%+v got=%d body=%s", tc, rr.Code, rr.Body.String())
		}
	}
	l.nativeAuto = false
	rr := httptest.NewRecorder()
	l.serveNativePermission(rr, httptest.NewRequest(http.MethodGet, "/sessions/s/native-permission", nil))
	if rr.Code != http.StatusNotImplemented {
		t.Fatal(rr.Code)
	}
}
