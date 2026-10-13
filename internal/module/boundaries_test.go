package module

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Modules depend on each other through interfaces supplied by the server,
// never through imports. Test imports may use other modules as fixtures.
func TestFeatureModuleImportBoundaries(t *testing.T) {
	modules := []string{"routines", "webhook", "artifacts", "inbox", "sharing", "factorymod", "pluginsmod", "autoapprove", "tmuxmod", "filesystem"}
	for _, name := range modules {
		entries, err := os.ReadDir(filepath.Join("..", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join("..", name, entry.Name())
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range file.Imports {
				importPath, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				for _, forbidden := range append([]string{"server"}, modules...) {
					if forbidden == name {
						continue
					}
					prefix := "github.com/NoUseFreak/ocman/internal/" + forbidden
					if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
						t.Errorf("%s imports %s; supply the dependency through Deps", path, importPath)
					}
				}
			}
		}
	}
}
