package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// ErrFileNotFound means the requested path is not a readable,
// non-ignored file of the repository.
var ErrFileNotFound = errors.New("file not found in repository")

const (
	// MaxListedFiles caps a listing so a giant monorepo cannot produce an
	// unbounded response.
	MaxListedFiles = 100_000
	// MaxFileBytes caps how much of one file is returned.
	MaxFileBytes = 1 << 20
)

// FileList is every tracked or untracked-but-not-ignored file of the
// repository containing a directory, relative to the repository root.
type FileList struct {
	Root      string   `json:"root"`
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated,omitempty"`
}

// FileContent is one file of a repository, capped at MaxFileBytes.
type FileContent struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Binary    bool   `json:"binary,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// lsFiles lists the files git would show, relative to root. Extra args
// (a pathspec) narrow the listing.
func lsFiles(ctx context.Context, root string, extra ...string) ([]string, error) {
	args := append([]string{"-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--deduplicate"}, extra...)
	out, err := gitexec.Command(ctx, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// ListFiles lists the repository containing dir from its root.
func ListFiles(ctx context.Context, dir string) (*FileList, error) {
	root, err := ResolveRepoRoot(ctx, dir)
	if err != nil {
		return nil, err
	}
	files, err := lsFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	list := &FileList{Root: root, Files: files}
	if len(files) > MaxListedFiles {
		list.Files, list.Truncated = files[:MaxListedFiles], true
	}
	if list.Files == nil {
		list.Files = []string{}
	}
	return list, nil
}

// ReadFile reads path (relative to the root of the repository containing
// dir). Only files ListFiles would return are readable, and the read
// cannot leave the root, so ignored secrets (.env) and symlink escapes
// are refused.
func ReadFile(ctx context.Context, dir, path string) (*FileContent, error) {
	root, err := ResolveRepoRoot(ctx, dir)
	if err != nil {
		return nil, err
	}
	// Literal pathspec: no globbing, and git refuses paths outside root.
	listed, err := lsFiles(ctx, root, "--", ":(literal)"+path)
	if err != nil || len(listed) != 1 || listed[0] != path {
		return nil, ErrFileNotFound
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	f, err := r.Open(path)
	if err != nil {
		return nil, errors.Join(ErrFileNotFound, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrFileNotFound
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes))
	if err != nil {
		return nil, err
	}
	out := &FileContent{Path: path, Size: info.Size(), Truncated: info.Size() > MaxFileBytes}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		out.Binary = true
		return out, nil
	}
	out.Content = string(data)
	return out, nil
}
