package plugins

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	DescribeTimeout        = 3 * time.Second
	MaxDescribeStderrBytes = 64 << 10
)

var (
	ErrDescribe          = errors.New("plugin describe failed")
	ErrDuplicateID       = errors.New("duplicate plugin identity")
	ErrIdentityChanged   = errors.New("plugin identity changed")
	ErrExecutableChanged = errors.New("plugin executable changed during discovery")
)

// Discovery retains rejected candidates for diagnostics. Only entries with a nil
// Err may be enabled or served; discovery never grants permission to serve.
type Discovery struct {
	Path        string
	Checksum    string
	Description Description
	Err         error
}

func DiscoveryDirectory() (string, error) {
	if dir := os.Getenv("OCMAN_PLUGIN_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ErrDescribe
	}
	return filepath.Join(home, ".local", "share", "ocman", "plugins"), nil
}

// Scan is used at startup and for explicit rescans. knownIDs binds previously
// registered absolute paths to immutable IDs, including removed registrations.
// A missing directory is an empty catalog; other directory errors abort the scan.
func Scan(ctx context.Context, dir string, knownIDs map[string]string) ([]Discovery, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, ErrDescribe
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Discovery{}, nil
	}
	if err != nil {
		return nil, ErrDescribe
	}
	results := make([]Discovery, 0)
	ids := make(map[string][]int)
	// ponytail: sequential describes bound resource use; parallelize only if startup latency warrants it.
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(entry.Name(), "ocman-plugin-") || entry.Name() == "ocman-plugin-" {
			continue
		}
		info, err := entry.Info() // Lstat semantics: never follow a child symlink.
		if err != nil {
			return nil, ErrDescribe
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			continue
		}
		candidate := describe(ctx, filepath.Join(dir, entry.Name()), info)
		if candidate.Description.ID != "" {
			ids[candidate.Description.ID] = append(ids[candidate.Description.ID], len(results))
		}
		if id := knownIDs[candidate.Path]; candidate.Err == nil && id != "" && id != candidate.Description.ID {
			candidate.Err = ErrIdentityChanged
		}
		results = append(results, candidate)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, indexes := range ids {
		if len(indexes) > 1 {
			for _, i := range indexes {
				results[i].Err = ErrDuplicateID
			}
		}
	}
	return results, nil
}

// executableChecksum rechecks regular-file identity around opening and hashing.
// Plugins are trusted native code, not sandboxed; checksum approval is still
// invalidated whenever a replacement or an in-place update is observed.
func executableChecksum(path string, expected os.FileInfo) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || !os.SameFile(info, expected) {
		return "", ErrExecutableChanged
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ErrDescribe
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", ErrExecutableChanged
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", ErrDescribe
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(opened, after) ||
		after.Size() != opened.Size() || after.ModTime() != opened.ModTime() || after.Mode() != opened.Mode() {
		return "", ErrExecutableChanged
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func describe(parent context.Context, path string, info os.FileInfo) Discovery {
	d := Discovery{Path: path}
	d.Checksum, d.Err = executableChecksum(path, info)
	if d.Err != nil {
		return d
	}
	ctx, cancel := context.WithTimeout(parent, DescribeTimeout)
	defer cancel()
	var random [32]byte
	_, _ = rand.Read(random[:])
	token := hex.EncodeToString(random[:])
	cmd := exec.CommandContext(ctx, path, string(ModeDescribe))
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "OCMAN_PLUGIN_TOKEN=" + token}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	// Bound waits even if a descendant leaves an inherited pipe open.
	cmd.WaitDelay = 100 * time.Millisecond
	stdout := &describeOutput{remaining: MaxMessageBytes + 2, keep: true, cancel: cancel}
	stderr := &describeOutput{remaining: MaxDescribeStderrBytes, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if cmd.Process != nil {
		// Clean up descendants even when their parent exits before the deadline.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	switch {
	case stdout.exceeded || stderr.exceeded:
		d.Err = ErrMessageTooLarge
	case ctx.Err() != nil:
		d.Err = ctx.Err()
	case err != nil:
		d.Err = ErrDescribe
	}
	if d.Err != nil {
		return d
	}
	decoder := NewDecoder(&stdout.data)
	message, err := decoder.Decode()
	if err != nil {
		d.Err = ErrInvalidMessage
		return d
	}
	stream, _ := NewStream(ModeDescribe, token, Version{ProtocolMajor, ProtocolMinor}, nil)
	if d.Err = stream.Accept(FromPlugin, message); d.Err != nil {
		return d
	}
	if _, err := decoder.Decode(); !errors.Is(err, io.EOF) {
		d.Err = ErrInvalidMessage
		return d
	}
	d.Description = *message.Hello.Description
	checksum, err := executableChecksum(path, info)
	if err != nil || checksum != d.Checksum {
		d.Err = ErrExecutableChanged
	}
	return d
}

type describeOutput struct {
	remaining int
	keep      bool
	data      bytes.Buffer
	exceeded  bool
	cancel    context.CancelFunc
}

func (w *describeOutput) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		w.exceeded = true
		w.cancel()
		return 0, ErrMessageTooLarge
	}
	w.remaining -= len(p)
	if w.keep {
		return w.data.Write(p)
	}
	return len(p), nil
}
