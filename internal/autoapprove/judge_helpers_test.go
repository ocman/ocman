package autoapprove

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocapi"
)

func TestPermissionJudgeAuthenticatesRequests(t *testing.T) {
	const password = "judge-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"info":{"role":"user"},"parts":[{"type":"text","text":"hello"}]}]`))
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)

	j := newPermissionJudge(ocapi.New(password))
	if got := j.recentUserMessages(context.Background(), u.Port(), "session"); got == nil {
		t.Fatal("authenticated judge request failed")
	}
}

// TestNewPermissionJudgeDefaults verifies the constructor wires the
// built-in model + a non-nil HTTP client and port-discovery func.
func TestNewPermissionJudgeDefaults(t *testing.T) {
	j := newPermissionJudge(ocapi.New(""))
	if j.modelProvider != judgeModelProvider || j.modelID != judgeModelID {
		t.Errorf("model = %q/%q, want %q/%q", j.modelProvider, j.modelID, judgeModelProvider, judgeModelID)
	}
	if j.httpClient == nil {
		t.Error("httpClient must be non-nil")
	}
	if j.openCodePort == nil {
		t.Error("openCodePort discovery func must be non-nil")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"", "b", "c"}, "b"},
		{[]string{"a", "b"}, "a"},
		{[]string{"", ""}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := firstNonEmpty(c.in...); got != c.want {
			t.Errorf("firstNonEmpty(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractTextFromParts(t *testing.T) {
	// Concatenates text parts, skips non-text and malformed entries.
	msg := map[string]interface{}{
		"parts": []interface{}{
			map[string]interface{}{"type": "text", "text": "hello "},
			map[string]interface{}{"type": "tool", "text": "ignored"},
			map[string]interface{}{"type": "text", "text": "world"},
			"not-a-map",
		},
	}
	if got := extractTextFromParts(msg); got != "hello world" {
		t.Errorf("extractTextFromParts = %q, want %q", got, "hello world")
	}
	// No parts → empty string.
	if got := extractTextFromParts(map[string]interface{}{}); got != "" {
		t.Errorf("extractTextFromParts(empty) = %q, want empty", got)
	}
}

// TestRecentUserMessages drives the message fetch against a fake
// OpenCode /message endpoint: only user-role text is returned, capped
// at the recent limit, and a non-2xx response yields nil.
func TestRecentUserMessages(t *testing.T) {
	body := `[
		{"info":{"role":"user"},"parts":[{"type":"text","text":"first"}]},
		{"info":{"role":"assistant"},"parts":[{"type":"text","text":"skip me"}]},
		{"info":{"role":"user"},"parts":[{"type":"text","text":"second"}]}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/message") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

	j := newPermissionJudge(ocapi.New(""))
	got := j.recentUserMessages(context.Background(), port, "ses-1")
	want := []string{"first", "second"}
	if len(got) != len(want) {
		t.Fatalf("recentUserMessages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("msg[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// A server error must yield nil (caller proceeds without context).
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errSrv.Close()
	errPort := strings.TrimPrefix(errSrv.URL, "http://127.0.0.1:")
	if got := j.recentUserMessages(context.Background(), errPort, "ses-1"); got != nil {
		t.Errorf("recentUserMessages on error = %v, want nil", got)
	}
}
