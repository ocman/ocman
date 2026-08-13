package share

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// RelayAllocation is the relay-side identity and credential returned
// when creating a share. The delete token authorises both PUT and DELETE.
//
// The relay also echoes its limits so a writer can size chunks against
// the server it is actually talking to instead of assuming a default
// that a differently-configured relay would reject.
type RelayAllocation struct {
	ID            string `json:"id"`
	DeleteToken   string `json:"deleteToken"`
	MaxChunkBytes int64  `json:"maxChunkBytes"`
	MaxChunks     int    `json:"maxChunks"`
	MaxShareBytes int64  `json:"maxShareBytes"`
}

// RelayClient writes sealed chunks to a share relay.
type RelayClient struct {
	BaseURL string
	HTTP    *http.Client
}

func (c RelayClient) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Create allocates an empty relay share.
func (c RelayClient) Create(ctx context.Context) (RelayAllocation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/s", nil)
	if err != nil {
		return RelayAllocation{}, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return RelayAllocation{}, relayTransportError("creating", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return RelayAllocation{}, relayStatusError("creating", resp)
	}
	var out RelayAllocation
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return RelayAllocation{}, fmt.Errorf("decoding relay allocation: %w", err)
	}
	if out.ID == "" || out.DeleteToken == "" {
		return RelayAllocation{}, fmt.Errorf("relay returned an incomplete allocation")
	}
	return out, nil
}

// Put stores one already-sealed chunk at a writer-chosen sequence.
func (c RelayClient) Put(ctx context.Context, allocation RelayAllocation, seq uint64, ciphertext []byte) error {
	u := strings.TrimRight(c.BaseURL, "/") + "/s/" + allocation.ID + "/" + strconv.FormatUint(seq, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(ciphertext))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+allocation.DeleteToken)
	resp, err := c.client().Do(req)
	if err != nil {
		return relayTransportError("uploading", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return relayStatusError("uploading", resp)
	}
	return nil
}

// Delete revokes a relay share.
func (c RelayClient) Delete(ctx context.Context, allocation RelayAllocation) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(c.BaseURL, "/")+"/s/"+allocation.ID, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+allocation.DeleteToken)
	resp, err := c.client().Do(req)
	if err != nil {
		return relayTransportError("deleting", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return relayStatusError("deleting", resp)
	}
	return nil
}

// RelayError reports a failed relay request. Status is 0 when the
// request never reached the relay (DNS, refused connection, timeout),
// which is the difference between "the relay is down" and "the relay
// said no" — the two need different advice, so callers must be able to
// tell them apart rather than seeing one opaque error.
type RelayError struct {
	// Op is the attempted operation: "creating", "uploading", "deleting".
	Op string
	// Status is the relay's HTTP status, or 0 when there was no response.
	Status int
	// Message is the relay's response body, trimmed.
	Message string
	// Err is the underlying transport error, when any.
	Err error
}

func (e *RelayError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("%s relay share: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("%s relay share: status %d: %s", e.Op, e.Status, e.Message)
}

func (e *RelayError) Unwrap() error { return e.Err }

// Unreachable reports whether the relay could not be contacted at all.
func (e *RelayError) Unreachable() bool { return e.Status == 0 }

func relayStatusError(action string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return &RelayError{
		Op:      action,
		Status:  resp.StatusCode,
		Message: strings.TrimSpace(string(body)),
	}
}

// relayTransportError wraps a request that never got a response.
func relayTransportError(action string, err error) error {
	return &RelayError{Op: action, Err: err}
}
