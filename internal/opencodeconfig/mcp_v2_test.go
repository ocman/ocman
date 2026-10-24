package opencodeconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// TestMain pins the installed-OpenCode check so no test runs the real
// `opencode --version`.
func TestMain(m *testing.M) {
	ocv2.SetInstalledV2(false)
	os.Exit(m.Run())
}

const testURL = "http://127.0.0.1:8227/mcp"

// configDoc is the subset of a written config the v2 tests assert on.
type configDoc struct {
	MCP map[string]json.RawMessage `json:"mcp"`
}

func readDoc(t *testing.T, path string) (configDoc, map[string]map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc configDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	servers := map[string]map[string]any{}
	if rawServers, ok := doc.MCP["servers"]; ok {
		if err := json.Unmarshal(rawServers, &servers); err != nil {
			t.Fatalf("mcp.servers: %v: %s", err, raw)
		}
	}
	return doc, servers
}

// OpenCode v2 converts v1's flat mcp.<name> entries field by field and
// drops codemode, so the entry must live in v2's own mcp.servers layout
// for Code Mode to stay off.
func TestInstallV2WritesServersLayout(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	dir := configHome(t)
	path := filepath.Join(dir, "opencode.json")
	writeConfig(t, path, `{"mcp":{
		"ocman":{"type":"remote","url":"http://127.0.0.1:1/mcp","enabled":true},
		"other":{"type":"local","command":["x"],"enabled":true},
		"servers":{"kept":{"type":"remote","url":"http://k/mcp","disabled":false}}}}`)

	if _, err := Install(testURL); err != nil {
		t.Fatalf("Install: %v", err)
	}
	doc, servers := readDoc(t, path)
	if _, has := doc.MCP[ServerName]; has {
		t.Errorf("flat mcp.ocman left behind: %s", doc.MCP[ServerName])
	}
	if _, has := doc.MCP["other"]; !has {
		t.Error("unrelated flat server dropped")
	}
	if _, has := servers["kept"]; !has {
		t.Error("unrelated mcp.servers entry dropped")
	}
	want := map[string]any{"type": "remote", "url": testURL, "disabled": false, "codemode": false}
	got := servers[ServerName]
	for k, v := range want {
		if got[k] != v {
			t.Errorf("mcp.servers.ocman[%s] = %v, want %v (entry %v)", k, got[k], v, got)
		}
	}
	if st, err := Check(testURL); err != nil || !st.Configured {
		t.Errorf("Check after Install: %+v, %v", st, err)
	}
}

func TestCheckV2(t *testing.T) {
	cases := []struct {
		name       string
		mcp        string
		configured bool
		currentURL string
	}{
		{"v1-era flat entry without codemode",
			`{"ocman":{"type":"remote","url":"` + testURL + `","enabled":true}}`, false, testURL},
		// What ocman wrote before this fix: v2 drops codemode from it.
		{"flat entry with codemode false",
			`{"ocman":{"type":"remote","url":"` + testURL + `","enabled":true,"codemode":false}}`, false, testURL},
		{"servers entry with codemode false",
			`{"servers":{"ocman":{"type":"remote","url":"` + testURL + `","disabled":false,"codemode":false}}}`, true, testURL},
		{"servers entry without codemode",
			`{"servers":{"ocman":{"type":"remote","url":"` + testURL + `","disabled":false}}}`, false, testURL},
		{"servers entry disabled",
			`{"servers":{"ocman":{"type":"remote","url":"` + testURL + `","disabled":true,"codemode":false}}}`, false, testURL},
		{"servers entry with stale URL",
			`{"servers":{"ocman":{"type":"remote","url":"http://127.0.0.1:1/mcp","codemode":false}}}`, false, "http://127.0.0.1:1/mcp"},
		{"servers entry plus leftover flat entry",
			`{"ocman":{"type":"remote","url":"` + testURL + `"},"servers":{"ocman":{"type":"remote","url":"` + testURL + `","codemode":false}}}`, false, testURL},
		{"no entry", `{"other":{"type":"local","command":["x"]}}`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer ocv2.SetInstalledV2(true)()
			dir := configHome(t)
			writeConfig(t, filepath.Join(dir, "opencode.json"), `{"mcp":`+tc.mcp+`}`)
			st, err := Check(testURL)
			if err != nil {
				t.Fatal(err)
			}
			if st.Configured != tc.configured || st.CurrentURL != tc.currentURL {
				t.Errorf("Check = configured %v url %q, want %v %q", st.Configured, st.CurrentURL, tc.configured, tc.currentURL)
			}
		})
	}
}

