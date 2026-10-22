package server

import (
	"context"
	"net/url"
)

func (s *Server) publicSessionURL(_ context.Context, platform, id string) (string, error) {
	return s.publicURL("/session/" + url.PathEscape(id) + "?platform=" + url.QueryEscape(platform)), nil
}
