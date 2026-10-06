package github

import "net/http"

// NewWithCredentials uses an explicit credential and HTTP client, without discovery.
func NewWithCredentials(apiBase, token string, client *http.Client) *Client {
	return &Client{apiBase: apiBase, token: token, http: client}
}