func TestEnsureV2Layout(t *testing.T) {
	const stale = "http://127.0.0.1:1/mcp"
	cases := []struct {
		name      string
		v2        bool
		body      string // config file contents; "" means no file
		rewritten bool
		wantURL   string
		disabled  bool
	}{
		{name: "v1 installed", body: `{"mcp":{"ocman":{"type":"remote","url":"` + testURL + `"}}}`},
		{name: "no config file", v2: true},
		{name: "no ocman entry", v2: true, body: `{"mcp":{"other":{"type":"local","command":["x"]}}}`},
		{name: "already current", v2: true,
			body: `{"mcp":{"servers":{"ocman":{"type":"remote","url":"` + testURL + `","disabled":false,"codemode":false}}}}`},
		{name: "flat entry without codemode", v2: true, rewritten: true, wantURL: testURL,
			body: `{"mcp":{"ocman":{"type":"remote","url":"` + testURL + `","enabled":true}}}`},
		{name: "flat entry with codemode false", v2: true, rewritten: true, wantURL: testURL,
			body: `{"mcp":{"ocman":{"type":"remote","url":"` + testURL + `","enabled":true,"codemode":false}}}`},
		{name: "keeps stale URL", v2: true, rewritten: true, wantURL: stale,
			body: `{"mcp":{"ocman":{"type":"remote","url":"` + stale + `"}}}`},
		{name: "keeps disabled flat entry disabled", v2: true, rewritten: true, wantURL: testURL, disabled: true,
			body: `{"mcp":{"ocman":{"type":"remote","url":"` + testURL + `","enabled":false}}}`},
		{name: "servers entry without codemode", v2: true, rewritten: true, wantURL: testURL,
			body: `{"mcp":{"servers":{"ocman":{"type":"remote","url":"` + testURL + `"}}}}`},
		{name: "keeps disabled servers entry disabled", v2: true, rewritten: true, wantURL: testURL, disabled: true,
			body: `{"mcp":{"servers":{"ocman":{"type":"remote","url":"` + testURL + `","disabled":true,"codemode":true}}}}`},
		{name: "drops leftover flat entry", v2: true, rewritten: true, wantURL: testURL,
			body: `{"mcp":{"ocman":{"type":"remote","url":"` + stale + `"},"servers":{"ocman":{"type":"remote","url":"` + testURL + `","codemode":false}}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer ocv2.SetInstalledV2(tc.v2)()
			dir := configHome(t)
			path := filepath.Join(dir, "opencode.json")
			if tc.body != "" {
				writeConfig(t, path, `{"theme":"x",`+tc.body[1:])
			}
			backup, err := EnsureV2Layout()
			if err != nil {
				t.Fatalf("EnsureV2Layout: %v", err)
			}
			if !tc.rewritten {
				if backup != "" {
					t.Errorf("backup = %q, want no rewrite", backup)
				}
				if tc.body != "" {
					if raw, _ := os.ReadFile(path); string(raw) != `{"theme":"x",`+tc.body[1:] {
						t.Errorf("config changed without a rewrite: %s", raw)
					}
				}
				return
			}
			if orig, err := os.ReadFile(backup); err != nil || string(orig) != `{"theme":"x",`+tc.body[1:] {
				t.Errorf("backup %q = %q, %v; want the original bytes", backup, orig, err)
			}
			doc, servers := readDoc(t, path)
			if _, has := doc.MCP[ServerName]; has {
				t.Errorf("flat mcp.ocman left behind: %s", doc.MCP[ServerName])
			}
			got := servers[ServerName]
			want := map[string]any{"type": "remote", "url": tc.wantURL, "disabled": tc.disabled, "codemode": false}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("mcp.servers.ocman[%s] = %v, want %v (entry %v)", k, got[k], v, got)
				}
			}
			if raw, _ := os.ReadFile(path); !json.Valid(raw) || !containsTheme(raw) {
				t.Errorf("other keys not preserved: %s", raw)
			}
			if again, err := EnsureV2Layout(); err != nil || again != "" {
				t.Errorf("second run rewrote again: %q, %v", again, err)
			}
		})
	}
}

func containsTheme(raw []byte) bool {
	var doc map[string]any
	return json.Unmarshal(raw, &doc) == nil && doc["theme"] == "x"
}

func TestEnsureV2LayoutRefusesUneditable(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	cases := map[string]struct{ file, body string }{
		"jsonc":          {"opencode.jsonc", `{"mcp":{"ocman":{"type":"remote","url":"` + testURL + `"}}}`},
		"comments":       {"opencode.json", "{\n// note\n\"mcp\":{}}"},
		"mcp not object": {"opencode.json", `{"mcp":[1],"x":{}}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := configHome(t)
			path := filepath.Join(dir, tc.file)
			writeConfig(t, path, tc.body)
			backup, err := EnsureV2Layout()
			if name == "mcp not object" {
				// No readable ocman entry: nothing to repair.
				if err != nil || backup != "" {
					t.Errorf("got %q, %v; want no-op", backup, err)
				}
			} else if !errors.Is(err, ErrNotEditable) || backup != "" {
				t.Errorf("got %q, %v; want ErrNotEditable", backup, err)
			}
			if raw, _ := os.ReadFile(path); string(raw) != tc.body {
				t.Errorf("config modified: %s", raw)
			}
		})
	}
}
