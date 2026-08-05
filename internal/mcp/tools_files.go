package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// FileSigner mints a browser-reachable URL for an absolute path. The
// server package supplies it (Server.FileURL); the MCP package stays
// unaware of tokens, keys and routes.
type FileSigner func(absPath string) (string, error)

// fileTools holds the dependencies for the asset-embedding tool.
type fileTools struct {
	sign FileSigner
}

// embedFileTool returns the tool definition for embed_file.
func embedFileTool() mcplib.Tool {
	return mcplib.NewTool("embed_file",
		mcplib.WithDescription(
			"Show a file from disk inside the conversation. Returns a URL plus a "+
				"ready-to-paste markdown snippet — include that snippet in your reply "+
				"so the user can actually see it. Images and SVGs render inline in the "+
				"conversation; PDFs and other types become a link the browser opens or "+
				"downloads. Use this whenever you generate or want to show a picture, "+
				"diagram, SVG, PDF, chart or other non-text artifact — the user cannot "+
				"open a file path, only what you embed."),
		mcplib.WithString("path",
			mcplib.Required(),
			mcplib.Description("Absolute path to the file."),
		),
		mcplib.WithString("label",
			mcplib.Description("Optional link/alt text. Defaults to the file name."),
		),
	)
}

// addFileTools registers the asset-embedding tools on the MCP server.
func addFileTools(s *server.MCPServer, t *fileTools) {
	s.AddTool(embedFileTool(), t.handleEmbedFile)
}

// handleEmbedFile handles the embed_file tool call.
func (t *fileTools) handleEmbedFile(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcplib.NewToolResultError("path is required"), nil
	}
	if t.sign == nil {
		return mcplib.NewToolResultError("file embedding is unavailable"), nil
	}
	if !filepath.IsAbs(path) {
		return mcplib.NewToolResultError("path must be absolute"), nil
	}
	path = filepath.Clean(path)

	info, err := os.Stat(path)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("cannot read %s: %v", path, err)), nil
	}
	if !info.Mode().IsRegular() {
		return mcplib.NewToolResultError(fmt.Sprintf("%s is not a regular file", path)), nil
	}

	url, err := t.sign(path)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("building file URL: %v", err)), nil
	}

	name := filepath.Base(path)
	label := req.GetString("label", "")
	if label == "" {
		label = name
	}

	return toolResultJSON(map[string]interface{}{
		"url":      url,
		"name":     name,
		"size":     info.Size(),
		"markdown": fileMarkdown(label, url, name),
		"hint":     "Include the markdown value verbatim in your reply to the user.",
	}), nil
}

// fileMarkdown renders an image embed for renderable types and a plain
// link for everything else.
func fileMarkdown(label, url, name string) string {
	if isInlineImage(name) {
		return "![" + label + "](" + url + ")"
	}
	return "[" + label + "](" + url + ")"
}

// isInlineImage reports whether a file name has an extension browsers
// render inside an <img> tag.
func isInlineImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".avif", ".bmp", ".ico":
		return true
	}
	return false
}
