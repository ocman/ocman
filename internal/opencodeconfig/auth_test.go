package opencodeconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

const testTokenFile = "/home/u/.local/share/ocman/mcp-token"

func entryHeaders(t *testing.T, path string, v2 bool) map[string]any {
	t.Helper()
	doc, servers := readDoc(t, path)
	entry := servers[ServerName]
	if !v2 {
		entry = nil
		if err := json.Unmarshal(doc.MCP[ServerName], &entry); err != nil {
			t.Fatal(err)
		}
	}
	headers, _ := entry["headers"].(map[string]any)
	return headers
}

// Install references the token file, never the token, and Check only calls
// the entry configured when that reference is present.
func TestInstallWritesTokenFileReference(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "v1", true: "v2"}[v2], func(t *testing.T) {
			defer ocv2.SetInstalledV2(v2)()
			path := filepath.Join(configHome(t), "opencode.json")
			writeConfig(t, path, `{"mcp":{"ocman":{"type":"remote","url":"`+testURL+`","enabled":true}}}`)
			if v2 {
				if _, err := EnsureEntry(testURL, ""); err != nil {
					t.Fatal(err)
				}
			}
			if st, err := Check(testURL, ""); err != nil || !st.Configured {
				t.Fatalf("tokenless check = %+v, %v; want configured", st, err)
			}
			if st, err := Check(testURL, testTokenFile); err != nil || st.Configured {
				t.Fatalf("check without header = %+v, %v; want not configured", st, err)
			}
			if _, err := Install(testURL, testTokenFile); err != nil {
				t.Fatal(err)
			}
			if got := entryHeaders(t, path, v2)["Authorization"]; got != "Bearer {file:"+testTokenFile+"}" {
				t.Fatalf("Authorization = %v", got)
			}
			if st, err := Check(testURL, testTokenFile); err != nil || !st.Configured {
				t.Fatalf("check after install = %+v, %v", st, err)
			}
		})
	}
}

// Startup repair adds the reference to an entry installed before the token
// existed, keeping the entry's other fields and headers, under both layouts.
func TestEnsureEntryAddsTokenReference(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "v1", true: "v2"}[v2], func(t *testing.T) {
			defer ocv2.SetInstalledV2(v2)()
			path := filepath.Join(configHome(t), "opencode.json")
			writeConfig(t, path, `{"mcp":{"ocman":{"type":"remote","url":"`+testURL+`","enabled":true,"timeout":9000,"headers":{"X-Keep":"1"}}}}`)
			backup, err := EnsureEntry(testURL, testTokenFile)
			if err != nil || backup == "" {
				t.Fatalf("EnsureEntry = %q, %v; want a rewrite", backup, err)
			}
			headers := entryHeaders(t, path, v2)
			if headers["Authorization"] != AuthHeader(testTokenFile) || headers["X-Keep"] != "1" {
				t.Fatalf("headers = %v", headers)
			}
			raw, _ := os.ReadFile(path)
			if !strings.Contains(string(raw), `"timeout": 9000`) {
				t.Fatalf("lost other fields: %s", raw)
			}
			if again, err := EnsureEntry(testURL, testTokenFile); err != nil || again != "" {
				t.Fatalf("second EnsureEntry = %q, %v; want no-op", again, err)
			}
		})
	}
}

func TestEnsureEntryV1(t *testing.T) {
	defer ocv2.SetInstalledV2(false)()
	dir := configHome(t)
	// No config, no entry: nothing to repair and nothing added.
	if backup, err := EnsureEntry(testURL, testTokenFile); err != nil || backup != "" {
		t.Fatalf("missing config = %q, %v", backup, err)
	}
	path := filepath.Join(dir, "opencode.json")
	writeConfig(t, path, `{"mcp":{"other":{"type":"local","command":["x"]}}}`)
	if backup, err := EnsureEntry(testURL, testTokenFile); err != nil || backup != "" {
		t.Fatalf("no ocman entry = %q, %v", backup, err)
	}
	// An entry ocman cannot read is left for the user, not rewritten.
	writeConfig(t, path, `{"mcp":{"ocman":{"type":"remote","url":"x","headers":"bad"}}}`)
	if backup, err := EnsureEntry(testURL, testTokenFile); err != nil || backup != "" {
		t.Fatalf("unreadable entry = %q, %v", backup, err)
	}
	jsonc := filepath.Join(dir, "opencode.jsonc")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, jsonc, `{"mcp":{"ocman":{"type":"remote","url":"x"}}}`)
	if _, err := EnsureEntry(testURL, testTokenFile); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("JSONC error = %v, want ErrNotEditable", err)
	}
}

// Startup repair never sends the token to an entry that points somewhere
// other than ocman's listener, such as another host, under either layout.
func TestEnsureEntryRefusesTokenForForeignURL(t *testing.T) {
	const foreign = "http://evil.example:8227/mcp"
	for _, v2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "v1", true: "v2"}[v2], func(t *testing.T) {
			defer ocv2.SetInstalledV2(v2)()
			path := filepath.Join(configHome(t), "opencode.json")
			writeConfig(t, path, `{"mcp":{"ocman":{"type":"remote","url":"`+foreign+`","enabled":true,"headers":{"X-Keep":"1"}}}}`)
			if _, err := EnsureEntry(testURL, testTokenFile); err != nil {
				t.Fatal(err)
			}
			headers := entryHeaders(t, path, v2)
			if got, ok := headers["Authorization"]; ok {
				t.Fatalf("Authorization = %v injected into an entry for %s", got, foreign)
			}
			if headers["X-Keep"] != "1" {
				t.Fatalf("headers = %v", headers)
			}
		})
	}
}
