package sessionlifecycle

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/ocman/ocman/internal/composerattachments"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
)

func (m *Module) HandleSessionAttachment(w http.ResponseWriter, r *http.Request) {
	m.d.WithSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
		detail, err := adapter.Session(r.Context(), sessionID, 0, 0)
		if err != nil {
			httpx.WritePlatformError(w, "loading session for attachment", err)
			return
		}
		if detail == nil || detail.Session == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		remoteID, _ := platforms.SplitPlatformID(string(adapter.ID()))
		if remoteID == "" {
			remoteID = "local"
		}
		owner, ok := m.d.Owner(w, detail.Session.Directory, remoteID)
		if !ok {
			return
		}
		m.saveOwnerAttachment(w, r, owner, detail.Session.Directory, sessionID)
	})
}

func (m *Module) draftAttachment(w http.ResponseWriter, r *http.Request) {
	if !m.draftEnabled(w) {
		return
	}
	draft, err := (draftStore{m.d.DB}).get(r.Context(), r.PathValue("id"))
	if err != nil || draft.Phase == "discarded" {
		http.NotFound(w, r)
		return
	}
	var target Target
	if json.Unmarshal(draft.Metadata, &target) != nil || !target.Validate(w) || !target.NormalizeOwner(w) {
		return
	}
	owner, ok := m.d.Owner(w, target.Directory, target.RemoteID)
	if !ok {
		return
	}
	m.saveOwnerAttachment(w, r, owner, target.Directory, draft.ID)
}

func (m *Module) saveOwnerAttachment(w http.ResponseWriter, r *http.Request, owner hostsvc.Host, directory, sessionID string) {
	r.Body = http.MaxBytesReader(w, r.Body, composerattachments.MaxBytes+(64<<10))
	httpx.SetBodyReadDeadline(w, m.d.UploadReadTimeout())
	defer httpx.ClearBodyReadDeadline(w)
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
			Directory: directory, SessionID: sessionID, Name: part.FileName(), Mime: part.Header.Get("Content-Type"),
		}, part)
		if errors.Is(err, composerattachments.ErrTooLarge) {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		if err != nil {
			httpx.WritePlatformError(w, "saving attachment on owner", err)
			return
		}
		httpx.WriteJSON(w, saved)
		return
	}
}
