package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

type approvalSignalWriter struct {
	io.Writer
	once sync.Once
	done chan struct{}
}

func (w *approvalSignalWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if bytes.Contains(p, []byte("event: ocman.permission.approved")) {
		w.once.Do(func() { close(w.done) })
	}
	return n, err
}

func TestProxyRemoteSessionEventsEmitsManualApproval(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	approved := make(chan struct{})
	fp := &fakePlatform{
		id: "opencode",
		proxyEventsFn: func(_ context.Context, _ string, w io.Writer, _ func()) error {
			for _, event := range []string{
				"data: {\"type\":\"permission.asked\",\"properties\":{\"id\":\"perm-1\",\"sessionID\":\"ses-1\",\"permission\":\"bash\",\"patterns\":[\"git status\"]}}\n\n",
				"data: {\"type\":\"permission.replied\",\"properties\":{\"sessionID\":\"ses-1\",\"requestID\":\"perm-1\",\"reply\":\"always\"}}\n\n",
			} {
				if _, err := io.WriteString(w, event); err != nil {
					return err
				}
			}
			select {
			case <-approved:
				return nil
			case <-time.After(time.Second):
				return fmt.Errorf("timed out waiting for synthetic approval")
			}
		},
	}
	var raw, synthetic bytes.Buffer
	syntheticWriter := &approvalSignalWriter{Writer: &synthetic, done: approved}
	if err := srv.ProxyRemoteSessionEvents(t.Context(), "opencode", "ses-1", fp, &raw, syntheticWriter, nil); err != nil {
		t.Fatalf("ProxyRemoteSessionEvents: %v", err)
	}
	if !strings.Contains(synthetic.String(), "event: ocman.permission.approved") {
		t.Fatalf("synthetic approval missing from owner stream: %q", synthetic.String())
	}
	if !strings.Contains(raw.String(), "\"type\":\"permission.asked\"") {
		t.Fatalf("raw OpenCode event missing from owner stream: %q", raw.String())
	}
}

func TestProxyRemoteSessionEventsResurrectsPendingPermission(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	fp := &fakePlatform{
		id: "opencode",
		listPermissionsFn: func(sessionID string) ([]platforms.LivePrompt, error) {
			if sessionID != "ses-1" {
				t.Fatalf("ListPermissions session = %q", sessionID)
			}
			return []platforms.LivePrompt{{
				"id": "perm-1", "sessionID": "child-1", "permission": "bash",
				"patterns": []any{"git status"}, "metadata": map[string]any{"command": "git status"},
			}}, nil
		},
		proxyEventsFn: func(_ context.Context, _ string, _ io.Writer, _ func()) error { return nil },
	}
	var raw, synthetic bytes.Buffer

	if err := srv.ProxyRemoteSessionEvents(t.Context(), "opencode", "ses-1", fp, &raw, &synthetic, nil); err != nil {
		t.Fatalf("ProxyRemoteSessionEvents: %v", err)
	}
	if !strings.Contains(synthetic.String(), "event: ocman.permission.pending") {
		t.Fatalf("pending permission state was not replayed: %q", synthetic.String())
	}
}
