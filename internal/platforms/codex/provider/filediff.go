package codexprovider

import (
	"strconv"
	"strings"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// fileEntries describes a file-change item in the shape OpenCode's
// apply_patch parts use (state.metadata.files): per file its path, kind,
// unified diff and line counts. The changes panel and the transcript both
// read it. A moved file is listed at its new path.
func fileEntries(changes []codexappserver.FileUpdate) []map[string]any {
	out := make([]map[string]any, 0, len(changes))
	for _, ch := range changes {
		path, kind := ch.Path, ch.Kind.Type
		if ch.Kind.MovePath != "" {
			path, kind = ch.Kind.MovePath, "move"
		}
		body, additions, deletions := unifiedBody(ch)
		from, to := ch.Path, path
		switch ch.Kind.Type {
		case "add":
			from = "/dev/null"
		case "delete":
			to = "/dev/null"
		}
		patch := "Index: " + path + "\n" + strings.Repeat("=", 67) + "\n--- " + from + "\n+++ " + to + "\n" + body
		out = append(out, map[string]any{"filePath": path, "type": kind, "patch": patch, "additions": additions, "deletions": deletions})
	}
	return out
}

// unifiedBody returns a change's hunks and its added and deleted lines.
// Additions and deletions carry the whole file; an update carries a
// unified diff whose file headers precede its first hunk.
func unifiedBody(ch codexappserver.FileUpdate) (string, int, int) {
	content := lines(ch.Diff)
	var b strings.Builder
	switch ch.Kind.Type {
	case "add", "delete":
		sign, header := "+", "@@ -0,0 +1,"+strconv.Itoa(len(content))+" @@\n"
		if ch.Kind.Type == "delete" {
			sign, header = "-", "@@ -1,"+strconv.Itoa(len(content))+" +0,0 @@\n"
		}
		if len(content) == 0 {
			return "", 0, 0
		}
		b.WriteString(header)
		for _, line := range content {
			b.WriteString(sign + line + "\n")
		}
		if sign == "+" {
			return b.String(), len(content), 0
		}
		return b.String(), 0, len(content)
	}
	additions, deletions, inHunk := 0, 0, false
	for _, line := range content {
		inHunk = inHunk || strings.HasPrefix(line, "@@")
		if !inHunk {
			continue
		}
		switch {
		case strings.HasPrefix(line, "@@"):
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
		b.WriteString(line + "\n")
	}
	return b.String(), additions, deletions
}
