package codexprovider

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRolloutCacheDetectsMiddleEditBeforeAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	padding := `{"type":"session_meta","padding":"` + strings.Repeat("x", 8192) + "\"}\n"
	usage := `{"type":"token_usage_record","payload":{"turn_id":"middle","turn_token_usage":{"input_tokens":11,"output_tokens":2},"usage":{"input_tokens":11,"output_tokens":2}}}` + "\n"
	raw := []byte(padding + usage + padding)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var cache rolloutUsageCache
	if _, err := cache.read(path); err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte(`"input_tokens":11`), []byte(`"input_tokens":99`))
	raw = append(raw, []byte("{}\n")...)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := cache.read(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := ReadRolloutUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("middle edit retained stale rollout accounting: got %+v want %+v", got, want)
	}
}

func TestRolloutCacheReplaysUnterminatedRecordsWithoutRetainingStaleUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	raw := []byte("{}\n" + `{"type":"token_usage_record","payload":{"turn_id":"one","turn_token_usage":{"input_tokens":11}}}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var cache rolloutUsageCache
	first, err := cache.read(path)
	if err != nil || first["one"].Tokens.InputTokens != 11 {
		t.Fatalf("valid EOF record lost: %v %v", first, err)
	}
	if err := os.WriteFile(path, append(raw, []byte(" invalid continuation\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := cache.read(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("unterminated accounting survived an invalid completed record: %v %v", got, err)
	}
}
