package linkpreview

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// Pasted personal tokens identify through the provider API, sent the way
// each provider expects them.
func TestPersonalTokenIdentify(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch {
		case r.URL.Path == "/graphql" && auth == "lin_api_key": // Linear API keys go bare
			fmt.Fprint(w, `{"data":{"viewer":{"name":"Dries"},"organization":{"id":"org-1","name":"Acme"}}}`)
		case r.URL.Path == "/users/me" && auth == "Bearer ntn_secret" && r.Header.Get("Notion-Version") == notionVersion:
			fmt.Fprint(w, `{"name":"ocman","bot":{"workspace_id":"ws-1","workspace_name":"Acme Wiki"}}`)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	linear := LinearOAuth("", "", srv.URL)
	if linear.OAuth() || linear.TokenHelp == "" {
		t.Fatalf("linear without app = %+v", linear)
	}
	g, err := linear.Identify(ctx, srv.Client(), previewauth.Token{AccessToken: "lin_api_key"})
	if err != nil || g[0].WorkspaceID != "org-1" || g[0].AccountName != "Dries" {
		t.Fatalf("linear = %+v %v", g, err)
	}

	notion := NotionOAuth("", "", srv.URL)
	g, err = notion.IdentifyToken(ctx, srv.Client(), previewauth.Token{AccessToken: "ntn_secret"})
	if err != nil || g[0].WorkspaceID != "ws-1" || g[0].WorkspaceName != "Acme Wiki" {
		t.Fatalf("notion = %+v %v", g, err)
	}
	if _, err := notion.IdentifyToken(ctx, srv.Client(), previewauth.Token{AccessToken: "wrong"}); err == nil {
		t.Fatal("rejected notion token identified")
	}
	for _, p := range []previewauth.Provider{GitHubOAuth("", "", ""), ForgejoOAuth("code.example.com", "", ""), GitLabOAuth("gitlab.com", "", "", "", "")} {
		if p.OAuth() || p.TokenHelp == "" {
			t.Fatalf("%s without app = %+v", p.ID, p)
		}
	}
}
