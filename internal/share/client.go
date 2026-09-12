package share

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

type InboxAllocation struct {
	ID                  string `json:"id"`
	IngestionURL        string `json:"ingestionUrl"`
	ManagementToken     string `json:"managementToken"`
	FetchToken          string `json:"fetchToken"`
	AcknowledgmentToken string `json:"acknowledgmentToken"`
	KeyVersion          int    `json:"keyVersion"`
}

type InboxDelivery struct {
	ID string `json:"id"`
}
type InboxDeliveryPage struct {
	Deliveries []InboxDelivery `json:"deliveries"`
	Cursor     string          `json:"cursor"`
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

func (c RelayClient) RegisterInbox(ctx context.Context, recipient, enrollmentToken string) (InboxAllocation, error) {
	body, err := json.Marshal(map[string]string{"recipient": recipient})
	if err != nil {
		return InboxAllocation{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/inboxes", bytes.NewReader(body))
	if err != nil {
		return InboxAllocation{}, err
	}
	req.Header.Set("Authorization", "Bearer "+enrollmentToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return InboxAllocation{}, relayTransportError("registering", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return InboxAllocation{}, relayStatusError("registering", resp)
	}
	var out InboxAllocation
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return InboxAllocation{}, fmt.Errorf("decoding inbox registration: %w", err)
	}
	return out, nil
}

func (c RelayClient) ListInboxDeliveries(ctx context.Context, inboxID, token, cursor string) (InboxDeliveryPage, error) {
	u := strings.TrimRight(c.BaseURL, "/") + "/inboxes/" + inboxID + "/deliveries"
	if cursor != "" {
		u += "?cursor=" + url.QueryEscape(cursor)
	}
	return c.inboxJSON(ctx, http.MethodGet, u, token, InboxDeliveryPage{})
}

func (c RelayClient) FetchInboxDelivery(ctx context.Context, inboxID, deliveryID, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/inboxes/"+inboxID+"/deliveries/"+deliveryID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, relayTransportError("fetching", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, relayStatusError("fetching", resp)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

func (c RelayClient) AcknowledgeInboxDelivery(ctx context.Context, inboxID, deliveryID, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/inboxes/"+inboxID+"/deliveries/"+deliveryID+"/ack", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.client().Do(req)
	if err != nil {
		return relayTransportError("acknowledging", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return relayStatusError("acknowledging", resp)
	}
	return nil
}

func (c RelayClient) inboxJSON(ctx context.Context, method, endpoint, token string, out InboxDeliveryPage) (InboxDeliveryPage, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.client().Do(req)
	if err != nil {
		return out, relayTransportError("listing", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, relayStatusError("listing", resp)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
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
