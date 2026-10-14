// Package frontendcontract checks the Go DTOs the frontend mirrors by hand
// (frontend/src/lib/api.types.ts) against a committed snapshot. The frontend's
// vitest suite checks the TypeScript types against the same snapshot, so a
// field added, renamed or removed on either side fails a test.
//
// Regenerate after an intentional DTO change with `make contracts`.
package frontendcontract

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type field struct {
	Name      string `json:"name"`
	OmitEmpty bool   `json:"omitempty"`
}

type snapshot struct {
	SessionStatus []string           `json:"SessionStatus"`
	DTOs          map[string][]field `json:"dtos"`
}

// dtos maps the TypeScript interface name to the Go type it mirrors.
var dtos = map[string]any{
	"Session":              db.Session{},
	"SessionNotice":        db.SessionNotice{},
	"Message":              db.Message{},
	"Part":                 db.Part{},
	"PlatformCapabilities": platforms.Capabilities{},
	"HostCapabilities":     hostsvc.HostCaps{},
}

func jsonFields(t reflect.Type) []field {
	var out []field
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, field{Name: name, OmitEmpty: strings.Contains(","+opts+",", ",omitempty,")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// sessionStatuses reads the SessionStatus constants from source, so a new
// status is picked up without editing this test.
func sessionStatuses(t *testing.T, dir string) []string {
	t.Helper()
	path := filepath.Join(dir, "internal", "db", "session_types.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "SessionStatus" {
				continue
			}
			for _, v := range vs.Values {
				if lit, ok := v.(*ast.BasicLit); ok {
					s, _ := strconv.Unquote(lit.Value)
					out = append(out, s)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestFrontendContractSnapshot(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	snap := snapshot{SessionStatus: sessionStatuses(t, root), DTOs: map[string][]field{}}
	if len(snap.SessionStatus) == 0 {
		t.Fatal("no SessionStatus constants found")
	}
	for name, v := range dtos {
		snap.DTOs[name] = jsonFields(reflect.TypeOf(v))
	}
	got, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	path := filepath.Join(root, "frontend", "src", "lib", "contracts", "go-dtos.json")
	if os.Getenv("UPDATE_FRONTEND_CONTRACTS") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `make contracts`)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is out of date with the Go DTOs. Run `make contracts`, then update frontend/src/lib/api.types.ts until `pnpm test` passes.", path)
	}
}
