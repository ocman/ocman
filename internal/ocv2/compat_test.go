package ocv2

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------
// fake OpenCode v2 server
// ---------------------------------------------------------------------

type fakeCall struct {
	Method string
	Path   string
	Query  url.Values
	Body   any
}

func (c fakeCall) key() string { return c.Method + " " + c.Path }

type fakeHandler func(c fakeCall) (int, any)

type fakeV2 struct {
	t      *testing.T
	srv    *httptest.Server
	URL    string
	mu     sync.Mutex
	calls  []fakeCall
	routes map[string]fakeHandler
	notV2  bool     // /api/info answers 404 (a v1 server)
	events []string // SSE frames for /api/event; ":..." is a comment
}

func fakeServer(t *testing.T) *fakeV2 {
	t.Helper()
	f := &fakeV2{t: t, routes: map[string]fakeHandler{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	f.URL = f.srv.URL
	host := f.srv.Listener.Addr().String()
	ForgetHost(host)
	t.Cleanup(func() {
		f.srv.Close()
		ForgetHost(host)
	})
	return f
}

// on registers a handler for "METHOD /path".
func (f *fakeV2) on(key string, h fakeHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[key] = h
}

// json registers a canned raw-JSON answer for "METHOD /path".
func (f *fakeV2) json(key string, status int, raw string) {
	f.on(key, func(fakeCall) (int, any) { return status, json.RawMessage(raw) })
}

func (f *fakeV2) serve(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var body any
	if len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, &body); err != nil {
			body = string(b)
		}
	}
	c := fakeCall{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: body}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	h := f.routes[c.key()]
	notV2, events := f.notV2, f.events
	f.mu.Unlock()
	if h != nil {
		status, v := h(c)
		fakeWrite(w, status, v)
		return
	}
	switch {
	case c.key() == "GET /api/info" && !notV2:
		fakeWrite(w, http.StatusOK, map[string]any{"version": "2.0.22"})
	case c.key() == "GET /api/event":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for _, e := range events {
			if strings.HasPrefix(e, ":") {
				fmt.Fprintf(w, "%s\n\n", e)
			} else {
				fmt.Fprintf(w, "data: %s\n\n", e)
			}
			if fl != nil {
				fl.Flush()
			}
		}
	default:
		fakeWrite(w, http.StatusNotFound, map[string]any{"name": "NotFound"})
	}
}

func fakeWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if raw, ok := v.(json.RawMessage); ok {
		w.Write(raw)
		return
	}
	b, _ := json.Marshal(v)
	w.Write(b)
}

// v2Calls returns every recorded call except the /api/info probe.
func (f *fakeV2) v2Calls() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.Path != "/api/info" {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeV2) keys() []string {
	var out []string
	for _, c := range f.v2Calls() {
		out = append(out, c.key())
	}
	return out
}

func (f *fakeV2) find(key string) []fakeCall {
	var out []fakeCall
	for _, c := range f.v2Calls() {
		if c.key() == key {
			out = append(out, c)
		}
	}
	return out
}

// one returns the single call recorded for key.
func (f *fakeV2) one(key string) fakeCall {
	f.t.Helper()
	got := f.find(key)
	if len(got) != 1 {
		f.t.Fatalf("calls to %s = %d, want 1 (all: %v)", key, len(got), f.keys())
	}
	return got[0]
}

// ---------------------------------------------------------------------
// client helpers
// ---------------------------------------------------------------------

func fakeClient() *http.Client { return &http.Client{Transport: Wrap(http.DefaultTransport)} }

// cmpDo sends one request through Wrap and returns status and body.
func cmpDo(t *testing.T, method, u string, header map[string]string, body string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := fakeClient().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

// cmpOK sends a request expecting 200 and decodes the JSON body.
func cmpOK(t *testing.T, method, u, body string) any {
	t.Helper()
	status, b := cmpDo(t, method, u, nil, body)
	if status != http.StatusOK {
		t.Fatalf("%s %s: status %d body %s", method, u, status, b)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s %s: decode %q: %v", method, u, b, err)
	}
	return v
}

// cmpJSON asserts got equals the JSON document want.
func cmpJSON(t *testing.T, what string, got any, want string) {
	t.Helper()
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	_ = json.Unmarshal(b, &g)
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad want JSON %q: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s:\n got  %s\n want %s", what, b, want)
	}
}

func cmpKeys(t *testing.T, f *fakeV2, want ...string) {
	t.Helper()
	if got := f.keys(); !reflect.DeepEqual(got, want) {
		t.Errorf("v2 calls:\n got  %q\n want %q", got, want)
	}
}

func cmpLoc(t *testing.T, c fakeCall, dir string) {
	t.Helper()
	if got := c.Query.Get("location[directory]"); got != dir {
		t.Errorf("%s location[directory] = %q, want %q", c.key(), got, dir)
	}
}

// ---------------------------------------------------------------------
// detection / passthrough
// ---------------------------------------------------------------------

