package local

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/gitexec"
	"github.com/ocman/ocman/internal/hostsvc"
)

func projectCloneURL(ctx context.Context, root string) string {
	if root == "" {
		return ""
	}
	raw, err := gitexec.Output(ctx, root, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	safe, _ := hostsvc.SafeCloneURL(raw)
	return safe
}

func (h *Host) PlanProjectClone(ctx context.Context, req hostsvc.ProjectClone) (*hostsvc.ProjectClone, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	safe, err := hostsvc.SafeCloneURL(req.URL)
	if err != nil {
		return nil, err
	}
	req.URL = safe
	if req.Directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		u, _ := url.Parse(safe)
		req.Directory = filepath.Join(home, "src", u.Hostname(), strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git"))
	}
	if !filepath.IsAbs(req.Directory) || strings.ContainsRune(req.Directory, 0) {
		return nil, errors.New("clone destination must be an absolute path")
	}
	req.Directory = filepath.Clean(req.Directory)
	if _, err := os.Lstat(req.Directory); err == nil {
		if !completedClone(ctx, req) {
			return nil, errors.New("clone destination already exists; choose a new directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("checking clone destination: %w", err)
	}
	return &req, nil
}

// CloneProject reserves a new directory before git writes. Only our own completed
// clone may be reused after a later session-start failure; existing paths are never adopted.
func (h *Host) CloneProject(ctx context.Context, req hostsvc.ProjectClone) error {
	plan, err := h.PlanProjectClone(ctx, req)
	if err != nil {
		return err
	}
	req = *plan
	if completedClone(ctx, req) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(req.Directory), 0o755); err != nil {
		return fmt.Errorf("creating clone parent directory: %w", err)
	}
	if err := os.Mkdir(req.Directory, 0o755); err != nil {
		return errors.New("could not reserve clone destination; choose a new directory")
	}
	completed := false
	defer func() {
		if !completed {
			_ = os.RemoveAll(req.Directory)
		} // Only the directory reserved above, never a pre-existing checkout.
	}()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	err = gitexec.Command(ctx, "-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always",
		"-c", "core.hooksPath=/dev/null", "-c", "core.sshCommand=ssh -oBatchMode=yes",
		"clone", "--template=", "--", req.URL, req.Directory).WithEnv("GIT_ALLOW_PROTOCOL=https:ssh").Run()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("cloning repository: %w", ctx.Err())
		}
		return errors.New("clone failed; check repository access and Git credentials on the selected machine")
	}
	if err := os.WriteFile(filepath.Join(req.Directory, ".git", "ocman-clone-url"), []byte(req.URL), 0o600); err != nil {
		return fmt.Errorf("recording completed clone: %w", err)
	}
	completed = true
	return nil
}

func completedClone(ctx context.Context, req hostsvc.ProjectClone) bool {
	info, err := os.Lstat(req.Directory)
	if err != nil || !info.IsDir() {
		return false
	}
	marker, err := os.ReadFile(filepath.Join(req.Directory, ".git", "ocman-clone-url"))
	if err != nil || string(marker) != req.URL {
		return false
	}
	origin, err := gitexec.Output(ctx, req.Directory, "remote", "get-url", "origin")
	return err == nil && origin == req.URL
}
