package filesystem

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
			if depth < maxDepth {
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
	searchText := strings.NewReplacer("/", " ", "\\", " ").Replace(relLower)
	if !fuzzySubsequence(strings.Join(tokens, " "), searchText) {
		return 0, false
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
		if token != "" && fuzzySubsequence(token, nameLower) {
			return true
		}
	}
	return false
}

func fuzzySubsequence(query, text string) bool {
	q := []rune(strings.ToLower(query))
	i := 0
	for _, char := range strings.ToLower(text) {
		if i < len(q) && char == q[i] {
			i++
		}
	}
	return i == len(q)
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
