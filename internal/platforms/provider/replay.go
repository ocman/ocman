package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/url"
	"strconv"

	"github.com/ocman/ocman/internal/providerprotocol"
)

// AcknowledgeTurn is called only after the headless lifecycle/event handlers
// applied the terminal event. Browsers never acknowledge dashboard delivery.
func (a *Adapter) AcknowledgeTurn(ctx context.Context, id string, sequence uint64) error {
	return a.call(ctx, a.hello.Capabilities.Replay, "POST", sessionPath(id, "/ack"), providerprotocol.TurnAck{Sequence: sequence}, nil)
}

func (a *Adapter) acknowledge(ctx context.Context, id string, sequence uint64) error {
	a.mu.Lock()
	if a.acks == nil {
		a.acks = make(map[string]uint64)
	}
	a.acks[id] = sequence
	a.mu.Unlock()
	err := a.AcknowledgeTurn(ctx, id, sequence)
	if err == nil {
		a.mu.Lock()
		if a.acks[id] == sequence {
			delete(a.acks, id)
		}
		a.mu.Unlock()
	}
	return err
}

func (a *Adapter) retryAcknowledgments(ctx context.Context) {
	a.mu.RLock()
	acks := maps.Clone(a.acks)
	a.mu.RUnlock()
	for id, seq := range acks {
		_ = a.acknowledge(ctx, id, seq)
	}
}

// proxySnapshot refreshes the browser with current v1 upserts, then continues
// at that snapshot's cursor. The lease closes the snapshot-to-stream race even
// if the headless consumer acknowledges and prunes in between. No shared cursor
// is stored for browsers: simultaneous tabs each get their own snapshot lease.
func (a *Adapter) proxySnapshot(ctx context.Context, id string, w io.Writer, flush func()) error {
	var snapshot providerprotocol.Snapshot
	if err := a.call(ctx, true, "GET", sessionPath(id, "/snapshot"), nil, &snapshot); err != nil {
		return err
	}
	if snapshot.Lease == "" {
		return fmt.Errorf("provider snapshot omitted lease")
	}
	limited := make(map[string]bool)
	for _, event := range snapshot.Events {
		event, err := a.exactEvent(event)
		if err != nil {
			return err
		}
		if _, ruled := a.ruleDecision(event); ruled != "" {
			continue
		}
		if event.Type == "session.status" {
			var props struct {
				SessionID string `json:"sessionID"`
				Status    struct {
					Type string `json:"type"`
				} `json:"status"`
			}
			if err := json.Unmarshal(event.Properties, &props); err != nil {
				return err
			}
			limited[props.SessionID] = props.Status.Type == "limited"
		}
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		if flush != nil {
			flush()
		}
	}
	query := url.Values{"after": {strconv.FormatUint(snapshot.Cursor, 10)}, "lease": {snapshot.Lease}}
	return a.streamURL(ctx, id, snapshot.Cursor, w, flush, "/events?"+query.Encode(), false, limited)
}
