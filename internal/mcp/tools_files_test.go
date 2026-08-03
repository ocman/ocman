package mcp_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
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
	res := callTool(t, srv, "embed_file", args)
	if res.IsError {
		t.Fatalf("embed_file returned an error: %s", resultText(res))
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		t.Fatalf("decoding result %q: %v", resultText(res), err)
	}
	return out
}

func TestEmbedFile_ImageRendersAsMarkdownEmbed(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "chart.png")
	if err := os.WriteFile(png, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := buildEmbedFileServer(t, func(p string) (string, error) {
		return "http://localhost:8228/api/file/tok-for-" + filepath.Base(p), nil
	})

	out := embedFileResult(t, srv, map[string]interface{}{"path": png})

	if out["url"] != "http://localhost:8228/api/file/tok-for-chart.png" {
		t.Errorf("url = %v", out["url"])
	}
	if out["name"] != "chart.png" {
		t.Errorf("name = %v", out["name"])
	}
	if out["size"] != float64(5) {
		t.Errorf("size = %v, want 5", out["size"])
	}
	want := "![chart.png](http://localhost:8228/api/file/tok-for-chart.png)"
	if out["markdown"] != want {
		t.Errorf("markdown = %v, want %q", out["markdown"], want)
	}
}

func TestEmbedFile_NonImageRendersAsLinkWithLabel(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := buildEmbedFileServer(t, func(string) (string, error) { return "http://x/api/file/t", nil })

	out := embedFileResult(t, srv, map[string]interface{}{"path": pdf, "label": "Q3 report"})

	if out["markdown"] != "[Q3 report](http://x/api/file/t)" {
		t.Errorf("markdown = %v", out["markdown"])
	}
}

func TestEmbedFile_SVGEmbedsInline(t *testing.T) {
	dir := t.TempDir()
	svg := filepath.Join(dir, "diagram.svg")
	if err := os.WriteFile(svg, []byte("<svg/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := buildEmbedFileServer(t, func(string) (string, error) { return "http://x/api/file/t", nil })

	out := embedFileResult(t, srv, map[string]interface{}{"path": svg})
	if !strings.HasPrefix(out["markdown"].(string), "![") {
		t.Errorf("svg should embed inline, got %v", out["markdown"])
	}
}

func TestEmbedFile_Errors(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "ok.png")
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	okSigner := internalmcp.FileSigner(func(string) (string, error) { return "http://x/api/file/t", nil })

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
		{"signer fails", func(string) (string, error) { return "", errors.New("boom") }, map[string]interface{}{"path": existing}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := buildEmbedFileServer(t, tt.sign)
			res := callTool(t, srv, "embed_file", tt.args)
			if !res.IsError {
				t.Errorf("expected an error result, got %s", resultText(res))
			}
		})
	}
}
