package filesystem

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- helpers ---

// fsTestTree builds a small directory tree used by the browse/search
// tests and returns its root.
//
//	root/
//	  .hidden/
//	  Zeta/
//	  alpha/
//	  projects/myapp/{.git/,go.mod}
//	  node_modules/skipped/
//	  notes.txt
//	  linkdir  -> alpha
//	  filelink -> notes.txt
//	  brokenlink -> nowhere
func fsTestTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		".hidden",
		"Zeta",
		"alpha",
		filepath.Join("projects", "myapp", ".git"),
		filepath.Join("node_modules", "skipped"),
	} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "projects", "myapp", "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	for _, ln := range [][2]string{
		{"alpha", "linkdir"},
		{"notes.txt", "filelink"},
		{"nowhere", "brokenlink"},
	} {
		if err := os.Symlink(filepath.Join(root, ln[0]), filepath.Join(root, ln[1])); err != nil {
			t.Fatalf("symlink %s: %v", ln[1], err)
		}
	}
	return root
}

// unreadableDir returns a directory that can be traversed but not read
// (mode 0111), so os.Stat succeeds while os.ReadDir fails with EACCES.
// Skips as root, which ignores the permission bits.
func unreadableDir(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o111); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Restore before TempDir's own cleanup (LIFO) so removal succeeds.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	return dir
}

func getJSON[T any](t *testing.T, h http.HandlerFunc, target string) T {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s: got %d, want 200: %s", target, rr.Code, rr.Body.String())
	}
	var out T
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET %s: invalid JSON: %v (%s)", target, err, rr.Body.String())
	}
	return out
}

// --- /api/filesystem/directories ---

