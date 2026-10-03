package server

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

type attachmentOwnerHost struct {
	hostsvc.Host
	id      string
	request hostsvc.ComposerAttachmentRequest
	data    []byte
}

func (h *attachmentOwnerHost) RemoteID() string { return h.id }
func (h *attachmentOwnerHost) SaveComposerAttachment(_ context.Context, req hostsvc.ComposerAttachmentRequest, reader io.Reader) (*hostsvc.ComposerAttachment, error) {
	h.request = req
	var err error
	h.data, err = io.ReadAll(reader)
	return &hostsvc.ComposerAttachment{Path: "/remote-cache/note.txt", Name: "note.txt", Mime: req.Mime, Size: int64(len(h.data))}, err
}

func TestSessionAttachmentSavesOnExplicitOwner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv, reg := newSessionsTestServer(t)
	local, owner := &attachmentOwnerHost{id: "local"}, &attachmentOwnerHost{id: "box"}
	srv.hostRouter = hostsvc.NewRouter(local)
	srv.hostRouter.RegisterRemote("box", owner)
	// Both machines have the same path and session id; the platform chooses one.
	for _, platform := range []string{"opencode", "r-box:opencode"} {
		reg.Register(&fakePlatformWithDetail{fakePlatform: fakePlatform{id: platform}, detailSession: &db.Session{ID: "s1", Platform: platform, Directory: "/repo"}})
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "note.txt")
	_, _ = io.WriteString(part, "owned by the build box")
	_ = writer.Close()
	r := httptest.NewRequest(http.MethodPost, "/api/session/s1/attachment?platform=r-box%3Aopencode", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	srv.dispatchSessionSubpath(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"path":"/remote-cache/note.txt"`) || string(owner.data) != "owned by the build box" {
		t.Fatalf("remote bytes=%q response=%s", owner.data, w.Body.String())
	}
	if owner.request.Directory != "/repo" || owner.request.SessionID != "s1" || local.data != nil {
		t.Fatalf("wrong owner: %+v local bytes=%q", owner.request, local.data)
	}
	if _, err := os.Stat(composerAttachmentRoot()); !os.IsNotExist(err) {
		t.Fatalf("hub must not save attachment bytes: %v", err)
	}
}
