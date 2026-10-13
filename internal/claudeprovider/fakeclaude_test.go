package claudeprovider

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
)

// The fake CLI rejects stdin the real CLI cannot use, so a protocol bug in
// ocman fails the replay instead of passing silently (NFR-6).
func TestFakeCLIRejectsMalformedInput(t *testing.T) {
	binary := claudetest.Binary(t)
	control := filepath.Join(t.TempDir(), "control.jsonl")
	if err := os.WriteFile(control, []byte(`{"direction":"stdin","message":{"type":"control_request"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	interrupt := filepath.Join(t.TempDir(), "interrupt.jsonl")
	if err := os.WriteFile(interrupt, []byte(`{"direction":"stdin","message":{"type":"control_request","request":{"subtype":"interrupt"}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(t.TempDir(), "user.jsonl")
	if err := os.WriteFile(user, []byte(`{"direction":"stdin","message":{"type":"user"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ fixture, stdin, want string }{
		"not json":               {user, "hello\n", "malformed stdin line"},
		"user without content":   {user, `{"type":"user","message":{"role":"user","content":[]}}` + "\n", "without user content"},
		"user wrong role":        {user, `{"type":"user","message":{"role":"assistant","content":"x"}}` + "\n", "without user content"},
		"control without id":     {control, `{"type":"control_request","request":{"subtype":"interrupt"}}` + "\n", "without request_id"},
		"control without body":   {control, `{"type":"control_request","request_id":"r"}` + "\n", "without request_id"},
		"initialize bad lists":   {control, `{"type":"control_request","request_id":"r","request":{"subtype":"initialize","sdkMcpServers":{}}}` + "\n", "must be arrays of strings"},
		"unknown message type":   {user, `{"type":"assistant","message":{"role":"user","content":"x"}}` + "\n", "unknown stdin message type"},
		"other subtype":          {interrupt, `{"type":"control_request","request_id":"r","request":{"subtype":"account_info"}}` + "\n", `subtype "account_info", expected "interrupt"`},
		"recorded subtype ok":    {interrupt, `{"type":"control_request","request_id":"r","request":{"subtype":"interrupt"}}` + "\n", ""},
		"unsupported subtype ok": {control, `{"type":"control_request","request_id":"r","request":{"subtype":"account_info"}}` + "\n", ""},
		"well formed user ok":    {user, `{"type":"user","message":{"role":"user","content":"hi"},"uuid":"u"}` + "\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := claudetest.Command(binary, tc.fixture, "-p")
			cmd.Stdin = strings.NewReader(tc.stdin)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			var exit *exec.ExitError
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("well-formed input rejected: %v %s", err, stderr.String())
			case tc.want != "" && (!errors.As(err, &exit) || exit.ExitCode() != 3 || !strings.Contains(stderr.String(), tc.want)):
				t.Fatalf("err = %v, stderr = %q; want exit 3 with %q", err, stderr.String(), tc.want)
			}
			if name == "unsupported subtype ok" && !strings.Contains(stdout.String(), "Unsupported control request subtype: account_info") {
				t.Fatalf("stdout = %q", stdout.String())
			}
		})
	}
}