func TestHandleFilesystemDirectories_ErrorPaths(t *testing.T) {
	m := NewModule(Deps{})
	tmp := t.TempDir()
	file := filepath.Join(tmp, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tests := []struct {
		name     string
		dir      string
		wantCode int
		wantBody string
	}{
		{"relative path", "relative/dir", http.StatusBadRequest, "absolute path"},
		{"traversal escaping to relative", "../../etc", http.StatusBadRequest, "absolute path"},
		{"missing directory", filepath.Join(tmp, "does-not-exist"), http.StatusNotFound, "directory not found"},
		{"file not directory", file, http.StatusBadRequest, "must be a directory"},
		{"tilde relative miss", "~/ocman-definitely-absent-dir", http.StatusNotFound, "directory not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/filesystem/directories?dir="+url.QueryEscape(tt.dir), nil)
			rr := httptest.NewRecorder()
			m.handleDirectories(rr, req)
			if rr.Code != tt.wantCode {
				t.Fatalf("got %d, want %d: %s", rr.Code, tt.wantCode, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Errorf("body %q does not contain %q", rr.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestHandleFilesystemDirectories_Unreadable(t *testing.T) {
	m := NewModule(Deps{})
	req := httptest.NewRequest(http.MethodGet, "/api/filesystem/directories?dir="+url.QueryEscape(unreadableDir(t)), nil)
	rr := httptest.NewRecorder()
	m.handleDirectories(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleFilesystemDirectories_ListsOnlyDirectories(t *testing.T) {
	m := NewModule(Deps{})
	root := fsTestTree(t)

	got := getJSON[directoryBrowseResponse](t, m.handleDirectories,
		"/api/filesystem/directories?dir="+url.QueryEscape(root))

	if got.Directory != root {
		t.Errorf("directory = %q, want %q", got.Directory, root)
	}
	if got.Parent != filepath.Dir(root) {
		t.Errorf("parent = %q, want %q", got.Parent, filepath.Dir(root))
	}
	// Visible dirs first (case-insensitive alphabetical), hidden last.
	// Files, file symlinks and dangling symlinks are excluded; a symlink
	// to a directory is included.
	wantNames := []string{"alpha", "linkdir", "node_modules", "projects", "Zeta", ".hidden"}
	var gotNames []string
	for _, e := range got.Entries {
		gotNames = append(gotNames, e.Name)
		if e.Path != filepath.Join(root, e.Name) {
			t.Errorf("entry %q path = %q", e.Name, e.Path)
		}
		if want := strings.HasPrefix(e.Name, "."); e.Hidden != want {
			t.Errorf("entry %q hidden = %v, want %v", e.Name, e.Hidden, want)
		}
	}
	if strings.Join(gotNames, ",") != strings.Join(wantNames, ",") {
		t.Errorf("entries = %v, want %v", gotNames, wantNames)
	}
}

func TestHandleFilesystemDirectories_HomeAndRootDefaults(t *testing.T) {
	m := NewModule(Deps{})
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tests := []struct {
		name      string
		query     string
		wantDir   string
		wantEmpty bool // parent is empty for the filesystem root
	}{
		{"absent dir falls back to home", "", home, false},
		{"bare tilde", "?dir=~", home, false},
		{"tilde prefix", "?dir=" + url.QueryEscape("~/sub"), filepath.Join(home, "sub"), false},
		{"filesystem root has no parent", "?dir=" + url.QueryEscape(string(filepath.Separator)), string(filepath.Separator), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getJSON[directoryBrowseResponse](t, m.handleDirectories,
				"/api/filesystem/directories"+tt.query)
			if got.Directory != tt.wantDir {
				t.Errorf("directory = %q, want %q", got.Directory, tt.wantDir)
			}
			if tt.wantEmpty && got.Parent != "" {
				t.Errorf("parent = %q, want empty", got.Parent)
			}
			if !tt.wantEmpty && got.Parent == "" {
				t.Error("parent is empty, want a value")
			}
		})
	}
}

// With no resolvable home the handler falls back to the working
// directory rather than erroring.
func TestHandleFilesystemDirectories_NoHomeFallsBackToCwd(t *testing.T) {
	m := NewModule(Deps{})
	t.Setenv("HOME", "")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	got := getJSON[directoryBrowseResponse](t, m.handleDirectories, "/api/filesystem/directories")
	if got.Directory != filepath.Clean(cwd) {
		t.Errorf("directory = %q, want %q", got.Directory, cwd)
	}
	if got.Home != "" {
		t.Errorf("home = %q, want empty", got.Home)
	}
}

// --- /api/filesystem/directory-search ---

func TestHandleFilesystemDirectorySearch_ErrorPaths(t *testing.T) {
	m := NewModule(Deps{})
	tmp := t.TempDir()
	file := filepath.Join(tmp, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tests := []struct {
		name     string
		root     string
		wantCode int
		wantBody string
	}{
		{"relative root", "relative/dir", http.StatusBadRequest, "absolute path"},
		{"traversal root", "../..", http.StatusBadRequest, "absolute path"},
		{"missing root", filepath.Join(tmp, "nope"), http.StatusNotFound, "root not found"},
		{"file root", file, http.StatusBadRequest, "must be a directory"},
		{"tilde relative miss", "~/ocman-definitely-absent-dir", http.StatusNotFound, "root not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet,
				"/api/filesystem/directory-search?q=x&root="+url.QueryEscape(tt.root), nil)
			rr := httptest.NewRecorder()
			m.handleDirectorySearch(rr, req)
			if rr.Code != tt.wantCode {
				t.Fatalf("got %d, want %d: %s", rr.Code, tt.wantCode, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Errorf("body %q does not contain %q", rr.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestHandleFilesystemDirectorySearch_UnreadableRoot(t *testing.T) {
	m := NewModule(Deps{})
	req := httptest.NewRequest(http.MethodGet,
		"/api/filesystem/directory-search?q=x&root="+url.QueryEscape(unreadableDir(t)), nil)
	rr := httptest.NewRecorder()
	m.handleDirectorySearch(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleFilesystemDirectorySearch_Matches(t *testing.T) {
	m := NewModule(Deps{})
	root := fsTestTree(t)
	esc := url.QueryEscape(root)

	tests := []struct {
		name      string
		query     string
		wantRoot  string
		wantPaths []string
	}{
		{
			name:      "fuzzy match skips node_modules and hidden",
			query:     "?q=myapp&root=" + esc,
			wantRoot:  root,
			wantPaths: []string{filepath.Join(root, "projects", "myapp")},
		},
		{
			name:      "empty query yields no entries",
			query:     "?q=&root=" + esc,
			wantRoot:  root,
			wantPaths: nil,
		},
		{
			name:      "limit below minimum is clamped",
			query:     "?q=alpha&limit=0&root=" + esc,
			wantRoot:  root,
			wantPaths: []string{filepath.Join(root, "alpha")},
		},
		{
			name:      "limit above maximum is clamped",
			query:     "?q=alpha&limit=5000&root=" + esc,
			wantRoot:  root,
			wantPaths: []string{filepath.Join(root, "alpha")},
		},
		{
			name:      "unparsable limit uses the default",
			query:     "?q=alpha&limit=abc&root=" + esc,
			wantRoot:  root,
			wantPaths: []string{filepath.Join(root, "alpha")},
		},
		{
			name:      "absolute query re-roots at its parent",
			query:     "?root=" + esc + "&q=" + url.QueryEscape(filepath.Join(root, "projects", "myapp")),
			wantRoot:  filepath.Join(root, "projects"),
			wantPaths: []string{filepath.Join(root, "projects", "myapp")},
		},
		{
			// The re-rooted parent does not exist, so the search falls
			// back to the requested root with the raw query. Every
			// path segment then becomes a required token, so nothing
			// under root matches.
			name:      "absolute query with unusable parent falls back to root",
			query:     "?root=" + esc + "&q=" + url.QueryEscape(filepath.Join(root, "gone", "alpha")+"/"),
			wantRoot:  root,
			wantPaths: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getJSON[directorySearchResponse](t, m.handleDirectorySearch,
				"/api/filesystem/directory-search"+tt.query)
			if got.Root != tt.wantRoot {
				t.Errorf("root = %q, want %q", got.Root, tt.wantRoot)
			}
			var paths []string
			for _, e := range got.Entries {
				paths = append(paths, e.Path)
			}
			if strings.Join(paths, ",") != strings.Join(tt.wantPaths, ",") {
				t.Errorf("entries = %v, want %v", paths, tt.wantPaths)
			}
		})
	}
}

// The project marker (go.mod) must be reported so the picker can badge
// real projects.
func TestHandleFilesystemDirectorySearch_FlagsProjectDirectory(t *testing.T) {
	m := NewModule(Deps{})
	root := fsTestTree(t)
	got := getJSON[directorySearchResponse](t, m.handleDirectorySearch,
		"/api/filesystem/directory-search?q=myapp&root="+url.QueryEscape(root))
	if len(got.Entries) != 1 {
		t.Fatalf("entries = %v, want exactly one", got.Entries)
	}
	if e := got.Entries[0]; !e.Project || e.Depth != 2 {
		t.Errorf("entry = %+v, want project=true depth=2", e)
	}
}

func TestHandleFilesystemDirectorySearch_HomeDefaults(t *testing.T) {
	m := NewModule(Deps{})
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Empty query short-circuits the walk, so this only pins the root
	// resolution (bare home, "~", "~/sub").
	if err := os.Mkdir(filepath.Join(home, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, tc := range map[string]struct{ query, wantRoot string }{
		"absent root falls back to home": {"?q=", home},
		"bare tilde":                     {"?q=&root=~", home},
		"tilde prefix":                   {"?q=&root=" + url.QueryEscape("~/sub"), filepath.Join(home, "sub")},
	} {
		t.Run(name, func(t *testing.T) {
			got := getJSON[directorySearchResponse](t, m.handleDirectorySearch,
				"/api/filesystem/directory-search"+tc.query)
			if got.Root != tc.wantRoot {
				t.Errorf("root = %q, want %q", got.Root, tc.wantRoot)
			}
		})
	}
}

func TestHandleFilesystemDirectorySearch_NoHomeFallsBackToCwd(t *testing.T) {
	m := NewModule(Deps{})
	t.Setenv("HOME", "")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	got := getJSON[directorySearchResponse](t, m.handleDirectorySearch,
		"/api/filesystem/directory-search?q=")
	if got.Root != filepath.Clean(cwd) {
		t.Errorf("root = %q, want %q", got.Root, cwd)
	}
}

// --- filesystem helpers ---

func TestExpandHomePath(t *testing.T) {
	tests := []struct {
		name, path, home, want string
	}{
		{"bare tilde", "~", "/home/u", "/home/u"},
		{"tilde prefix", "~/src/x", "/home/u", "/home/u/src/x"},
		{"tilde without home", "~", "", "~"},
		{"tilde prefix without home", "~/src", "", "~/src"},
		{"absolute untouched", "/tmp/x", "/home/u", "/tmp/x"},
		{"relative untouched", "src/x", "/home/u", "src/x"},
		{"embedded tilde untouched", "/a/~/b", "/home/u", "/a/~/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandHomePath(tt.path, tt.home); got != tt.want {
				t.Errorf("expandHomePath(%q, %q) = %q, want %q", tt.path, tt.home, got, tt.want)
			}
		})
	}
}

func TestDirectorySearchRootAndQuery(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "projects")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	home := t.TempDir()

	tests := []struct {
		name, root, query, home string
		wantRoot, wantQuery     string
		wantExact               string
	}{
		{"empty query", root, "", home, root, "", ""},
		{"relative query", root, "myapp", home, root, "myapp", ""},
		{"existing absolute dir", root, existing, home, root, "projects", existing},
		{"missing absolute path", root, filepath.Join(root, "gone"), home, root, "gone", ""},
		{"missing absolute with slash", root, filepath.Join(root, "gone") + "/", home, filepath.Join(root, "gone"), "", ""},
		{"tilde expands to home", root, "~", home, filepath.Dir(home), filepath.Base(home), home},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRoot, gotQuery, gotExact := directorySearchRootAndQuery(tt.root, tt.query, tt.home)
			if gotRoot != tt.wantRoot || gotQuery != tt.wantQuery || gotExact != tt.wantExact {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)",
					gotRoot, gotQuery, gotExact, tt.wantRoot, tt.wantQuery, tt.wantExact)
			}
		})
	}
}

func TestDirectorySearchClassifiers(t *testing.T) {
	t.Run("shouldSkip", func(t *testing.T) {
		tests := []struct {
			name          string
			includeHidden bool
			want          bool
		}{
			{"src", false, false},
			{".config", false, true},
			{".config", true, false},
			{".git", true, true},
			{"node_modules", false, true},
			{"vendor", false, true},
			{".pnpm-store", true, true},
		}
		for _, tt := range tests {
			if got := shouldSkipDirectorySearchEntry(tt.name, tt.includeHidden); got != tt.want {
				t.Errorf("shouldSkipDirectorySearchEntry(%q, %v) = %v, want %v",
					tt.name, tt.includeHidden, got, tt.want)
			}
		}
	})

	t.Run("visitRank", func(t *testing.T) {
		tests := []struct {
			name   string
			tokens []string
			want   int
		}{
			{"myapp", []string{"myapp"}, 0},
			{"src", nil, 1},
			{"Workspaces", nil, 1},
			{"Library", nil, 50},
			{"Music", nil, 50},
			{"random", nil, 10},
		}
		for _, tt := range tests {
			if got := directorySearchVisitRank(tt.name, tt.tokens); got != tt.want {
				t.Errorf("directorySearchVisitRank(%q, %v) = %d, want %d", tt.name, tt.tokens, got, tt.want)
			}
		}
	})

	t.Run("heavyBranch", func(t *testing.T) {
		for name, want := range map[string]bool{
			"Library": true, "applications": true, "Movies": true,
			"music": true, "Pictures": true, "src": false, "": false,
		} {
			if got := isHeavyDirectorySearchBranch(name); got != want {
				t.Errorf("isHeavyDirectorySearchBranch(%q) = %v, want %v", name, got, want)
			}
		}
	})

	t.Run("hiddenComponent", func(t *testing.T) {
		for path, want := range map[string]bool{
			"a/b/c": false, "a/.b/c": true, ".hidden": true,
			"./a": false, "../a": false, "a/../b": false,
		} {
			if got := pathHasHiddenComponent(path); got != want {
				t.Errorf("pathHasHiddenComponent(%q) = %v, want %v", path, got, want)
			}
		}
	})

	t.Run("projectDirectory", func(t *testing.T) {
		plain := t.TempDir()
		if got := isLikelyProjectDirectory(plain); got {
			t.Errorf("isLikelyProjectDirectory(%q) = true, want false", plain)
		}
		// Gemfile sits late in the marker list, so this also walks it.
		project := t.TempDir()
		if err := os.WriteFile(filepath.Join(project, "Gemfile"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if !isLikelyProjectDirectory(project) {
			t.Errorf("isLikelyProjectDirectory(%q) = false, want true", project)
		}
	})
}

func TestDirectorySearchScore(t *testing.T) {
	tests := []struct {
		name, rel, entry, query string
		depth                   int
		wantScore               int
		wantOK                  bool
	}{
		{"no tokens matches everything", "a/b", "b", "", 1, 0, true},
		{"token missing from rel", "a/b", "b", "zzz", 1, 0, false},
		{"exact name match", "a/myapp", "myapp", "myapp", 2, 10, true},
		{"prefix match", "a/myapp2", "myapp2", "myapp", 2, 20, true},
		{"substring match", "a/xmyappx", "xmyappx", "myapp", 1, 25, true},
		{"path-only match scores worst", "myapp/inner", "inner", "myapp", 2, 45, true},
		{"multi token all present", "src/myapp", "myapp", "src myapp", 2, 45, true},
		{"multi token one absent", "src/myapp", "myapp", "src nope", 2, 0, false},
		{"fuzzy subsequence", "banana-frontend", "banana-frontend", "banfron", 1, 40, true},
		{"out of order rejected", "banana-frontend", "banana-frontend", "fronban", 1, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, ok := directorySearchScore(tt.rel, tt.entry, tt.query, tt.depth)
			if ok != tt.wantOK || score != tt.wantScore {
				t.Errorf("got (%d, %v), want (%d, %v)", score, ok, tt.wantScore, tt.wantOK)
			}
		})
	}
}

func TestSearchDirectories_EmptyQueryAndExactSeed(t *testing.T) {
	root := fsTestTree(t)
	exact := filepath.Join(root, "projects", "myapp")

	got, err := searchDirectories(root, "", 10, "")
	if err != nil {
		t.Fatalf("empty query: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty query returned %v, want none", got)
	}

	// The exact seed sorts first even when the walk finds other matches.
	got, err = searchDirectories(root, "a", 10, exact)
	if err != nil {
		t.Fatalf("exact seed: %v", err)
	}
	if len(got) == 0 || got[0].Path != exact {
		t.Fatalf("entries = %v, want %q first", got, exact)
	}
	if !got[0].Project {
		t.Errorf("seeded entry %+v, want project=true", got[0])
	}
	// Hidden directories surface only when the query mentions a dot.
	hidden, err := searchDirectories(root, ".hidden", 10, "")
	if err != nil {
		t.Fatalf("hidden query: %v", err)
	}
	if len(hidden) != 1 || hidden[0].Path != filepath.Join(root, ".hidden") {
		t.Errorf("entries = %v, want the hidden directory", hidden)
	}
}

func TestSearchDirectories_FuzzyCandidatesDoNotStopTraversal(t *testing.T) {
	root := t.TempDir()
	for i := range 41 {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("banana-frontend-%02d", i)), 0o755); err != nil {
			t.Fatalf("mkdir decoy: %v", err)
		}
	}
	project := filepath.Join(root, "workspace", "banana-frontend")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.com/project\n"), 0o644); err != nil {
		t.Fatalf("write project marker: %v", err)
	}

	got, err := searchDirectories(root, "banfron", 10, "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) == 0 || got[0].Path != project {
		t.Fatalf("entries = %+v, want project %q first", got, project)
	}
}

func TestSearchDirectories_UnreadableRootErrors(t *testing.T) {
	if _, err := searchDirectories(unreadableDir(t), "x", 10, ""); !os.IsPermission(err) {
		t.Fatalf("err = %v, want a permission error", err)
	}
}

func TestHandleFilesystemDirectorySearch_FindsNestedProjectDirectories(t *testing.T) {
	m := NewModule(Deps{})
	root := t.TempDir()
	projectDir := filepath.Join(root, "workspace", "ocman")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module example.com/ocman\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "ocman-copy"), 0o755); err != nil {
		t.Fatalf("mkdir skipped dir: %v", err)
	}

	resp := getJSON[directorySearchResponse](t, m.handleDirectorySearch,
		"/api/filesystem/directory-search?root="+url.QueryEscape(root)+"&q=work+oc&limit=10")
	if resp.Root != filepath.Clean(root) {
		t.Fatalf("root = %q, want %q", resp.Root, filepath.Clean(root))
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("entries = %+v, want one project match", resp.Entries)
	}
	if resp.Entries[0].Path != projectDir || !resp.Entries[0].Project {
		t.Fatalf("entry = %+v, want project dir %q", resp.Entries[0], projectDir)
	}
}

func TestHandleFilesystemDirectorySearch_DeduplicatesExactAbsoluteDirectory(t *testing.T) {
	m := NewModule(Deps{})
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "workspace")
	projectDir := filepath.Join(workspaceDir, "research")
	nestedDir := filepath.Join(projectDir, "anam-research")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module example.com/research\n"), 0o644); err != nil {
		t.Fatalf("write project marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write nested project marker: %v", err)
	}

	resp := getJSON[directorySearchResponse](t, m.handleDirectorySearch,
		"/api/filesystem/directory-search?root="+url.QueryEscape(root)+"&q="+url.QueryEscape(projectDir)+"&limit=10")
	if resp.Root != filepath.Clean(workspaceDir) {
		t.Fatalf("root = %q, want %q", resp.Root, filepath.Clean(workspaceDir))
	}

	projectCount := 0
	nestedFound := false
	for _, entry := range resp.Entries {
		switch entry.Path {
		case projectDir:
			projectCount++
		case nestedDir:
			nestedFound = true
		}
	}
	if projectCount != 1 {
		t.Fatalf("project path appears %d times in %+v, want once", projectCount, resp.Entries)
	}
	if !nestedFound {
		t.Fatalf("nested project %q not found in %+v", nestedDir, resp.Entries)
	}
}
