package claudeprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/NoUseFreak/ocman/internal/historyjournal"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// HookTimeout bounds `ocman hook` end to end (AD-11).
const HookTimeout = time.Second

// HandleHook reads Claude's hook input once, appends history events to the
// journal in every session, and posts status to addr only when managed. It
// never fails or blocks Claude beyond timeout.
func HandleHook(addr, journal, event string, managed bool, stdin io.Reader, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		in := readHookInput(stdin)
		recordHook(journal, event, in)
		if managed && addr != "" && in.SessionID != "" {
			body, _ := json.Marshal(providerprotocol.HookEvent{Event: event, SessionID: in.SessionID,
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

func recordHook(journal, event string, in hookInput) {
	if journal == "" || in.SessionID == "" || !slices.Contains(HistoryHookEvents, event) {
		return
	}
	// Only the pointer to the transcript is recorded: the transcript itself
	// is Claude's durable, append-only record of the conversation.
	data, _ := json.Marshal(map[string]string{"transcriptPath": in.TranscriptPath, "cwd": in.Cwd})
	_ = historyjournal.Append(journal, "claudecode-"+time.Now().UTC().Format("20060102")+".jsonl",
		historyjournal.Record{Time: time.Now().UnixMilli(), Session: in.SessionID, Type: event, Data: data})
}

type hookInput struct{ SessionID, TranscriptPath, Cwd string }

// readHookInput reads only the few top-level fields ocman needs: tool hooks
// carry whole tool inputs and outputs, which are skipped, not retained.
func readHookInput(r io.Reader) hookInput {
	var in hookInput
	dec := json.NewDecoder(r)
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return hookInput{}
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return hookInput{}
		}
		var target *string
		switch key {
		case "session_id":
			target = &in.SessionID
		case "transcript_path":
			target = &in.TranscriptPath
		case "cwd":
			target = &in.Cwd
		}
		if target != nil {
			_ = dec.Decode(target)
			continue
		}
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return hookInput{}
		}
	}
	return in
}
