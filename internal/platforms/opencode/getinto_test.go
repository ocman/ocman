package opencode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetInto(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantOK  bool
		wantVal string
	}{
		{"ok", http.StatusOK, `{"small_model":"anthropic/haiku"}`, true, "anthropic/haiku"},
		{"non-200", http.StatusInternalServerError, `{}`, false, ""},
		{"bad json", http.StatusOK, `{not json`, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

			var out struct {
				SmallModel string `json:"small_model"`
			}
			if ok := getInto(port, "/config", &out); ok != tt.wantOK {
				t.Fatalf("getInto ok = %v, want %v", ok, tt.wantOK)
			}
			if out.SmallModel != tt.wantVal {
				t.Errorf("decoded %q, want %q", out.SmallModel, tt.wantVal)
			}
		})
	}
}

func TestGetInto_Unreachable(t *testing.T) {
	var out map[string]interface{}
	if getInto("1", "/config", &out) { // port 1: nothing listening
		t.Fatal("getInto succeeded against unreachable port")
	}
}

func TestFetchOpenCodeSmallModel(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantProvider string
		wantModel    string
		wantOK       bool
	}{
		{"valid", `{"small_model":"anthropic/claude-haiku"}`, "anthropic", "claude-haiku", true},
		{"no slash", `{"small_model":"haiku"}`, "", "", false},
		{"trailing slash", `{"small_model":"anthropic/"}`, "", "", false},
		{"empty", `{}`, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

			provider, model, ok := fetchOpenCodeSmallModel(port)
			if provider != tt.wantProvider || model != tt.wantModel || ok != tt.wantOK {
				t.Errorf("got (%q, %q, %v), want (%q, %q, %v)",
					provider, model, ok, tt.wantProvider, tt.wantModel, tt.wantOK)
			}
		})
	}
}

func TestFetchOpenCodeMCPAndLSP_FailureReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

	if mcp := fetchOpenCodeMCP(port); mcp == nil || len(mcp) != 0 {
		t.Errorf("fetchOpenCodeMCP on failure = %v, want empty non-nil map", mcp)
	}
	if lsp := fetchOpenCodeLSP(port); lsp == nil || len(lsp) != 0 {
		t.Errorf("fetchOpenCodeLSP on failure = %v, want empty non-nil slice", lsp)
	}
}

func TestFetchOpenCodeMCPAndLSP_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			_, _ = w.Write([]byte(`{"srv1":{}}`))
		case "/lsp":
			_, _ = w.Write([]byte(`[{}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

	if mcp := fetchOpenCodeMCP(port); len(mcp) != 1 {
		t.Errorf("fetchOpenCodeMCP = %v, want 1 entry", mcp)
	}
	if lsp := fetchOpenCodeLSP(port); len(lsp) != 1 {
		t.Errorf("fetchOpenCodeLSP = %v, want 1 entry", lsp)
	}
}
