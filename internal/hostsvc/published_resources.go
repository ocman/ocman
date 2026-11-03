package hostsvc

import (
	"net/http"
	"regexp"
)

// PublishedResources is an optional Host seam for owner-local artifact reads
// and signed embeds. It does not expose arbitrary HTTP routes or filesystem paths.
type PublishedResources interface {
	ServePublishedResource(http.ResponseWriter, *http.Request) error
}

var publishedReadPath = regexp.MustCompile(`^/api/(file/[^/]+|artifacts/?|artifacts/[^/]+|artifacts/[^/]+/files/[0-9]+(/interactive)?)$`)

func IsPublishedResource(method, path string) bool {
	return ((method == http.MethodGet || method == http.MethodHead) && publishedReadPath.MatchString(path)) ||
		(method == http.MethodPost && path == "/api/files/refresh")
}

// PublishedResourceRequest carries only the headers needed for conditional and
// ranged reads. Authentication stays on the gRPC transport, never in the URL.
type PublishedResourceRequest struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

type PublishedResourceResponse struct {
	Status int
	Header http.Header
}
