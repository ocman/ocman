// Package composerattachments owns attachment cache writes and cleanup.
package composerattachments

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	log "github.com/sirupsen/logrus"
)

const MaxBytes = 100 << 20

var ErrTooLarge = errors.New("attachment exceeds 100 MB")

func Root() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "ocman", "composer-attachments")
}

func Save(ctx context.Context, req hostsvc.ComposerAttachmentRequest, reader io.Reader) (*hostsvc.ComposerAttachment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(req.Directory) || req.SessionID == "" {
		return nil, errors.New("attachment requires an absolute directory and session id")
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(req.Directory))
	root := filepath.Join(Root(), strconv.FormatUint(hash.Sum64(), 36), safeName(req.SessionID))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	name := safeName(req.Name)
	path := filepath.Join(root, fmt.Sprintf("%d-%s", time.Now().UnixNano(), name))
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	size, copyErr := io.Copy(out, io.LimitReader(reader, MaxBytes+1))
	err = errors.Join(copyErr, out.Close(), ctx.Err())
	if size > MaxBytes {
		err = ErrTooLarge
	}
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	mime := req.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}
	return &hostsvc.ComposerAttachment{Path: path, Name: name, Mime: mime, Size: size}, nil
}

func safeName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == ".." || name == string(filepath.Separator) || name == "" {
		return "attachment"
	}
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "attachment"
	}
	return b.String()
}

// Sweep removes expired uploads and prunes empty directories. Best effort:
// one unreadable directory must not prevent cleanup of the others.
func Sweep(root string, ttl time.Duration) int {
	projects, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-ttl)
	removed := 0
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		projectDir := filepath.Join(root, project.Name())
		sessions, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, session := range sessions {
			if !session.IsDir() {
				continue
			}
			sessionDir := filepath.Join(projectDir, session.Name())
			entries, err := os.ReadDir(sessionDir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				info, err := entry.Info()
				if err != nil || info.ModTime().After(cutoff) {
					continue
				}
				path := filepath.Join(sessionDir, entry.Name())
				if err := os.Remove(path); err != nil {
					log.WithError(err).WithField("path", path).Warn("sweeping composer attachment")
					continue
				}
				removed++
			}
			_ = os.Remove(sessionDir)
		}
		_ = os.Remove(projectDir)
	}
	return removed
}
