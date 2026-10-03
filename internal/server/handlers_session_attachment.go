package server

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/composerattachments"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

const maxComposerAttachmentBytes = composerattachments.MaxBytes

func (s *Server) handleSessionAttachment(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		detail, err := adapter.Session(r.Context(), sessionID, 0, 0)
		if err != nil {
			writePlatformError(w, "loading session for attachment", err)
			return
		}
		if detail == nil || detail.Session == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		owner, ok := s.resolveOwner(w, detail.Session.Directory, remoteIDForPlatform(string(adapter.ID())))
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxComposerAttachmentBytes+(64<<10))
		setBodyReadDeadline(w, uploadReadTimeout)
		defer clearBodyReadDeadline(w)
		form, err := r.MultipartReader()
		if err != nil {
			http.Error(w, "failed to read attachment", http.StatusBadRequest)
			return
		}
		for {
			part, err := form.NextPart()
			if err != nil {
				if errors.Is(err, io.EOF) {
					http.Error(w, "file is required", http.StatusBadRequest)
				} else {
					http.Error(w, "failed to read attachment", http.StatusBadRequest)
				}
				return
			}
			if part.FormName() != "file" || part.FileName() == "" {
				_ = part.Close()
				continue
			}
			defer part.Close()
			saved, err := owner.SaveComposerAttachment(r.Context(), hostsvc.ComposerAttachmentRequest{
				Directory: detail.Session.Directory, SessionID: sessionID, Name: part.FileName(), Mime: part.Header.Get("Content-Type"),
			}, part)
			if errors.Is(err, composerattachments.ErrTooLarge) {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			if err != nil {
				writePlatformError(w, "saving attachment on owner", err)
				return
			}
			writeJSON(w, saved)
			return
		}
	})
}

func composerAttachmentRoot() string { return composerattachments.Root() }

// ponytail: seven-day age cap; reference counting if users need older uploads.
const composerAttachmentTTL = 7 * 24 * time.Hour

func sweepComposerAttachments(root string, ttl time.Duration) int {
	return composerattachments.Sweep(root, ttl)
}
