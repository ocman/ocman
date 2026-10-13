package filesystem

// Directory browse + fuzzy search backing the directory picker
// (new-session and worktree dialogs). Reads the hub's own disk.

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/httpx"
)

type directoryBrowseEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Hidden bool   `json:"hidden,omitempty"`
}

type directoryBrowseResponse struct {
	Directory string                 `json:"directory"`
	Parent    string                 `json:"parent,omitempty"`
	Home      string                 `json:"home,omitempty"`
	Entries   []directoryBrowseEntry `json:"entries"`
}

type directorySearchEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Hidden  bool   `json:"hidden,omitempty"`
	Project bool   `json:"project,omitempty"`
	Depth   int    `json:"depth,omitempty"`
}

type directorySearchResponse struct {
	Root    string                 `json:"root"`
	Query   string                 `json:"query"`
	Entries []directorySearchEntry `json:"entries"`
}

func (m *Module) handleDirectories(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	home, _ := os.UserHomeDir()
	if dir == "" {
		dir = home
	}
	dir = expandHomePath(dir, home)
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			dir = string(filepath.Separator)
		}
	}
	if !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return
	}

	clean := filepath.Clean(dir)
	info, err := os.Stat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "directory not found", http.StatusNotFound)
			return
		}
		if os.IsPermission(err) {
			http.Error(w, "directory is not readable", http.StatusForbidden)
			return
		}
		httpx.ServerError(w, "stat directory", err)
		return
	}
	if !info.IsDir() {
		http.Error(w, "dir must be a directory", http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(clean)
	if err != nil {
		if os.IsPermission(err) {
			http.Error(w, "directory is not readable", http.StatusForbidden)
			return
		}
		httpx.ServerError(w, "reading directory", err)
		return
	}

	out := make([]directoryBrowseEntry, 0, len(entries))
	for _, entry := range entries {
		fullPath := filepath.Join(clean, entry.Name())
		isDir := entry.IsDir()
		if !isDir && entry.Type()&os.ModeSymlink != 0 {
			if target, err := os.Stat(fullPath); err == nil {
				isDir = target.IsDir()
			}
		}
		if !isDir {
			continue
		}
		out = append(out, directoryBrowseEntry{
			Name:   entry.Name(),
			Path:   fullPath,
			Hidden: strings.HasPrefix(entry.Name(), "."),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hidden != out[j].Hidden {
			return !out[i].Hidden
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	parent := filepath.Dir(clean)
	if parent == clean {
		parent = ""
	}
	httpx.WriteJSON(w, directoryBrowseResponse{
		Directory: clean,
		Parent:    parent,
		Home:      home,
		Entries:   out,
	})
}

func (m *Module) handleDirectorySearch(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(r.URL.Query().Get("root"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if err != nil {
		limit = 50
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	home, _ := os.UserHomeDir()
	if root == "" {
		root = home
	}
	root = expandHomePath(root, home)
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			root = string(filepath.Separator)
		}
	}
	if !filepath.IsAbs(root) {
		http.Error(w, "root must be an absolute path", http.StatusBadRequest)
		return
	}

	cleanRoot := filepath.Clean(root)
	info, err := os.Stat(cleanRoot)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "root not found", http.StatusNotFound)
			return
		}
		if os.IsPermission(err) {
			http.Error(w, "root is not readable", http.StatusForbidden)
			return
		}
		httpx.ServerError(w, "stat search root", err)
		return
	}
	if !info.IsDir() {
		http.Error(w, "root must be a directory", http.StatusBadRequest)
		return
	}

	searchRoot, searchQuery, exact := directorySearchRootAndQuery(cleanRoot, query, home)
	if searchRoot != cleanRoot {
		if info, err := os.Stat(searchRoot); err != nil || !info.IsDir() {
			searchRoot = cleanRoot
			searchQuery = query
			exact = ""
		}
	}
	entries, err := searchDirectories(searchRoot, searchQuery, limit, exact)
	if err != nil {
		if os.IsPermission(err) {
			http.Error(w, "root is not readable", http.StatusForbidden)
			return
		}
		httpx.ServerError(w, "search directories", err)
		return
	}
	httpx.WriteJSON(w, directorySearchResponse{
		Root:    searchRoot,
		Query:   query,
		Entries: entries,
	})
}

func expandHomePath(path, home string) string {
	if path == "~" && home != "" {
		return home
	}
	if strings.HasPrefix(path, "~/") && home != "" {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}
