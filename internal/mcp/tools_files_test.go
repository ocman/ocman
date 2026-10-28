package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"

	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/testutil"
)

func buildEmbedFileServer(t *testing.T, sign internalmcp.FileSigner) *mcptest.Server {
	t.Helper()
	tools := internalmcp.ServerTools(internalmcp.Deps{SignFile: sign})
	srv, err := mcptest.NewServer(t, tools...)
	if err != nil {
		t.Fatalf("mcptest.NewServer: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

func embedFileResult(t *testing.T, srv *mcptest.Server, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	res := testutil.CallTool(t, srv, "embed_file", args)
	if res.IsError {
		t.Fatalf("embed_file returned an error: %s", testutil.ResultText(res))
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(testutil.ResultText(res)), &out); err != nil {
		t.Fatalf("decoding result %q: %v", testutil.ResultText(res), err)
	}
	return out
}

func TestEmbedFile_Success(t *testing.T) {
	tests := []struct {
		file, content, label, wantMarkdown string
	}{
		{"chart.png", "12345", "", "![chart.png](http://x/api/file/tok-for-chart.png)"},
		{"report.pdf", "%PDF-1.4", "Q3 report", "[Q3 report](http://x/api/file/tok-for-report.pdf)"},
		{"diagram.svg", "<svg/>", "", "![diagram.svg](http://x/api/file/tok-for-diagram.svg)"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.file)
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			srv := buildEmbedFileServer(t, func(_ context.Context, p string) (string, error) {
				return "http://x/api/file/tok-for-" + filepath.Base(p), nil
			})
			args := map[string]interface{}{"path": path}
			if tt.label != "" {
				args["label"] = tt.label
			}

			out := embedFileResult(t, srv, args)

			if want := "http://x/api/file/tok-for-" + tt.file; out["url"] != want {
				t.Errorf("url = %v, want %q", out["url"], want)
			}
			if out["name"] != tt.file {
				t.Errorf("name = %v, want %q", out["name"], tt.file)
			}
			if out["size"] != float64(len(tt.content)) {
				t.Errorf("size = %v, want %d", out["size"], len(tt.content))
			}
			if out["markdown"] != tt.wantMarkdown {
				t.Errorf("markdown = %v, want %q", out["markdown"], tt.wantMarkdown)
			}
		})
	}
}

func TestEmbedFile_Errors(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "ok.png")
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	okSigner := internalmcp.FileSigner(func(context.Context, string) (string, error) { return "http://x/api/file/t", nil })

	tests := []struct {
		name string
		sign internalmcp.FileSigner
		args map[string]interface{}
	}{
		{"missing path", okSigner, map[string]interface{}{}},
		{"relative path", okSigner, map[string]interface{}{"path": "docs/chart.png"}},
		{"nonexistent file", okSigner, map[string]interface{}{"path": filepath.Join(dir, "nope.png")}},
		{"directory", okSigner, map[string]interface{}{"path": dir}},
		{"no signer", nil, map[string]interface{}{"path": existing}},
		{"signer fails", func(context.Context, string) (string, error) { return "", errors.New("boom") }, map[string]interface{}{"path": existing}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := buildEmbedFileServer(t, tt.sign)
			res := testutil.CallTool(t, srv, "embed_file", tt.args)
			if !res.IsError {
				t.Errorf("expected an error result, got %s", testutil.ResultText(res))
			}
		})
	}
}
