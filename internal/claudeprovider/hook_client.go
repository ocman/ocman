package claudeprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// HookTimeout bounds `ocman hook` end to end (AD-11).
const HookTimeout = time.Second

// RunHook posts one hook event, read from Claude's hook input on stdin, to
// ocman's loopback listener at addr. It never fails or blocks Claude:
// errors are dropped and the whole call returns within timeout.
func RunHook(addr, event string, stdin io.Reader, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		id := hookSessionID(json.NewDecoder(stdin))
		if id != "" {
			body, _ := json.Marshal(providerprotocol.HookEvent{Event: event, SessionID: id,
				PID: hookClaudePID(ctx), Managed: os.Getenv("OCMAN_MANAGED") == "1"})
			if req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/hook", bytes.NewReader(body)); err == nil {
				req.Header.Set("Content-Type", "application/json")
				if resp, err := http.DefaultClient.Do(req); err == nil {
					_ = resp.Body.Close()
				}
			}
		}
		// Let Claude finish writing its input rather than see a closed pipe.
		_, _ = io.Copy(io.Discard, stdin)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// hookSessionID reads only up to session_id: tool hooks carry the whole
// tool input, which can be large.
func hookSessionID(dec *json.Decoder) string {
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return ""
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return ""
		}
		if key == "session_id" {
			var id string
			_ = dec.Decode(&id)
			return id
		}
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return ""
		}
	}
	return ""
}
