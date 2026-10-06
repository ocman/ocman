package forgejo

import (
	"net/http"
	"strings"
)

// NewWithCredentials uses an explicit credential and HTTP client, without discovery.
func NewWithCredentials(host, baseURL, token string, client *http.Client) *Client {
	return &Client{host: host, baseURL: strings.TrimRight(baseURL, "/"), token: token, http: client}
}