func TestCompatPassthroughWhenInstalledV1(t *testing.T) {
	defer SetInstalledV2(false)()
	f := fakeServer(t)
	f.json("GET /config", http.StatusOK, `{"v1":true}`)
	got := cmpOK(t, http.MethodGet, f.URL+"/config?directory=/d", "")
	cmpJSON(t, "body", got, `{"v1":true}`)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 || f.calls[0].Path != "/config" {
		t.Fatalf("calls = %+v, want only the untouched /config", f.calls)
	}
}

func TestCompatPassthroughWhenServerNotV2(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.notV2 = true
	f.json("GET /session/s1", http.StatusOK, `{"id":"s1"}`)
	cmpJSON(t, "body", cmpOK(t, http.MethodGet, f.URL+"/session/s1", ""), `{"id":"s1"}`)
	cmpKeys(t, f, "GET /session/s1")
	// The verdict is cached: a second request does not re-probe.
	cmpOK(t, http.MethodGet, f.URL+"/session/s1", "")
	f.mu.Lock()
	probes := 0
	for _, c := range f.calls {
		if c.Path == "/api/info" {
			probes++
		}
	}
	f.mu.Unlock()
	if probes != 1 {
		t.Errorf("/api/info probes = %d, want 1 (cached)", probes)
	}
}

func TestCompatPassthroughAPIPaths(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{}}`)
	cmpJSON(t, "body", cmpOK(t, http.MethodGet, f.URL+"/api/session/active", ""), `{"data":{}}`)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 || f.calls[0].Path != "/api/session/active" {
		t.Fatalf("calls = %+v, want only the passthrough (no probe)", f.calls)
	}
}

func TestCompatUnknownRouteNotFound(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	status, b := cmpDo(t, http.MethodGet, f.URL+"/nope", nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d", status)
	}
	var v map[string]any
	_ = json.Unmarshal(b, &v)
	if v["name"] != "NotFound" {
		t.Errorf("body = %s", b)
	}
	cmpKeys(t, f)
}

func TestCompatUpstreamErrorRelayed(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s1", http.StatusConflict, `{"name":"Conflict","data":{"message":"busy"}}`)
	status, b := cmpDo(t, http.MethodGet, f.URL+"/session/s1", nil, "")
	if status != http.StatusConflict || string(b) != `{"name":"Conflict","data":{"message":"busy"}}` {
		t.Fatalf("got %d %s", status, b)
	}
}

// ---------------------------------------------------------------------
// catalog routes
// ---------------------------------------------------------------------

func TestCompatConfig(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/config", http.StatusOK, `[
		{"type":"document","info":{"model":"a/b","agents":{"title":{"model":"x/small"}}}},
		{"type":"other","info":{"model":"ignored"}},
		{"type":"document","info":{"theme":"dark"}}]`)
	got := cmpOK(t, http.MethodGet, f.URL+"/config?directory="+url.QueryEscape("/my dir"), "")
	cmpJSON(t, "config", got, `{"model":"a/b","theme":"dark","agents":{"title":{"model":"x/small"}},"small_model":"x/small"}`)
	cmpLoc(t, f.one("GET /api/config"), "/my dir")
}

func TestCompatProjectCurrent(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/location", http.StatusOK, `{"directory":"/repo/wt","project":{"id":"p1","directory":"/repo","canonical":"/repo"}}`)
	got := cmpOK(t, http.MethodGet, f.URL+"/project/current?directory=/repo/wt", "")
	cmpJSON(t, "project", got, `{"id":"p1","worktree":"/repo","directory":"/repo/wt"}`)
	cmpLoc(t, f.one("GET /api/location"), "/repo/wt")
}

func TestCompatAgentsAndCommands(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/agent", http.StatusOK, `{"data":[{"id":"build","description":"B","mode":"primary"},{"id":"mine","mode":"subagent","hidden":true,"model":{"providerID":"p","id":"m"}}]}`)
	f.json("GET /api/command", http.StatusOK, `{"data":[{"name":"review","description":"R"}]}`)
	cmpJSON(t, "agents", cmpOK(t, http.MethodGet, f.URL+"/agent?directory=/d", ""), `[
		{"name":"build","description":"B","mode":"primary","hidden":false,"native":true,"color":""},
		{"name":"mine","description":"","mode":"subagent","hidden":true,"native":false,"color":"","model":"p/m"}]`)
	cmpJSON(t, "commands", cmpOK(t, http.MethodGet, f.URL+"/command?directory=/d", ""),
		`[{"name":"review","description":"R","template":"","source":"command"}]`)
	cmpLoc(t, f.one("GET /api/agent"), "/d")
	cmpLoc(t, f.one("GET /api/command"), "/d")
}

func TestCompatProviders(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/provider", http.StatusOK, `{"data":[{"id":"openai","name":"OpenAI"},{"id":"empty","name":"Empty"}]}`)
	f.json("GET /api/model", http.StatusOK, `{"data":[
		{"providerID":"openai","id":"gpt","modelID":"gpt-upstream","name":"GPT","status":"active","variants":[{"id":"high"}],"limit":{"context":100,"output":10}},
		{"providerID":"openai","id":"off","modelID":"off","enabled":false}]}`)
	f.json("GET /api/model/default", http.StatusOK, `{"data":{"providerID":"openai","id":"gpt","modelID":"gpt-upstream"}}`)
	got := cmpOK(t, http.MethodGet, f.URL+"/provider?directory=/d", "")
	cmpJSON(t, "providers", got, `{
		"all":[
			{"id":"openai","name":"OpenAI","models":{"gpt":{"id":"gpt","name":"GPT","status":"active","variants":{"high":{}},"limit":{"context":100,"output":10}}}},
			{"id":"empty","name":"Empty","models":{}}],
		"connected":["openai"],
		"default":{"openai":"gpt"}}`)
	for _, k := range []string{"GET /api/provider", "GET /api/model", "GET /api/model/default"} {
		cmpLoc(t, f.one(k), "/d")
	}
}

func TestCompatProvidersUpstreamError(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/provider", http.StatusOK, `{"data":[]}`)
	f.json("GET /api/model", http.StatusInternalServerError, `{"name":"Boom"}`)
	status, b := cmpDo(t, http.MethodGet, f.URL+"/provider", nil, "")
	if status != http.StatusInternalServerError || string(b) != `{"name":"Boom"}` {
		t.Fatalf("got %d %s", status, b)
	}
	// /api/model/default is optional: missing it still answers.
	f2 := fakeServer(t)
	f2.json("GET /api/provider", http.StatusOK, `{"data":[]}`)
	f2.json("GET /api/model", http.StatusOK, `{"data":[]}`)
	cmpJSON(t, "no default", cmpOK(t, http.MethodGet, f2.URL+"/provider", ""), `{"all":[],"connected":[],"default":{}}`)
}

func TestCompatMCPAndLSP(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/mcp", http.StatusOK, `{"data":[{"name":"ocman","status":{"status":"connected"}},{"name":"bad","status":{"status":"failed","error":"nope"}}]}`)
	f.json("POST /api/experimental/mcp/my srv/connect", http.StatusOK, `{}`)
	cmpJSON(t, "mcp", cmpOK(t, http.MethodGet, f.URL+"/mcp?directory=/d", ""),
		`{"ocman":{"status":"connected","error":""},"bad":{"status":"failed","error":"nope"}}`)
	cmpLoc(t, f.one("GET /api/mcp"), "/d")
	cmpJSON(t, "connect", cmpOK(t, http.MethodPost, f.URL+"/mcp/my%20srv/connect?directory=/d", ""), `true`)
	cmpLoc(t, f.one("POST /api/experimental/mcp/my srv/connect"), "/d")
	n := len(f.v2Calls())
	cmpJSON(t, "lsp", cmpOK(t, http.MethodGet, f.URL+"/lsp", ""), `[]`)
	if len(f.v2Calls()) != n {
		t.Errorf("/lsp made upstream calls: %v", f.keys())
	}
}

func TestCompatSessionStatus(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{"s1":{"since":1},"s2":{}}}`)
	cmpJSON(t, "status", cmpOK(t, http.MethodGet, f.URL+"/session/status", ""), `{"s1":{"type":"busy"},"s2":{"type":"busy"}}`)
}

