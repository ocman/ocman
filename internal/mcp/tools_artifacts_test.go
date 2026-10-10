package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/mark3labs/mcp-go/mcptest"
)

type fakeArtifacts struct {
	created internalmcp.ArtifactCreateRequest
	filter  state.ArtifactFilter
	err     error
}

func (f *fakeArtifacts) CreateArtifact(_ context.Context, in internalmcp.ArtifactCreateRequest) (state.Artifact, error) {
	f.created = in
	if f.err != nil {
		return state.Artifact{}, f.err
	}
	return state.Artifact{ID: "art1", Title: in.Title, Items: []state.ArtifactItem{
		{Kind: state.ArtifactItemFile, Name: "shot.png", MIME: "image/png", URL: "http://o/api/artifacts/art1/files/0"},
		{Kind: state.ArtifactItemFile, Name: "notes.md", MIME: "text/markdown", URL: "http://o/api/artifacts/art1/files/1"},
		{Kind: state.ArtifactItemLink, URL: "https://example.com", Label: "PR"},
	}}, nil
}

func (f *fakeArtifacts) ListArtifacts(_ context.Context, filter state.ArtifactFilter) ([]state.Artifact, string, error) {
	f.filter = filter
	return nil, "next", f.err
}

func (f *fakeArtifacts) GetArtifact(_ context.Context, id string) (state.Artifact, error) {
	if id != "art1" {
		return state.Artifact{}, state.ErrArtifactNotFound
	}
	return state.Artifact{ID: id, Title: "T"}, f.err
}

func (*fakeArtifacts) ArtifactPageURL(id string) string { return "http://o/artifacts/" + id }

func artifactTestServer(t *testing.T, svc *fakeArtifacts) *mcptest.Server {
	t.Helper()
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{ArtifactService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv
}

func TestArtifactToolDisabledWithoutService(t *testing.T) {
	for _, tool := range internalmcp.ServerTools(internalmcp.Deps{}) {
		if tool.Tool.Name == "artifacts" {
			t.Fatal("artifacts tool registered without a service")
		}
	}
}

func TestArtifactToolHelpAndValidation(t *testing.T) {
	srv := artifactTestServer(t, &fakeArtifacts{})
	help := resultText(callTool(t, srv, "artifacts", map[string]any{"action": "help"}))
	for _, want := range []string{"create", "list", "get", "output_schema", "permalink", "items[].url", "original folders", "origin", "metadata"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q: %s", want, help)
		}
	}
	if strings.Contains(help, `"delete"`) {
		t.Fatal("help exposes delete")
	}
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{}, "action is required"},
		{map[string]any{"action": "delete"}, "unknown action"},
		{map[string]any{"action": "create", "title": "t"}, "directory is required"},
		{map[string]any{"action": "create", "directory": "/repo", "title": " "}, "title is required"},
		{map[string]any{"action": "create", "directory": "/repo", "title": "t", "platform": "opencode"}, "platform and session_id must be provided together"},
		{map[string]any{"action": "create", "directory": "/repo", "title": "t", "files": "nope"}, "files must be an array of objects"},
		{map[string]any{"action": "create", "directory": "/repo", "title": "t", "links": []any{1}}, "links must be an array of objects"},
		{map[string]any{"action": "get"}, "artifact_id is required"},
		{map[string]any{"action": "get", "artifact_id": "missing"}, "artifact not found"},
		{map[string]any{"action": "list", "limit": 0}, "limit must be between 1 and 200"},
		{map[string]any{"action": "list", "session_id": "ses"}, "platform and session_id must be provided together"},
	} {
		result := callTool(t, srv, "artifacts", tc.args)
		if !result.IsError || resultText(result) != tc.want {
			t.Errorf("args %#v: %q, want %q", tc.args, resultText(result), tc.want)
		}
	}
}

