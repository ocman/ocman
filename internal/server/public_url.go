package server

import (
	"context"
	"strings"
)

// publicURL turns an absolute in-app path into an absolute URL for a reader
// who is not on this machine. The configured -public-base-url /
// OCMAN_PUBLIC_BASE_URL wins; without it the listen address is the only thing
// left to guess with, which yields a loopback URL that only works locally.
// Callers that hand the link to a remote reader should say so in their docs.
func (s *Server) publicURL(path string) string {
	base := s.publicBaseURL
	if base == "" {
		addr := s.addr
		if addr == "" {
			addr = "localhost:8228"
		}
		if strings.HasPrefix(addr, ":") {
			addr = "localhost" + addr
		}
		base = "http://" + addr
	}
	return strings.TrimRight(base, "/") + path
}

// FileURL returns an origin-relative URL that serves the file at absPath
// (the filesystem module's signed /api/file/ route). Backs the MCP
// embed_file tool.
func (s *Server) FileURL(ctx context.Context, absPath string) (string, error) {
	return s.filesystemModule().FileURL(ctx, absPath)
}