// ---------------------------------------------------------------------
// sessions
// ---------------------------------------------------------------------

const fakeSessionJSON = `{"data":{"id":"s1","projectID":"p1","location":{"directory":"/tmp/my dir"},"title":"T",
	"time":{"created":1,"updated":2},"permissions":[{"action":"shell","resource":"*","effect":"ask"}]}}`

const cmpV1Session = `{"id":"s1","projectID":"p1","directory":"/tmp/my dir","title":"T","version":"2",
	"time":{"created":1,"updated":2},"permission":[{"permission":"bash","pattern":"*","action":"ask"}]}`

func TestCompatCreateSession(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("POST /api/session", http.StatusOK, fakeSessionJSON)
	status, b := cmpDo(t, http.MethodPost, f.URL+"/session",
		map[string]string{"x-opencode-directory": url.PathEscape("/tmp/my dir")},
		`{"title":"T","permission":[{"permission":"bash","pattern":"*","action":"ask"}]}`)
	if status != http.StatusOK {
		t.Fatalf("status %d %s", status, b)
	}
	var got any
	_ = json.Unmarshal(b, &got)
	cmpJSON(t, "session", got, cmpV1Session)
	cmpJSON(t, "v2 body", f.one("POST /api/session").Body,
		`{"title":"T","location":{"directory":"/tmp/my dir"},"permissions":[{"action":"shell","resource":"*","effect":"ask"}]}`)

	// A child session addresses its parent instead of a location.
	f2 := fakeServer(t)
	f2.json("POST /api/session", http.StatusOK, fakeSessionJSON)
	cmpDo(t, http.MethodPost, f2.URL+"/session", map[string]string{"x-opencode-directory": "/x"}, `{"parentID":"sp"}`)
	cmpJSON(t, "child body", f2.one("POST /api/session").Body, `{"parentID":"sp"}`)
}

func TestCompatCreateSessionBadBody(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.URL+"/session", strings.NewReader(`{not json`))
	resp, err := fakeClient().Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("want an error for an undecodable body")
	}
	cmpKeys(t, f)
}