func TestArtifactToolCreate(t *testing.T) {
	svc := &fakeArtifacts{}
	srv := artifactTestServer(t, svc)
	result := callTool(t, srv, "artifacts", map[string]any{
		"action": "create", "directory": "/repo", "platform": "opencode", "session_id": "ses1", "title": " Report ", "description": "d",
		"files": []any{map[string]any{"path": "/repo/shot.png"}, map[string]any{"name": "notes.md", "content": "# hi", "mime": "text/markdown"}},
		"links": []any{map[string]any{"url": "https://example.com", "label": "PR"}},
	})
	if result.IsError {
		t.Fatal(resultText(result))
	}
	want := internalmcp.ArtifactCreateRequest{Title: "Report", Description: "d", Directory: "/repo", Platform: "opencode", SessionID: "ses1",
		Files: []internalmcp.ArtifactFile{{Path: "/repo/shot.png"}, {Name: "notes.md", Content: "# hi", MIME: "text/markdown"}},
		Links: []internalmcp.ArtifactLink{{URL: "https://example.com", Label: "PR"}}}
	if fmt.Sprint(svc.created) != fmt.Sprint(want) {
		t.Fatalf("request = %#v", svc.created)
	}
	var out struct {
		ID, URL, Markdown string
		Items             []struct{ Kind, URL string }
	}
	if err := json.Unmarshal([]byte(resultText(result)), &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "art1" || out.URL != "http://o/artifacts/art1" || len(out.Items) != 3 || out.Items[0].URL != "http://o/api/artifacts/art1/files/0" {
		t.Fatalf("out = %#v", out)
	}
	for _, line := range []string{"[Report](http://o/artifacts/art1)", "![shot.png](http://o/api/artifacts/art1/files/0)", "- [notes.md](http://o/api/artifacts/art1/files/1)", "- [PR](https://example.com)"} {
		if !strings.Contains(out.Markdown, line) {
			t.Errorf("markdown missing %q: %s", line, out.Markdown)
		}
	}

	svc.err = fmt.Errorf("%w: at least one file or link is required", state.ErrArtifactInvalid)
	if r := callTool(t, srv, "artifacts", map[string]any{"action": "create", "directory": "/repo", "title": "t"}); !r.IsError || resultText(r) != svc.err.Error() {
		t.Fatalf("invalid = %q", resultText(r))
	}
	svc.err = errors.New("disk on fire")
	if r := callTool(t, srv, "artifacts", map[string]any{"action": "create", "directory": "/repo", "title": "t"}); resultText(r) != "artifact request failed" {
		t.Fatalf("internal error leaked: %q", resultText(r))
	}
}

func TestArtifactToolListAndGet(t *testing.T) {
	svc := &fakeArtifacts{}
	srv := artifactTestServer(t, svc)
	result := callTool(t, srv, "artifacts", map[string]any{"action": "list", "directory": "/repo", "platform": "opencode", "session_id": "ses1", "cursor": "c", "limit": 5})
	if result.IsError || !strings.Contains(resultText(result), `"artifacts": []`) || !strings.Contains(resultText(result), `"next_cursor": "next"`) {
		t.Fatalf("list = %s", resultText(result))
	}
	if f := svc.filter; f.Directory != "/repo" || f.Platform != "opencode" || len(f.SessionIDs) != 1 || f.SessionIDs[0] != "ses1" || f.Cursor != "c" || f.Limit != 5 {
		t.Fatalf("filter = %#v", f)
	}
	got := resultText(callTool(t, srv, "artifacts", map[string]any{"action": "get", "artifact_id": "art1"}))
	if !strings.Contains(got, `"url": "http://o/artifacts/art1"`) || !strings.Contains(got, `"title": "T"`) {
		t.Fatalf("get = %s", got)
	}
	svc.err = errors.New("boom")
	if r := callTool(t, srv, "artifacts", map[string]any{"action": "list"}); resultText(r) != "artifact request failed" {
		t.Fatalf("list error = %q", resultText(r))
	}
}
