package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// ErrFileNotFound means the requested path is not a readable,
// non-ignored regular file of the repository.
var ErrFileNotFound = errors.New("file not found in repository")

// Limits are vars so tests can lower them.
var (
	// MaxListedFiles and MaxListedBytes bound a listing while it is read,
	// so a giant untracked tree cannot be buffered whole.
	MaxListedFiles = 100_000
	MaxListedBytes = 8 << 20
	// MaxFileBytes caps how much of one file is returned.
	MaxFileBytes int64 = 1 << 20
)

// FileList is every tracked or untracked-but-not-ignored file of the
// repository containing a directory (plus ignored ones on request),
// relative to the repository root.
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

// lsFiles lists the files git would show, relative to root, stopping at
// the count and byte budgets. Ignored files, when included, come after
// the rest so a huge ignored tree (node_modules) cannot crowd out real
// files. A pathspec narrows the listing.
func lsFiles(ctx context.Context, root string, ignored bool, pathspec ...string) ([]string, bool, error) {
	files, truncated, err := lsFilesInto(ctx, root, nil, append([]string{"--cached", "--others", "--exclude-standard", "--deduplicate"}, pathspec...))
	if err != nil || truncated || !ignored {
		return files, truncated, err
	}
	return lsFilesInto(ctx, root, files, append([]string{"--others", "--ignored", "--exclude-standard"}, pathspec...))
}

// lsFilesInto appends one ls-files listing to files, sharing the budgets.
func lsFilesInto(ctx context.Context, root string, files, mode []string) (_ []string, truncated bool, err error) {
	args := append([]string{"-C", root, "ls-files", "-z"}, mode...)
	size := 0
	for _, f := range files {
		size += len(f)
	}
	err = gitexec.Command(ctx, args...).Stream(func(r io.Reader) error {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 64<<10)
		sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			if i := bytes.IndexByte(data, 0); i >= 0 {
				return i + 1, data[:i], nil
			}
			if atEOF && len(data) > 0 {
				return len(data), data, nil
			}
			return 0, nil, nil
		})
		size := 0
		for sc.Scan() {
			size += len(sc.Bytes())
			if len(files) == MaxListedFiles || size > MaxListedBytes {
				truncated = true
				return gitexec.ErrStopStream
			}
			files = append(files, sc.Text())
		}
		return sc.Err()
	})
	if err != nil {
		return nil, false, fmt.Errorf("git ls-files: %w", err)
	}
	return files, truncated, nil
}

// ListFiles lists the repository containing dir from its root, with
// ignored files appended when ignored is set.
func ListFiles(ctx context.Context, dir string, ignored bool) (*FileList, error) {
	root, err := ResolveRepoRoot(ctx, dir)
	if err != nil {
		return nil, err
	}
	files, truncated, err := lsFiles(ctx, root, ignored)
	if err != nil {
		return nil, err
	}
	if files == nil {
		files = []string{}
	}
	return &FileList{Root: root, Files: files, Truncated: truncated}, nil
}

// openNoFollow opens rel under root one component at a time, refusing a
// symlink anywhere on the path. The allowlist authorizes a *name*, so
// following a listed link (public.txt -> .env, or into .git) would read
// a file the listing hides. O_NONBLOCK keeps a FIFO from hanging the open.
func openNoFollow(root, rel string) (*os.File, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			_ = unix.Close(fd)
			return nil, ErrFileNotFound
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) {
			return nil, errors.Join(ErrFileNotFound, err)
		}
		if err != nil {
			return nil, err
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), rel), nil
}

// ReadFile reads path (relative to the root of the repository containing
// dir). Only regular files ListFiles would return are readable, reached
// without following symlinks, so git metadata, anything outside the root
// and (unless ignored is set) ignored files such as .env are refused.
func ReadFile(ctx context.Context, dir, path string, ignored bool) (*FileContent, error) {
	root, err := ResolveRepoRoot(ctx, dir)
	if err != nil {
		return nil, err
	}
	// Literal pathspec: no globbing. git rejects paths outside the root
	// with an error, which is a refusal, not an operational failure.
	if strings.HasPrefix(path, "/") || strings.Contains("/"+path+"/", "/../") {
		return nil, ErrFileNotFound
	}
	listed, _, err := lsFiles(ctx, root, ignored, "--", ":(literal)"+path)
	if err != nil {
		return nil, err
	}
	if len(listed) != 1 || listed[0] != path {
		return nil, ErrFileNotFound
	}
	f, err := openNoFollow(root, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
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
