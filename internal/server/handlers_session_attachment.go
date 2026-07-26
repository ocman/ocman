// Composer attachment upload handler and its filesystem helpers.
package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

const maxComposerAttachmentBytes = 100 << 20

func (s *Server) handleSessionAttachment(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		r.Body = http.MaxBytesReader(w, r.Body, maxComposerAttachmentBytes)
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			http.Error(w, "failed to read attachment", http.StatusBadRequest)
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file is required", http.StatusBadRequest)
			return
		}
		defer file.Close()

		detail, err := adapter.Session(r.Context(), sessionID, 0, 0)
		if err != nil {
			writePlatformError(w, "loading session for attachment", err)
			return
		}
		if detail == nil || detail.Session == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}

		root, err := composerAttachmentDir(detail.Session.Directory, sessionID)
		if err != nil {
			log.WithError(err).Error("resolving composer attachment directory")
			http.Error(w, "failed to prepare attachment directory", http.StatusInternalServerError)
			return
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			log.WithError(err).Error("creating composer attachment directory")
			http.Error(w, "failed to prepare attachment directory", http.StatusInternalServerError)
			return
		}

		name := safeAttachmentName(header.Filename)
		path := filepath.Join(root, fmt.Sprintf("%d-%s", time.Now().UnixNano(), name))
		out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			log.WithError(err).Error("creating composer attachment file")
			http.Error(w, "failed to save attachment", http.StatusInternalServerError)
			return
		}
		size, copyErr := io.Copy(out, file)
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(path)
			if copyErr != nil {
				log.WithError(copyErr).Error("saving composer attachment")
			} else {
				log.WithError(closeErr).Error("closing composer attachment")
			}
			http.Error(w, "failed to save attachment", http.StatusInternalServerError)
			return
		}

		mime := header.Header.Get("Content-Type")
		if mime == "" {
			mime = "application/octet-stream"
		}
		writeJSON(w, map[string]interface{}{
			"path": path,
			"name": name,
			"mime": mime,
			"size": size,
		})
	})
}

func composerAttachmentDir(projectDir, sessionID string) (string, error) {
	projectKey := strconv.FormatUint(fnv64(projectDir), 36)
	return filepath.Join(composerAttachmentRoot(), projectKey, safeAttachmentName(sessionID)), nil
}

// composerAttachmentRoot is the directory every upload lands under.
func composerAttachmentRoot() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "ocman", "composer-attachments")
}

// composerAttachmentTTL is how long an uploaded attachment is kept. The
// file only has to outlive the turn that references it; the agent has
// long since read it by then.
//
// ponytail: a flat age cap, no per-session bookkeeping. Add reference
// counting only if someone reports losing an attachment they still needed.
const composerAttachmentTTL = 7 * 24 * time.Hour

// sweepComposerAttachments deletes attachments older than ttl and prunes
// the directories they leave empty. Nothing else ever removed these, so
// real user content (screenshots, PDFs, logs) grew without bound in a
// cache directory macOS does not purge. Returns the number of files
// removed. Soft-fails on every error: this is best-effort housekeeping.
func sweepComposerAttachments(root string, ttl time.Duration) int {
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
			removed += sweepAttachmentDir(sessionDir, cutoff)
			// Prunes only when empty, so a live session keeps its dir.
			_ = os.Remove(sessionDir)
		}
		_ = os.Remove(projectDir)
	}
	return removed
}

func sweepAttachmentDir(dir string, cutoff time.Time) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			log.WithError(err).WithField("path", filepath.Join(dir, entry.Name())).
				Warn("sweeping composer attachment")
			continue
		}
		removed++
	}
	return removed
}

func safeAttachmentName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "attachment"
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

func fnv64(s string) uint64 {
	const prime uint64 = 1099511628211
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}
