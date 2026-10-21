package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Native integration reads belong to platforms. Server composition may keep a
// legacy handle and report its pool diagnostics, but must not query its content
// or manufacture an unregistered adapter as a fallback.
func TestServerDoesNotReadNativePlatformContent(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if receiver, ok := selector.X.(*ast.SelectorExpr); ok && receiver.Sel.Name == "db" && selector.Sel.Name != "Stats" {
				if root, ok := receiver.X.(*ast.Ident); ok && root.Name == "s" {
					t.Errorf("%s: native content query in server", fset.Position(call.Pos()))
				}
				if root, ok := receiver.X.(*ast.SelectorExpr); ok && root.Sel.Name == "server" {
					t.Errorf("%s: native content query in server", fset.Position(call.Pos()))
				}
			}
			if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "opencode" && selector.Sel.Name == "New" {
				t.Errorf("%s: raw fallback adapter in server", fset.Position(call.Pos()))
			}
			return true
		})
	}
	// Analytics calculations may use the read-only statev2 compatibility reader,
	// but the feature must not accept a native database in its dependency struct.
	raw, err := os.ReadFile("../analytics/module.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "analytics/module.go", raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Deps" {
			return true
		}
		deps, ok := spec.Type.(*ast.StructType)
		if !ok {
			t.Fatal("analytics Deps is not a struct")
		}
		for _, field := range deps.Fields.List {
			for _, name := range field.Names {
				if name.Name == "DB" || name.Name == "Analytics" {
					t.Errorf("analytics accepts native %s dependency", name.Name)
				}
			}
		}
		return false
	})
}
