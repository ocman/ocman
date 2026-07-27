// Package github provides a thin GitHub API client that discovers
// authentication credentials from the local environment.
//
// Token discovery order:
//  1. GITHUB_TOKEN environment variable
//  2. GH_TOKEN environment variable
//  3. gh CLI config (~/.config/gh/hosts.yml, then XDG_CONFIG_HOME/gh/hosts.yml)
//
// When no token is found the client still works for public resources
// (unauthenticated, 60 req/h per IP).
package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// DefaultAPIBase is the canonical GitHub REST API endpoint. Overridable
// per-Client (see apiBase field) for tests against httptest.Server.
const DefaultAPIBase = "https://api.github.com"

// Client is a minimal GitHub REST API client.
type Client struct {
	token string
	http  *http.Client
	// apiBase is the API root URL. Empty means DefaultAPIBase; tests
	// set this to an httptest.Server URL so requests stay in-process.
	apiBase string
}

// New creates a Client and discovers a GitHub token from the environment.
func New() *Client {
	return &Client{
		token: discoverToken(),
		http:  &http.Client{Timeout: 10 * time.Second},
	}
}

// NewForTest returns a Client wired against the given apiBase and
// token. Intended for tests that need to point the client at a
// httptest.Server; not used in production paths.
func NewForTest(apiBase, token string, httpClient *http.Client) *Client {
	return &Client{
		token:   token,
		http:    httpClient,
		apiBase: apiBase,
	}
}

// base returns the effective API base URL, falling back to
// DefaultAPIBase when apiBase is empty.
func (c *Client) base() string {
	if c.apiBase != "" {
		return c.apiBase
	}
	return DefaultAPIBase
}

// Authenticated reports whether a token was found.
func (c *Client) Authenticated() bool { return c.token != "" }

// GetPR fetches pull-request metadata.
func (c *Client) GetPR(owner, repo string, number int) (map[string]interface{}, error) {
	return c.get(fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(repo), number))
}

// GetIssue fetches issue metadata (also works for PRs via the issues endpoint).
func (c *Client) GetIssue(owner, repo string, number int) (map[string]interface{}, error) {
	return c.get(fmt.Sprintf("/repos/%s/%s/issues/%d", url.PathEscape(owner), url.PathEscape(repo), number))
}

// GetCommit fetches commit metadata.
func (c *Client) GetCommit(owner, repo, sha string) (map[string]interface{}, error) {
	return c.get(fmt.Sprintf("/repos/%s/%s/commits/%s", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(sha)))
}

func (c *Client) get(path string) (map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodGet, c.base()+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api %s: %s", path, resp.Status)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Token discovery
// ---------------------------------------------------------------------------

func discoverToken() string {
	// 1. Standard env vars
	for _, env := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if t := os.Getenv(env); t != "" {
			log.WithField("source", env).Debug("github: using token from env")
			return t
		}
	}

	// 2. gh CLI — try `gh auth token` first (handles keychain / 1Password / etc.)
	if t := tokenFromGhCommand(); t != "" {
		log.Debug("github: using token from `gh auth token`")
		return t
	}

	// 3. gh CLI config file (oauth_token stored in plaintext hosts.yml)
	if t := tokenFromGhCLI(); t != "" {
		log.Debug("github: using token from gh CLI hosts.yml")
		return t
	}

	log.Debug("github: no token found, using unauthenticated access")
	return ""
}

// tokenFromGhCommand runs `gh auth token` and returns the trimmed output.
// Returns "" if gh is not installed or not authenticated.
func tokenFromGhCommand() string {
	cmd := exec.Command("gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ghHostsConfig is the minimal structure we need from hosts.yml.
type ghHostsConfig map[string]struct {
	OAuthToken string `yaml:"oauth_token"`
}

func tokenFromGhCLI() string {
	for _, dir := range ghConfigDirs() {
		path := filepath.Join(dir, "gh", "hosts.yml")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg ghHostsConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			log.WithError(err).WithField("path", path).Debug("github: failed to parse gh hosts.yml")
			continue
		}
		if entry, ok := cfg["github.com"]; ok && entry.OAuthToken != "" {
			return entry.OAuthToken
		}
	}
	return ""
}

func ghConfigDirs() []string {
	var dirs []string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		dirs = append(dirs, xdg)
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config"))
	}
	return dirs
}
