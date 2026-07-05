// Filesystem directory browse + fuzzy search backing the directory
// picker (new-session and worktree dialogs).

package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

func (s *Server) handleFilesystemDirectories(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	home, _ := os.UserHomeDir()
	if dir == "" {
		dir = home
	}
	if dir == "~" && home != "" {
		dir = home
	} else if strings.HasPrefix(dir, "~/") && home != "" {
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
	}
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
		serverError(w, "stat directory", err)
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
		serverError(w, "reading directory", err)
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
	writeJSON(w, directoryBrowseResponse{
		Directory: clean,
		Parent:    parent,
		Home:      home,
		Entries:   out,
	})
}

func (s *Server) handleFilesystemDirectorySearch(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(r.URL.Query().Get("root"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := parseIntParam(r, "limit", 50)
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
		serverError(w, "stat search root", err)
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
		serverError(w, "search directories", err)
		return
	}
	writeJSON(w, directorySearchResponse{
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

func directorySearchRootAndQuery(root, query, home string) (string, string, string) {
	if query == "" {
		return root, query, ""
	}
	expanded := expandHomePath(query, home)
	if !filepath.IsAbs(expanded) {
		return root, query, ""
	}

	clean := filepath.Clean(expanded)
	if info, err := os.Stat(clean); err == nil && info.IsDir() {
		return filepath.Dir(clean), filepath.Base(clean), clean
	}
	parent := filepath.Dir(clean)
	base := filepath.Base(clean)
	if strings.HasSuffix(query, "/") {
		parent = clean
		base = ""
	}
	return parent, base, ""
}

func searchDirectories(root, query string, limit int, exact string) ([]directorySearchEntry, error) {
	query = strings.TrimSpace(query)
	if query == "" && exact == "" {
		return nil, nil
	}

	type queuedDir struct {
		path  string
		depth int
	}
	type scoredEntry struct {
		entry directorySearchEntry
		score int
	}

	const maxDepth = 6
	const maxVisited = 2500
	maxCandidates := limit * 4
	if maxCandidates < limit {
		maxCandidates = limit
	}

	tokens := directorySearchTokens(query)
	includeHidden := strings.Contains(query, ".")
	seenDirs := map[string]struct{}{root: {}}
	emitted := make(map[string]struct{})
	queue := []queuedDir{{path: root}}
	candidates := make([]scoredEntry, 0, limit)
	visited := 0

	if exact != "" {
		exact = filepath.Clean(exact)
		candidates = append(candidates, scoredEntry{
			entry: directorySearchEntry{
				Name:    filepath.Base(exact),
				Path:    exact,
				Hidden:  pathHasHiddenComponent(exact),
				Project: isLikelyProjectDirectory(exact),
			},
			score: -100,
		})
		emitted[exact] = struct{}{}
	}

	for len(queue) > 0 && visited < maxVisited {
		current := queue[0]
		queue = queue[1:]
		visited++

		entries, err := os.ReadDir(current.path)
		if err != nil {
			if current.path == root {
				return nil, err
			}
			continue
		}
		sort.Slice(entries, func(i, j int) bool {
			ri := directorySearchVisitRank(entries[i].Name(), tokens)
			rj := directorySearchVisitRank(entries[j].Name(), tokens)
			if ri != rj {
				return ri < rj
			}
			return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
		})

		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			name := entry.Name()
			if shouldSkipDirectorySearchEntry(name, includeHidden) {
				continue
			}
			fullPath := filepath.Join(current.path, name)
			cleanPath := filepath.Clean(fullPath)
			if _, ok := seenDirs[cleanPath]; ok {
				continue
			}
			seenDirs[cleanPath] = struct{}{}

			rel, err := filepath.Rel(root, cleanPath)
			if err != nil {
				rel = name
			}
			depth := current.depth + 1
			if score, ok := directorySearchScore(rel, name, query, depth); ok {
				if _, exists := emitted[cleanPath]; !exists {
					project := isLikelyProjectDirectory(cleanPath)
					if project {
						score -= 15
					}
					candidates = append(candidates, scoredEntry{
						entry: directorySearchEntry{
							Name:    name,
							Path:    cleanPath,
							Hidden:  pathHasHiddenComponent(rel),
							Project: project,
							Depth:   depth,
						},
						score: score,
					})
					emitted[cleanPath] = struct{}{}
				}
			}
			if depth < maxDepth && len(candidates) < maxCandidates {
				if isHeavyDirectorySearchBranch(name) && !directoryNameMatchesAnyToken(name, tokens) {
					continue
				}
				queue = append(queue, queuedDir{path: cleanPath, depth: depth})
			}
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}
		if candidates[i].entry.Depth != candidates[j].entry.Depth {
			return candidates[i].entry.Depth < candidates[j].entry.Depth
		}
		return strings.ToLower(candidates[i].entry.Path) < strings.ToLower(candidates[j].entry.Path)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	out := make([]directorySearchEntry, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.entry)
	}
	return out, nil
}

func directorySearchScore(rel, name, query string, depth int) (int, bool) {
	tokens := directorySearchTokens(query)
	if len(tokens) == 0 {
		return 0, true
	}
	relLower := strings.ToLower(rel)
	nameLower := strings.ToLower(name)
	for _, token := range tokens {
		if !strings.Contains(relLower, token) {
			return 0, false
		}
	}

	score := depth * 5
	queryLower := strings.ToLower(strings.TrimSpace(query))
	switch {
	case nameLower == queryLower:
		score += 0
	case strings.HasPrefix(nameLower, queryLower):
		score += 10
	case strings.Contains(nameLower, queryLower):
		score += 20
	default:
		score += 35
	}
	return score, true
}

func directorySearchTokens(query string) []string {
	query = strings.NewReplacer("/", " ", "\\", " ").Replace(strings.ToLower(query))
	return strings.Fields(query)
}

func shouldSkipDirectorySearchEntry(name string, includeHidden bool) bool {
	if strings.HasPrefix(name, ".") && !includeHidden {
		return true
	}
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", "target", ".cache", ".next", ".turbo", ".pnpm-store":
		return true
	default:
		return false
	}
}

func directorySearchVisitRank(name string, tokens []string) int {
	nameLower := strings.ToLower(name)
	if directoryNameMatchesAnyToken(name, tokens) {
		return 0
	}
	switch nameLower {
	case "workspace", "workspaces", "src", "source", "code", "projects", "dev":
		return 1
	case "library", "applications", "movies", "music", "pictures":
		return 50
	default:
		return 10
	}
}

func directoryNameMatchesAnyToken(name string, tokens []string) bool {
	nameLower := strings.ToLower(name)
	for _, token := range tokens {
		if token != "" && strings.Contains(nameLower, token) {
			return true
		}
	}
	return false
}

func isHeavyDirectorySearchBranch(name string) bool {
	switch strings.ToLower(name) {
	case "library", "applications", "movies", "music", "pictures":
		return true
	default:
		return false
	}
}

func pathHasHiddenComponent(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}

func isLikelyProjectDirectory(dir string) bool {
	for _, marker := range []string{
		".git",
		"go.mod",
		"package.json",
		"pnpm-workspace.yaml",
		"pyproject.toml",
		"Cargo.toml",
		"deno.json",
		"bun.lockb",
		"composer.json",
		"Gemfile",
		"Makefile",
	} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}