func TestCompatGetPatchDeleteSession(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s1", http.StatusOK, fakeSessionJSON)
	f.json("PATCH /api/session/s1", http.StatusOK, `{}`)
	f.json("DELETE /api/session/s1", http.StatusOK, `{}`)

	cmpJSON(t, "get", cmpOK(t, http.MethodGet, f.URL+"/session/s1", ""), cmpV1Session)
	cmpJSON(t, "patch", cmpOK(t, http.MethodPatch, f.URL+"/session/s1",
		`{"title":"New","permission":[{"permission":"write","pattern":"*.go","action":"allow"}]}`), cmpV1Session)
	cmpJSON(t, "patch body", f.one("PATCH /api/session/s1").Body,
		`{"title":"New","permissions":[{"action":"edit","resource":"*.go","effect":"allow"}]}`)
	cmpJSON(t, "delete", cmpOK(t, http.MethodDelete, f.URL+"/session/s1", ""), `true`)
	cmpKeys(t, f, "GET /api/session/s1", "PATCH /api/session/s1", "GET /api/session/s1", "DELETE /api/session/s1")
}

func TestCompatPatchSessionUpstreamError(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("PATCH /api/session/s1", http.StatusNotFound, `{"name":"NotFoundError"}`)
	status, _ := cmpDo(t, http.MethodPatch, f.URL+"/session/s1", nil, `{"title":"x"}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d", status)
	}
	cmpKeys(t, f, "PATCH /api/session/s1")
}

func TestCompatMessagesPaginate(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.on("GET /api/session/s1/message", func(c fakeCall) (int, any) {
		if c.Query.Get("cursor") == "c2" {
			return http.StatusOK, json.RawMessage(`{"data":[{"id":"msg2","type":"user","text":"again","time":{"created":2}}],"cursor":{}}`)
		}
		if c.Query.Get("order") != "asc" || c.Query.Get("limit") != "200" {
			t.Errorf("first page query = %v", c.Query)
		}
		return http.StatusOK, json.RawMessage(`{"data":[
			{"id":"msg1","type":"user","text":"hi","time":{"created":1}},
			{"id":"msgS","type":"system","text":"hidden"}],"cursor":{"next":"c2"}}`)
	})
	got := cmpOK(t, http.MethodGet, f.URL+"/session/s1/message", "")
	cmpJSON(t, "messages", got, `[
		{"info":{"id":"msg1","sessionID":"s1","role":"user","time":{"created":1}},
		 "parts":[{"id":"prt10001","messageID":"msg1","sessionID":"s1","type":"text","text":"hi","synthetic":false}]},
		{"info":{"id":"msg2","sessionID":"s1","role":"user","time":{"created":2}},
		 "parts":[{"id":"prt20001","messageID":"msg2","sessionID":"s1","type":"text","text":"again","synthetic":false}]}]`)
	calls := f.find("GET /api/session/s1/message")
	if len(calls) != 2 || calls[1].Query.Encode() != "cursor=c2" {
		t.Errorf("pages = %+v", calls)
	}
}

// ---------------------------------------------------------------------
// prompts
// ---------------------------------------------------------------------

func TestCompatPromptAsyncSwitchesAgentAndModel(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s1", http.StatusOK, `{"data":{"id":"s1","agent":"build","model":{"providerID":"anthropic","id":"claude"}}}`)
	for _, k := range []string{"POST /api/session/s1/agent", "POST /api/session/s1/model", "POST /api/session/s1/prompt"} {
		f.json(k, http.StatusOK, `{}`)
	}
	status, b := cmpDo(t, http.MethodPost, f.URL+"/session/s1/prompt_async", nil, `{
		"agent":"plan","model":{"providerID":"openai","modelID":"gpt","variant":"high"},"delivery":"steer",
		"parts":[{"type":"text","text":"a"},{"type":"file","url":"file:///x.png","mime":"image/png","filename":"x.png"},{"type":"text","text":"b"}]}`)
	if status != http.StatusNoContent || len(b) != 0 {
		t.Fatalf("got %d %q", status, b)
	}
	cmpKeys(t, f, "GET /api/session/s1", "POST /api/session/s1/agent", "POST /api/session/s1/model", "POST /api/session/s1/prompt")
	cmpJSON(t, "agent", f.one("POST /api/session/s1/agent").Body, `{"agent":"plan"}`)
	cmpJSON(t, "model", f.one("POST /api/session/s1/model").Body, `{"model":{"providerID":"openai","id":"gpt","variant":"high"}}`)
	cmpJSON(t, "prompt", f.one("POST /api/session/s1/prompt").Body,
		`{"text":"a\n\nb","files":[{"uri":"file:///x.png","name":"x.png"}],"delivery":"steer"}`)
}

// A held prompt must not switch agent or model now: that would change the
// running turn. It is refused (412) so ocman's own queue holds it; a held
// prompt that matches the session is queued natively.
func TestCompatQueuedPromptNeverSwitchesSelection(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s1", http.StatusOK, `{"data":{"id":"s1","agent":"build","model":{"providerID":"anthropic","id":"claude"}}}`)
	f.json("POST /api/session/s1/prompt", http.StatusOK, `{}`)
	status, _ := cmpDo(t, http.MethodPost, f.URL+"/session/s1/prompt_async", nil,
		`{"agent":"plan","delivery":"queue","parts":[{"type":"text","text":"later"}]}`)
	if status != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412", status)
	}
	cmpKeys(t, f, "GET /api/session/s1")
	status, _ = cmpDo(t, http.MethodPost, f.URL+"/session/s1/prompt_async", nil,
		`{"agent":"build","model":{"providerID":"anthropic","modelID":"claude"},"delivery":"queue","parts":[{"type":"text","text":"later"}]}`)
	if status != http.StatusNoContent {
		t.Fatalf("matching queued prompt status = %d", status)
	}
	cmpJSON(t, "prompt", f.one("POST /api/session/s1/prompt").Body, `{"text":"later","delivery":"queue"}`)
}

func TestCompatPromptAsyncNoSwitchWhenCurrent(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s1", http.StatusOK, `{"data":{"id":"s1","agent":"plan","model":{"providerID":"openai","id":"gpt"}}}`)
	f.json("POST /api/session/s1/prompt", http.StatusOK, `{}`)
	status, _ := cmpDo(t, http.MethodPost, f.URL+"/session/s1/prompt_async", nil,
		`{"agent":"plan","model":{"providerID":"openai","modelID":"gpt","variant":"default"},"delivery":"bogus","parts":[{"type":"text","text":"x"}]}`)
	if status != http.StatusNoContent {
		t.Fatalf("status %d", status)
	}
	cmpKeys(t, f, "GET /api/session/s1", "POST /api/session/s1/prompt")
	cmpJSON(t, "prompt", f.one("POST /api/session/s1/prompt").Body, `{"text":"x"}`)

	// Neither agent nor model: no session lookup at all.
	f2 := fakeServer(t)
	f2.json("POST /api/session/s1/prompt", http.StatusOK, `{}`)
	cmpDo(t, http.MethodPost, f2.URL+"/session/s1/prompt_async", nil, `{"delivery":"steer","parts":[]}`)
	cmpKeys(t, f2, "POST /api/session/s1/prompt")
	cmpJSON(t, "steer", f2.one("POST /api/session/s1/prompt").Body, `{"text":"","delivery":"steer"}`)
}

func TestCompatPromptAsyncSessionLookupFails(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s1", http.StatusNotFound, `{"name":"NotFoundError"}`)
	status, b := cmpDo(t, http.MethodPost, f.URL+"/session/s1/prompt_async", nil, `{"agent":"plan"}`)
	if status != http.StatusNotFound || string(b) != `{"name":"NotFoundError"}` {
		t.Fatalf("got %d %s", status, b)
	}
	cmpKeys(t, f, "GET /api/session/s1")
}

const fakeHistory = `{"data":[
	{"id":"msg1","type":"user","text":"q","time":{"created":1}},
	{"id":"msg2","type":"assistant","agent":"build","model":{"providerID":"p","id":"m"},"time":{"created":2,"completed":3},"finish":"stop","content":[{"type":"text","text":"answer"}]},
	{"id":"msg3","type":"agent-switched"}]}`

func TestCompatMessageSyncWaits(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("POST /api/session/s1/prompt", http.StatusOK, `{"data":{"id":"msg1"}}`)
	f.json("POST /api/experimental/session/s1/wait", http.StatusOK, `{}`)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{}}`)
	f.json("GET /api/session/s1/message", http.StatusOK, fakeHistory)
	got := cmpOK(t, http.MethodPost, f.URL+"/session/s1/message", `{"parts":[{"type":"text","text":"q"}]}`)
	m, _ := got.(map[string]any)
	if info := obj(m, "info"); info["id"] != "msg2" || info["role"] != "assistant" {
		t.Fatalf("reply = %v", got)
	}
}

