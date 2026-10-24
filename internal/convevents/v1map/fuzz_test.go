package v1map

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// corpusSeeds reads the values of Go fuzz corpus files ("go test fuzz v1").
func corpusSeeds(f *testing.F, dirs ...string) [][]byte {
	var out [][]byte
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*", "*"))
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				f.Fatal(err)
			}
			for _, line := range strings.Split(string(raw), "\n") {
				open, end := strings.IndexByte(line, '('), strings.LastIndexByte(line, ')')
				literal := strings.HasPrefix(line, "[]byte(") || strings.HasPrefix(line, "string(")
				if !literal || open < 0 || end <= open {
					continue
				}
				if v, err := strconv.Unquote(line[open+1 : end]); err == nil {
					out = append(out, []byte(v))
				}
			}
		}
	}
	return out
}

// FuzzMapper feeds arbitrary JSONL frame sequences (and the same bytes as one
// SSE frame) through a mapper: it must not panic, must be deterministic, and
// every event must frame as a well-formed envelope with contiguous offsets.
func FuzzMapper(f *testing.F) {
	fixtures, _ := filepath.Glob(filepath.Join("testdata", "*", "*.v1.jsonl"))
	for _, path := range fixtures {
		raw, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	root := filepath.Join("..", "..")
	for _, seed := range corpusSeeds(f,
		filepath.Join(root, "platforms", "opencode", "apiv2", "testdata", "fuzz"),
		filepath.Join(root, "autoapprove", "testdata", "fuzz"),
		filepath.Join(root, "platforms", "claude", "provider", "testdata", "fuzz"),
	) {
		f.Add(seed)
	}
	f.Add([]byte("id: 3\nevent: ocman.permission.flagged\ndata: {\"permissionId\":\"p\",\"reasoning\":\"r\"}"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<16 {
			return
		}
		session, frames := readFixture(nil, raw)
		if session == "" {
			session = "s"
		}
		frames = append(frames, ParseFrame(raw))
		got := encodeJSONL(t, mapFixture(session, "fuzz", frames))
		if again := encodeJSONL(t, mapFixture(session, "fuzz", frames)); !bytes.Equal(got, again) {
			t.Fatalf("non-deterministic mapping:\n%s\n%s", got, again)
		}
		checkWellFormed(t, got)
	})
}
