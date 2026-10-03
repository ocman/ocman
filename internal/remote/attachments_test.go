package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/composerattachments"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type attachmentWireHost struct {
	hostsvc.Host
	request hostsvc.ComposerAttachmentRequest
	data    []byte
	err     error
}

func (h *attachmentWireHost) SaveComposerAttachment(_ context.Context, req hostsvc.ComposerAttachmentRequest, reader io.Reader) (*hostsvc.ComposerAttachment, error) {
	h.request = req
	var err error
	h.data, err = io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if h.err != nil {
		return nil, h.err
	}
	return &hostsvc.ComposerAttachment{Path: "/owner/cache/note.txt", Name: req.Name, Mime: req.Mime, Size: int64(len(h.data))}, nil
}

func attachmentWire(t *testing.T, owner hostsvc.Host) pb.OcmanClient {
	t.Helper()
	return pb.NewOcmanClient(startTestServer(t, "token", NewServer(platforms.NewRegistry(), owner, "box", "test")))
}

func TestComposerAttachmentStreamsAboveUnaryLimit(t *testing.T) {
	owner := &attachmentWireHost{}
	host := newRemoteHost(&RemoteConn{client: attachmentWire(t, owner)})
	data := bytes.Repeat([]byte("file bytes\x00"), 500_000)
	request := hostsvc.ComposerAttachmentRequest{Directory: "/repo", SessionID: "s1", Name: "note.txt", Mime: "text/plain"}
	saved, err := host.SaveComposerAttachment(t.Context(), request, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if owner.request != request || !bytes.Equal(owner.data, data) || saved.Path != "/owner/cache/note.txt" || saved.Size != int64(len(data)) {
		t.Fatalf("metadata=%+v saved=%+v owner bytes=%d", owner.request, saved, len(owner.data))
	}
}

func TestComposerAttachmentStreamRejectsInvalidPackets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		packets [][]byte
		code    codes.Code
	}{
		{"bad metadata", [][]byte{[]byte("not json")}, codes.InvalidArgument},
		{"large metadata", [][]byte{make([]byte, attachmentChunkBytes+1)}, codes.InvalidArgument},
		{"large chunk", [][]byte{[]byte(`{"directory":"/repo","sessionId":"s1"}`), make([]byte, attachmentChunkBytes+1)}, codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := &attachmentWireHost{}
			stream, err := attachmentWire(t, owner).SaveComposerAttachment(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, packet := range tc.packets {
				_ = stream.Send(&pb.JsonReq{Payload: packet})
			}
			_, err = stream.CloseAndRecv()
			if status.Code(err) != tc.code {
				t.Fatalf("error=%v want %v", err, tc.code)
			}
		})
	}
}

func TestComposerAttachmentStreamPropagatesOwnerLimit(t *testing.T) {
	owner := &attachmentWireHost{err: composerattachments.ErrTooLarge}
	host := newRemoteHost(&RemoteConn{client: attachmentWire(t, owner)})
	_, err := host.SaveComposerAttachment(t.Context(), hostsvc.ComposerAttachmentRequest{Directory: "/repo", SessionID: "s1"}, bytes.NewReader(nil))
	if !errors.Is(err, composerattachments.ErrTooLarge) {
		t.Fatalf("error=%v", err)
	}
	_, err = newRemoteHost(&RemoteConn{}).SaveComposerAttachment(t.Context(), hostsvc.ComposerAttachmentRequest{}, bytes.NewReader(nil))
	if !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("offline error=%v", err)
	}
}

type interruptedUpload struct {
	read chan struct{}
	sent bool
	ctx  context.Context
}

func (r *interruptedUpload) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "partial file"), nil
	}
	select {
	case <-r.read:
		return 0, errors.New("source read failed")
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}

type observedReader struct {
	io.Reader
	read chan struct{}
	once sync.Once
}

func (r *observedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.once.Do(func() { close(r.read) })
	}
	return n, err
}

type observedAttachmentHost struct {
	hostsvc.Host
	read chan struct{}
	done chan error
}

func (h *observedAttachmentHost) SaveComposerAttachment(ctx context.Context, req hostsvc.ComposerAttachmentRequest, reader io.Reader) (*hostsvc.ComposerAttachment, error) {
	saved, err := h.Host.SaveComposerAttachment(ctx, req, &observedReader{Reader: reader, read: h.read})
	h.done <- err
	return saved, err
}

func TestComposerAttachmentInterruptedStreamRemovesOwnerPartialFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	owner := &observedAttachmentHost{Host: local.New(local.Deps{}), read: make(chan struct{}), done: make(chan error, 1)}
	host := newRemoteHost(&RemoteConn{client: attachmentWire(t, owner)})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := host.SaveComposerAttachment(ctx, hostsvc.ComposerAttachmentRequest{Directory: "/repo", SessionID: "s1", Name: "note.txt"},
		&interruptedUpload{read: owner.read, ctx: ctx})
	if err == nil || err.Error() != "source read failed" {
		t.Fatalf("source error=%v", err)
	}
	select {
	case err := <-owner.done:
		if err == nil {
			t.Fatal("interrupted owner write succeeded")
		}
	case <-ctx.Done():
		t.Fatal("owner did not stop after client cancellation")
	}
	_ = filepath.WalkDir(composerattachments.Root(), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			t.Errorf("owner partial file remains: %s", path)
		}
		return err
	})
}