// Idle before the prompt's turn produced a reply (v2 had not scheduled it
// yet) must keep waiting, not return an older or empty answer.
func TestCompatMessageSyncWaitsForItsOwnReply(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("POST /api/session/s1/prompt", http.StatusOK, `{"data":{"id":"msg5"}}`)
	f.json("POST /api/experimental/session/s1/wait", http.StatusNotFound, `{}`)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{}}`)
	var mu sync.Mutex
	reads := 0
	f.on("GET /api/session/s1/message", func(fakeCall) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		reads++
		old := `{"id":"msg2","type":"assistant","agent":"build","model":{"providerID":"p","id":"m"},"time":{"created":2},"finish":"stop","content":[]}`
		if reads == 1 {
			return http.StatusOK, json.RawMessage(`{"data":[` + old + `,{"id":"msg5","type":"user","text":"q"}]}`)
		}
		return http.StatusOK, json.RawMessage(`{"data":[` + old + `,{"id":"msg5","type":"user","text":"q"},
			{"id":"msg6","type":"assistant","agent":"build","model":{"providerID":"p","id":"m"},"time":{"created":6},"finish":"stop","content":[{"type":"text","text":"new"}]}]}`)
	})
	got := cmpOK(t, http.MethodPost, f.URL+"/session/s1/message", `{"parts":[{"type":"text","text":"q"}]}`)
	if info := obj(got.(map[string]any), "info"); info["id"] != "msg6" {
		t.Fatalf("reply = %v, want the turn's own reply msg6", got)
	}
}

func TestCompatMessageSyncErrors(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("POST /api/session/s1/prompt", http.StatusBadRequest, `{"name":"Bad"}`)
	if status, _ := cmpDo(t, http.MethodPost, f.URL+"/session/s1/message", nil, `{}`); status != http.StatusBadRequest {
		t.Errorf("prompt failure status = %d", status)
	}
	f2 := fakeServer(t)
	f2.json("POST /api/session/s1/prompt", http.StatusOK, `{"data":{"id":"msg1"}}`)
	f2.json("GET /api/session/active", http.StatusBadGateway, `{"name":"Gateway"}`)
	if status, _ := cmpDo(t, http.MethodPost, f2.URL+"/session/s1/message", nil, `{}`); status != http.StatusBadGateway {
		t.Errorf("poll failure status = %d", status)
	}
	f3 := fakeServer(t)
	f3.json("POST /api/session/s1/prompt", http.StatusOK, `{"data":{"id":"msg1"}}`)
	f3.json("GET /api/session/active", http.StatusOK, `{"data":{}}`)
	f3.json("GET /api/session/s1/message", http.StatusGone, `{"name":"Gone"}`)
	if status, _ := cmpDo(t, http.MethodPost, f3.URL+"/session/s1/message", nil, `{}`); status != http.StatusGone {
		t.Errorf("messages failure status = %d", status)
	}
}

// ---------------------------------------------------------------------
// session actions
// ---------------------------------------------------------------------

func TestCompatShellAndCommand(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("POST /api/session/s1/shell", http.StatusOK, `{}`)
	f.json("GET /api/session/s1", http.StatusOK, `{"data":{"id":"s1","agent":"build","model":{"providerID":"openai","id":"gpt"}}}`)
	f.json("POST /api/session/s1/model", http.StatusOK, `{}`)
	f.json("POST /api/session/s1/command", http.StatusOK, `{}`)

	cmpJSON(t, "shell", cmpOK(t, http.MethodPost, f.URL+"/session/s1/shell", `{"command":"ls -la","agent":"build"}`), `{}`)
	cmpJSON(t, "shell body", f.one("POST /api/session/s1/shell").Body, `{"command":"ls -la"}`)

	cmpJSON(t, "command", cmpOK(t, http.MethodPost, f.URL+"/session/s1/command",
		`{"command":"review","arguments":"main","model":"openai/gpt#high"}`), `{}`)
	cmpJSON(t, "model body", f.one("POST /api/session/s1/model").Body, `{"model":{"providerID":"openai","id":"gpt","variant":"high"}}`)
	cmpJSON(t, "command body", f.one("POST /api/session/s1/command").Body, `{"name":"review","text":"main"}`)
	cmpKeys(t, f, "POST /api/session/s1/shell", "GET /api/session/s1", "POST /api/session/s1/model", "POST /api/session/s1/command")
}

func TestCompatSessionActions(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s1", http.StatusOK, fakeSessionJSON)
	for _, k := range []string{
		"POST /api/session/s1/interrupt", "POST /api/session/s1/revert/stage", "DELETE /api/session/s1/revert",
		"POST /api/session/s1/compact", "POST /api/session/s1/move",
	} {
		f.json(k, http.StatusOK, `{}`)
	}
	f.json("POST /api/session/s1/fork", http.StatusOK, `{"data":{"id":"s2","parentID":"s1","location":{"directory":"/d"},"time":{"created":5}}}`)

	cmpJSON(t, "abort", cmpOK(t, http.MethodPost, f.URL+"/session/s1/abort", ""), `true`)
	cmpJSON(t, "revert", cmpOK(t, http.MethodPost, f.URL+"/session/s1/revert", `{"messageID":"msg5"}`), cmpV1Session)
	cmpJSON(t, "revert body", f.one("POST /api/session/s1/revert/stage").Body, `{"messageID":"msg5","files":true}`)
	cmpJSON(t, "unrevert", cmpOK(t, http.MethodPost, f.URL+"/session/s1/unrevert", ""), cmpV1Session)
	cmpJSON(t, "summarize", cmpOK(t, http.MethodPost, f.URL+"/session/s1/summarize", `{"providerID":"p","modelID":"m"}`), `true`)
	cmpJSON(t, "compact body", f.one("POST /api/session/s1/compact").Body, `{}`)
	cmpJSON(t, "fork", cmpOK(t, http.MethodPost, f.URL+"/session/s1/fork", `{"messageID":"msg5"}`),
		`{"id":"s2","projectID":"","parentID":"s1","directory":"/d","title":"","version":"2","time":{"created":5,"updated":null},"permission":[]}`)
	cmpJSON(t, "fork body", f.one("POST /api/session/s1/fork").Body, `{"before":"msg5"}`)
	cmpJSON(t, "move", cmpOK(t, http.MethodPost, f.URL+"/experimental/control-plane/move-session",
		`{"sessionID":"s1","destination":{"directory":"/new"}}`), `true`)
	cmpJSON(t, "move body", f.one("POST /api/session/s1/move").Body, `{"directory":"/new"}`)

	cmpKeys(t, f,
		"POST /api/session/s1/interrupt",
		"POST /api/session/s1/revert/stage", "GET /api/session/s1",
		"DELETE /api/session/s1/revert", "GET /api/session/s1",
		"POST /api/session/s1/compact",
		"POST /api/session/s1/fork",
		"POST /api/session/s1/move")
}

func TestCompatForkWithoutMessage(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("POST /api/session/s1/fork", http.StatusOK, `{"data":{"id":"s2"}}`)
	cmpOK(t, http.MethodPost, f.URL+"/session/s1/fork", "")
	cmpJSON(t, "fork body", f.one("POST /api/session/s1/fork").Body, `{}`)
}

func TestCompatRevertUpstreamError(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("POST /api/session/s1/revert/stage", http.StatusConflict, `{"name":"Busy"}`)
	f.json("DELETE /api/session/s1/revert", http.StatusConflict, `{"name":"Busy"}`)
	for _, p := range []string{"/revert", "/unrevert"} {
		if status, b := cmpDo(t, http.MethodPost, f.URL+"/session/s1"+p, nil, `{}`); status != http.StatusConflict || string(b) != `{"name":"Busy"}` {
			t.Errorf("%s: got %d %s", p, status, b)
		}
	}
	if n := len(f.find("GET /api/session/s1")); n != 0 {
		t.Errorf("session fetched %d times after a failed revert", n)
	}
}

// ---------------------------------------------------------------------
// permissions and questions
// ---------------------------------------------------------------------

func TestCompatPermissionListAndReply(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/permission/request", http.StatusOK, `{"data":[{"id":"per_cmp_list","sessionID":"s9","action":"shell",
		"resources":["ls"],"save":["ls *"],"metadata":{},"source":{"messageID":"msg1","id":"call1"}}]}`)
	f.json("POST /api/session/s9/permission/per_cmp_list/reply", http.StatusOK, `{}`)

	got := cmpOK(t, http.MethodGet, f.URL+"/permission?directory=/d", "")
	cmpJSON(t, "permissions", got, `[{"id":"per_cmp_list","sessionID":"s9","permission":"bash","patterns":["ls"],"always":["ls *"],
		"metadata":{"command":"ls"},"tool":{"messageID":"msg1","callID":"call1"}}]`)
	cmpLoc(t, f.one("GET /api/permission/request"), "/d")

	cmpJSON(t, "reply", cmpOK(t, http.MethodPost, f.URL+"/permission/per_cmp_list/reply", `{"reply":"once","message":"ok"}`), `true`)
	cmpJSON(t, "reply body", f.one("POST /api/session/s9/permission/per_cmp_list/reply").Body, `{"decision":"once","message":"ok"}`)
	// Resolved from the cache filled by the list: no second list.
	cmpKeys(t, f, "GET /api/permission/request", "POST /api/session/s9/permission/per_cmp_list/reply")
}

func TestCompatPermissionReplyCacheMiss(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/permission/request", http.StatusOK, `{"data":[{"id":"per_cmp_miss","sessionID":"s8","action":"read"}]}`)
	f.json("POST /api/session/s8/permission/per_cmp_miss/reply", http.StatusOK, `{}`)
	cmpJSON(t, "reply", cmpOK(t, http.MethodPost, f.URL+"/permission/per_cmp_miss/reply?directory=/d", `{"reply":"always"}`), `true`)
	cmpKeys(t, f, "GET /api/permission/request", "POST /api/session/s8/permission/per_cmp_miss/reply")
	cmpLoc(t, f.one("GET /api/permission/request"), "/d")
	cmpJSON(t, "reply body", f.one("POST /api/session/s8/permission/per_cmp_miss/reply").Body, `{"decision":"always"}`)
}

func TestCompatPermissionReplyUnknown(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/permission/request", http.StatusOK, `{"data":[]}`)
	status, b := cmpDo(t, http.MethodPost, f.URL+"/permission/per_cmp_unknown/reply", nil, `{"reply":"once"}`)
	if status != http.StatusNotFound || !strings.Contains(string(b), "NotFoundError") {
		t.Fatalf("got %d %s", status, b)
	}
	cmpKeys(t, f, "GET /api/permission/request")
}

const fakeQuestionForms = `{"data":[
	{"id":"que_cmp_1","sessionID":"s7","metadata":{"kind":"question","tool":{"messageID":"msg1","id":"call1"}},
	 "fields":[{"key":"q0","type":"string","title":"Color","description":"Pick?","options":[{"label":"Blue","description":"b"}]},
	           {"key":"q1","type":"multiselect","title":"Many","description":"Pick some","custom":false}]},
	{"id":"frm_cmp_other","sessionID":"s7","metadata":{"kind":"mcp"}}]}`

func TestCompatQuestions(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/form", http.StatusOK, fakeQuestionForms)
	f.json("GET /api/session/s7/form/que_cmp_1", http.StatusOK, `{"data":{"id":"que_cmp_1","fields":[
		{"key":"q0","type":"string"},{"key":"q1","type":"multiselect"},{"key":"q2","type":"string"}]}}`)
	f.json("POST /api/session/s7/form/que_cmp_1/reply", http.StatusOK, `{}`)
	f.json("DELETE /api/session/s7/form/que_cmp_1", http.StatusOK, `{}`)

	got := cmpOK(t, http.MethodGet, f.URL+"/question?directory=/d", "")
	cmpJSON(t, "questions", got, `[{"id":"que_cmp_1","sessionID":"s7","tool":{"messageID":"msg1","callID":"call1"},"questions":[
		{"header":"Color","question":"Pick?","options":[{"label":"Blue","description":"b"}],"multiple":false,"custom":true},
		{"header":"Many","question":"Pick some","options":[],"multiple":true,"custom":false}]}]`)
	cmpLoc(t, f.one("GET /api/form"), "/d")

	cmpJSON(t, "reply", cmpOK(t, http.MethodPost, f.URL+"/question/que_cmp_1/reply", `{"answers":[["Blue"],["a","b"]]}`), `true`)
	cmpJSON(t, "answer", f.one("POST /api/session/s7/form/que_cmp_1/reply").Body, `{"answer":{"q0":"Blue","q1":["a","b"]}}`)

	cmpJSON(t, "reject", cmpOK(t, http.MethodPost, f.URL+"/question/que_cmp_1/reject", ""), `true`)
	cmpKeys(t, f, "GET /api/form", "GET /api/session/s7/form/que_cmp_1", "POST /api/session/s7/form/que_cmp_1/reply",
		"DELETE /api/session/s7/form/que_cmp_1")
}

func TestCompatQuestionUnknownOrGone(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/form", http.StatusOK, `{"data":[{"id":"que_cmp_gone","sessionID":"s6","metadata":{"kind":"question"}}]}`)
	// Listed, but the form fetch fails: treated as gone.
	if status, _ := cmpDo(t, http.MethodPost, f.URL+"/question/que_cmp_gone/reply", nil, `{"answers":[]}`); status != http.StatusNotFound {
		t.Errorf("gone form reply status = %d", status)
	}
	cmpKeys(t, f, "GET /api/form", "GET /api/session/s6/form/que_cmp_gone")
	// Never listed at all.
	for _, p := range []string{"/question/que_cmp_never/reply", "/question/que_cmp_never/reject"} {
		if status, _ := cmpDo(t, http.MethodPost, f.URL+p, nil, `{}`); status != http.StatusNotFound {
			t.Errorf("%s status = %d", p, status)
		}
	}
}

// ---------------------------------------------------------------------
// helpers shared with events_test.go
// ---------------------------------------------------------------------

// cmpStream opens a translated event stream and returns every decoded
// `data:` frame once the upstream stream ends.
func cmpStream(t *testing.T, u string) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := fakeClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var out []map[string]any
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("frame %q: %v", line, err)
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("stream read: %v", err)
	}
	return out
}

type fakeBody struct {
	io.Reader
	closed bool
}

func (b *fakeBody) Close() error { b.closed = true; return nil }

// http.RoundTripper: "RoundTrip must always close the body, including
// on errors". Routes that never read the v1 body leave it open.
func TestCompatRoundTripClosesRequestBody(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("POST /api/session/s1/interrupt", http.StatusOK, `{}`)
	for _, p := range []string{"/session/s1/abort", "/nope"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		body := &fakeBody{Reader: strings.NewReader(`{}`)}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.URL+p, body)
		resp, err := Wrap(http.DefaultTransport).RoundTrip(req)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if !body.closed {
			t.Errorf("POST %s: request body not closed", p)
		}
	}
}

// A failed lookup is not proof the prompt is gone: relaying a 404 would
// make the adapter forget a prompt the agent is still waiting on.
func TestCompatPromptLookupErrorsAreRelayed(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/form", http.StatusOK, `{"data":[{"id":"que_cmp_err","sessionID":"s9","metadata":{"kind":"question"}}]}`)
	f.json("GET /api/session/s9/form/que_cmp_err", http.StatusInternalServerError, `{"message":"boom"}`)
	if status, _ := cmpDo(t, http.MethodPost, f.URL+"/question/que_cmp_err/reply", nil, `{"answers":[["x"]]}`); status != http.StatusInternalServerError {
		t.Errorf("form fetch 500: reply status = %d, want 500", status)
	}

	g := fakeServer(t)
	g.json("GET /api/permission/request", http.StatusInternalServerError, `{"message":"down"}`)
	g.json("GET /api/form", http.StatusInternalServerError, `{"message":"down"}`)
	for _, p := range []string{"/permission/per_cmp_lookup/reply", "/question/que_cmp_lookup/reply", "/question/que_cmp_lookup/reject"} {
		if status, _ := cmpDo(t, http.MethodPost, g.URL+p, nil, `{"reply":"once","answers":[]}`); status != http.StatusInternalServerError {
			t.Errorf("%s with a failing list: status = %d, want 500", p, status)
		}
	}
}
