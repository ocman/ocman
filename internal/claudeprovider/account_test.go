package claudeprovider

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// initFixture answers ocman's initialize with response, the spike §5 shape.
func initFixture(t *testing.T, response string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "initialize.jsonl")
	rows := `{"direction":"stdin","message":{"type":"control_request","request":{"subtype":"initialize"}}}
{"direction":"stdout","message":{"type":"system","subtype":"init"}}
{"direction":"stdout","message":{"type":"control_response","response":` + response + `}}
`
	if err := os.WriteFile(path, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const teamAccount = `{"subtype":"success","request_id":"ocman-initialize","response":{"commands":[],"account":{"email":"spike@example.invalid","organization":"Example","subscriptionType":"Claude Team","apiProvider":"firstParty"}}}`

// fakeAccount points an account at the fake claude replaying an initialize
// fixture; every stdin line it receives is logged to the returned path.
func fakeAccount(t *testing.T, response string) (*account, string) {
	t.Helper()
	fakeBinary.once.Do(func() { fakeBinary.path = claudetest.Binary(t) })
	if _, err := os.Stat(fakeBinary.path); err != nil {
		fakeBinary.path = claudetest.Binary(t)
	}
	stdin := filepath.Join(t.TempDir(), "stdin.log")
	t.Setenv("FAKE_CLAUDE_FIXTURE", initFixture(t, response))
	t.Setenv("FAKE_CLAUDE_STDIN", stdin)
	return newAccount(Options{CLI: fakeBinary.path, UsagePath: filepath.Join(t.TempDir(), "claude-usage.json")}), stdin
}

// Ocman never reads, stores or proxies Claude credentials (requirements
// Overview): a stored login and a keychain are ignored, and the plan comes
// from the CLI's initialize response.
func TestAccountNeverReadsCredentials(t *testing.T) {
	config, bin := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(config, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"tok","expiresAt":9999999999999,"subscriptionType":"max"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(bin, "security-called")
	if err := os.WriteFile(filepath.Join(bin, "security"), []byte("#!/bin/sh\ntouch "+marker+"\necho '{\"claudeAiOauth\":{\"accessToken\":\"tok\",\"subscriptionType\":\"max\"}}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("HOME", config)
	a, _ := fakeAccount(t, teamAccount)
	got, err := a.get(t.Context())
	if err != nil || got.SubscriptionType != "Claude Team" {
		t.Fatalf("account = %+v, %v; want the CLI's initialize account", got, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the macOS keychain was queried")
	}

	// No code path names a credential store or calls the usage API with a token.
	sources, _ := filepath.Glob("*.go")
	root, _ := filepath.Glob("../../claude_*.go")
	for _, path := range append(sources, root...) {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data := string(must(os.ReadFile(path)))
		for _, banned := range []string{".credentials.json", "Claude Code-credentials", "find-generic-password", "oauth/usage", "accessToken", "Authorization"} {
			if strings.Contains(data, banned) {
				t.Errorf("%s references %q", path, banned)
			}
		}
	}
}

func TestAccountPlanFromInitializeIsCached(t *testing.T) {
	a, stdin := fakeAccount(t, teamAccount)
	rec := httptest.NewRecorder()
	(&handler{account: a}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/account", nil))
	var got providerprotocol.Account
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.SubscriptionType != "Claude Team" || got.RateLimits == nil || len(got.RateLimits) != 0 {
		t.Fatalf("GET /account = %d %s", rec.Code, rec.Body)
	}
	var sent struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype       string `json:"subtype"`
			SDKMcpServers []any  `json:"sdkMcpServers"`
		} `json:"request"`
	}
	lines := strings.Split(strings.TrimSpace(string(must(os.ReadFile(stdin)))), "\n")
	if len(lines) != 1 || json.Unmarshal([]byte(lines[0]), &sent) != nil || sent.Type != "control_request" ||
		sent.RequestID != initRequestID || sent.Request.Subtype != "initialize" || sent.Request.SDKMcpServers == nil {
		t.Fatalf("stdin = %q; want one initialize request and no user message", lines)
	}
	if _, err := a.get(t.Context()); err != nil || len(strings.Split(strings.TrimSpace(string(must(os.ReadFile(stdin)))), "\n")) != 1 {
		t.Fatalf("second read ran the CLI again: %v", err)
	}
}

func TestAccountSignedOutAndCLIFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		response string
		code     int
	}{
		"no account":    {`{"subtype":"success","request_id":"ocman-initialize","response":{"commands":[]}}`, http.StatusNotFound},
		"empty account": {`{"subtype":"success","request_id":"ocman-initialize","response":{"account":{}}}`, http.StatusNotFound},
		"error":         {`{"subtype":"error","request_id":"ocman-initialize","error":"boom"}`, http.StatusInternalServerError},
		"other request": {`{"subtype":"success","request_id":"other","response":{"account":{"subscriptionType":"pro"}}}`, http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			a, _ := fakeAccount(t, tc.response)
			rec := httptest.NewRecorder()
			(&handler{account: a}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/account", nil))
			if rec.Code != tc.code {
				t.Fatalf("GET /account = %d %s, want %d", rec.Code, rec.Body, tc.code)
			}
		})
	}
	a := newAccount(Options{CLI: filepath.Join(t.TempDir(), "missing")})
	if _, err := a.get(t.Context()); err == nil || errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("missing CLI = %v, want an error that is not signed-out", err)
	}
}

// Windows come only from rate_limit_event observations, shared by every
// supervisor through UsagePath and dropped once their reset passes.
func TestAccountReportsObservedWindows(t *testing.T) {
	a, _ := fakeAccount(t, teamAccount)
	other := newAccount(Options{UsagePath: a.usagePath}) // another supervisor
	future := time.Now().Add(time.Hour).Unix()
	for _, info := range []string{
		`{"status":"allowed_warning","resetsAt":` + itoa(future+3600) + `,"rateLimitType":"seven_day","utilization":0.76,"isUsingOverage":false,"surpassedThreshold":0.75}`,
		`{"status":"allowed","resetsAt":` + itoa(future) + `,"rateLimitType":"five_hour"}`, // no utilisation: skipped
		`{"status":"allowed_warning","resetsAt":` + itoa(future) + `,"rateLimitType":"seven_day_opus","utilization":0.9}`,
		`{"status":"rejected","resetsAt":` + itoa(future) + `,"rateLimitType":"five_hour"}`,
		`not json`,
	} {
		other.observe(json.RawMessage(info))
	}
	got, err := a.get(t.Context())
	want := []providerprotocol.RateLimit{{Name: "five_hour", Utilization: 1, ResetsAt: future * 1000}, {Name: "weekly", Utilization: 0.76, ResetsAt: (future + 3600) * 1000}}
	if err != nil || len(got.RateLimits) != 2 || got.RateLimits[0] != want[0] || got.RateLimits[1] != want[1] {
		t.Fatalf("account = %+v, %v", got, err)
	}
	other.observe(json.RawMessage(`{"status":"allowed_warning","resetsAt":1,"rateLimitType":"five_hour","utilization":0.5}`))
	if got, _ := a.get(t.Context()); len(got.RateLimits) != 1 || got.RateLimits[0].Name != "weekly" {
		t.Fatalf("a reset window is still reported: %+v", got.RateLimits)
	}
}

// A driven session's rate_limit_event reaches the shared usage file.
func TestDrivenSessionRecordsRateLimit(t *testing.T) {
	var usage string
	h := startLive(t, claudetest.Fixture("plain.jsonl"), func(o *Options) {
		usage = filepath.Join(t.TempDir(), "claude-usage.json")
		o.UsagePath = usage
	})
	id := h.create(t)
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn idle", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
	got := newAccount(Options{UsagePath: usage, ConfigDir: h.live.opts.ConfigDir}).update(func(*usageScope) {})["weekly"]
	if got != (providerprotocol.RateLimit{Name: "weekly", Utilization: 0.76, ResetsAt: 1791360000000}) {
		t.Fatalf("recorded weekly window = %+v", got)
	}
}

// Observed windows belong to the Claude configuration they were observed
// under: switching CLAUDE_CONFIG_DIR, or logging another account in to the
// same configuration, never shows the previous account's usage.
func TestUsageWindowsScopedToConfiguration(t *testing.T) {
	a, _ := fakeAccount(t, teamAccount)
	a.config = "/config/a"
	future := itoa(time.Now().Add(time.Hour).Unix())
	observer := newAccount(Options{UsagePath: a.usagePath, ConfigDir: "/config/a"})
	observer.observe(json.RawMessage(`{"status":"allowed_warning","resetsAt":` + future + `,"rateLimitType":"five_hour","utilization":0.5}`))
	if got, err := a.get(t.Context()); err != nil || len(got.RateLimits) != 1 {
		t.Fatalf("own configuration = %+v %v", got, err)
	}
	switched := newAccount(Options{CLI: a.cli, UsagePath: a.usagePath, ConfigDir: "/config/b"})
	if got, err := switched.get(t.Context()); err != nil || len(got.RateLimits) != 0 {
		t.Fatalf("another configuration saw the windows: %+v %v", got, err)
	}
	// Same configuration, another login: the windows are dropped.
	t.Setenv("FAKE_CLAUDE_FIXTURE", initFixture(t, strings.Replace(teamAccount, "spike@example.invalid", "other@example.invalid", 1)))
	relogged := newAccount(Options{CLI: a.cli, UsagePath: a.usagePath, ConfigDir: "/config/a"})
	if got, err := relogged.get(t.Context()); err != nil || len(got.RateLimits) != 0 {
		t.Fatalf("another login saw the windows: %+v %v", got, err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
