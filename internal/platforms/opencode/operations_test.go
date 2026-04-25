package opencode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestParseOpenCodeModelRef(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantNil      bool
		wantProvider string
		wantModel    string
	}{
		{"empty", "", true, "", ""},
		{"whitespace only", "   ", true, "", ""},
		{"model only", "gpt-4", false, "", "gpt-4"},
		{"provider/model", "openai/gpt-4", false, "openai", "gpt-4"},
		{"with spaces", "  openai / gpt-4  ", false, "openai", "gpt-4"},
		{"empty provider", "/gpt-4", false, "", "/gpt-4"},
		{"empty model", "openai/", false, "", "openai/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseOpenCodeModelRefInternal(tt.input)
			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.ProviderID != tt.wantProvider {
				t.Errorf("ProviderID = %q, want %q", result.ProviderID, tt.wantProvider)
			}
			if result.ModelID != tt.wantModel {
				t.Errorf("ModelID = %q, want %q", result.ModelID, tt.wantModel)
			}
		})
	}
}

// TestCreateSession_NoRunningInstanceReturnsUnreachable ensures that
// when no OpenCode process is listening for the given directory, the
// adapter returns an error wrapping platforms.ErrPlatformUnreachable
// (which the HTTP layer maps to 503 and the frontend uses to trigger
// the auto-launch flow).
func TestCreateSession_NoRunningInstanceReturnsUnreachable(t *testing.T) {
	a := &Adapter{}
	// A uniquely-named directory that no opencode process will have
	// bound to. lsof will simply not report it, so discovery returns
	// an empty port string.
	_, err := a.CreateSession(context.Background(), platforms.CreateSessionRequest{
		Directory: "/tmp/ocman-test-nonexistent-directory-no-opencode-abc123xyz",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Errorf("error does not wrap ErrPlatformUnreachable: %v", err)
	}
}

// TestExtractOpenCodeErrorMessage covers the parsing of OpenCode's
// NamedError JSON response into a UI-friendly string. This is the
// data we surface to users when SendMessage hits e.g. an unknown
// model and OpenCode returns ProviderModelNotFoundError.
func TestExtractOpenCodeErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", ""},
		{"whitespace", "   \n", ""},
		{"non-json passthrough", "boom", "boom"},
		{
			"named error with message field",
			`{"name":"ProviderModelNotFoundError","data":{"message":"Model anthropic/foo not found"}}`,
			"Model anthropic/foo not found",
		},
		{
			"named error without message uses name + data",
			`{"name":"ProviderModelNotFoundError","data":{"providerID":"anthropic","modelID":"foo"}}`,
			"ProviderModelNotFoundError: modelID=foo, providerID=anthropic",
		},
		{
			"named error with empty data",
			`{"name":"BadRequest","data":{}}`,
			"BadRequest",
		},
		{
			"json without name returns raw body",
			`{"foo":"bar"}`,
			`{"foo":"bar"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOpenCodeErrorMessage([]byte(tt.body))
			if got != tt.want {
				t.Errorf("extractOpenCodeErrorMessage(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// TestSendJSON_4xxReturnsUpstreamError ensures that a 4xx upstream
// response is converted into a typed *platforms.UpstreamError so the
// HTTP layer can map it to 422 and forward the parsed message to the
// UI. Regression guard for the "ocman silently swallows
// ProviderModelNotFoundError" bug.
func TestSendJSON_4xxReturnsUpstreamError(t *testing.T) {
	// httptest server stands in for an OpenCode instance: returns
	// the canonical NamedError shape on POST.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"name":"ProviderModelNotFoundError","data":{"providerID":"anthropic","modelID":"foo"}}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	err := sendJSON(context.Background(), http.MethodPost, port, "/session/x/prompt_async", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, platforms.ErrUpstreamRejected) {
		t.Errorf("error does not wrap ErrUpstreamRejected: %v", err)
	}
	var ue *platforms.UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error is not *UpstreamError: %v", err)
	}
	if ue.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", ue.Status)
	}
	if !strings.Contains(ue.Message, "ProviderModelNotFoundError") {
		t.Errorf("message missing error name, got %q", ue.Message)
	}
}

// TestSendJSON_5xxFallsThroughToGenericError ensures a 5xx response
// is *not* tagged as ErrUpstreamRejected — those land in the
// "platform unreachable / unknown" bucket because they typically
// indicate a server-side bug rather than user input we can fix.
func TestSendJSON_5xxFallsThroughToGenericError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	err := sendJSON(context.Background(), http.MethodPost, port, "/x", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, platforms.ErrUpstreamRejected) {
		t.Errorf("5xx must not wrap ErrUpstreamRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error missing status, got %v", err)
	}
}


