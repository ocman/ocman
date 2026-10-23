package share

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
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
	SecretHeader        string `json:"secretHeader,omitempty"`
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

// do sends one relay request and returns the response when its status is
// one of ok; the caller must close the body. A non-empty token is sent as a
// bearer credential. body is nil, raw []byte, or a value sent as JSON. op
// names the operation in the returned RelayError.
func (c RelayClient) do(ctx context.Context, op, method, path, token string, body any, ok ...int) (*http.Response, error) {
	var reader io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(b)
	default:
		encoded, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		reader, contentType = bytes.NewReader(encoded), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, relayTransportError(op, err)
	}
	if !slices.Contains(ok, resp.StatusCode) {
		defer resp.Body.Close()
		return nil, relayStatusError(op, resp)
	}
	return resp, nil
}

// discard closes a successful response whose body is not needed.
func discard(resp *http.Response, err error) error {
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// decodeBody decodes at most limit bytes of JSON from resp and closes it.
func decodeBody(resp *http.Response, limit int64, out any) error {
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(out)
}

// Create allocates an empty relay share.
func (c RelayClient) Create(ctx context.Context) (RelayAllocation, error) {
	resp, err := c.do(ctx, "creating", http.MethodPost, "/s", "", nil, http.StatusCreated)
	if err != nil {
		return RelayAllocation{}, err
	}
	var out RelayAllocation
	if err := decodeBody(resp, 64<<10, &out); err != nil {
		return RelayAllocation{}, fmt.Errorf("decoding relay allocation: %w", err)
	}
	if out.ID == "" || out.DeleteToken == "" {
		return RelayAllocation{}, fmt.Errorf("relay returned an incomplete allocation")
	}
	return out, nil
}

// Put stores one already-sealed chunk at a writer-chosen sequence.
func (c RelayClient) Put(ctx context.Context, allocation RelayAllocation, seq uint64, ciphertext []byte) error {
	path := "/s/" + allocation.ID + "/" + strconv.FormatUint(seq, 10)
	return discard(c.do(ctx, "uploading", http.MethodPut, path, allocation.DeleteToken, ciphertext, http.StatusNoContent))
}

// Delete revokes a relay share.
func (c RelayClient) Delete(ctx context.Context, allocation RelayAllocation) error {
	return discard(c.do(ctx, "deleting", http.MethodDelete, "/s/"+allocation.ID, allocation.DeleteToken, nil, http.StatusNoContent, http.StatusNotFound))
}

func (c RelayClient) RegisterInboxWithSecret(ctx context.Context, recipient, enrollmentToken, secret, secretHeader string) (InboxAllocation, error) {
	body := map[string]string{"recipient": recipient, "secret": secret, "secretHeader": secretHeader}
	resp, err := c.do(ctx, "registering", http.MethodPost, "/inboxes", enrollmentToken, body, http.StatusCreated)
	if err != nil {
		return InboxAllocation{}, err
	}
	var out InboxAllocation
	if err := decodeBody(resp, 64<<10, &out); err != nil {
		return InboxAllocation{}, err
	}
	return out, nil
}

func (c RelayClient) RotateInbox(ctx context.Context, id, token, recipient string) (int, error) {
	resp, err := c.do(ctx, "rotating", http.MethodPost, "/inboxes/"+id+"/rotate", token, map[string]string{"recipient": recipient}, http.StatusOK)
	if err != nil {
		return 0, err
	}
	var out struct {
		KeyVersion int `json:"keyVersion"`
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.KeyVersion, nil
}

// UpdateInboxSecret replaces the inbox's shared secret; an empty secret
// clears it. It returns the header the relay will check.
func (c RelayClient) UpdateInboxSecret(ctx context.Context, id, token, secret, secretHeader string) (string, error) {
	body := map[string]string{"secret": secret, "secretHeader": secretHeader}
	resp, err := c.do(ctx, "updating inbox secret", http.MethodPut, "/inboxes/"+id+"/secret", token, body, http.StatusOK)
	if err != nil {
		return "", err
	}
	var out struct {
		SecretHeader string `json:"secretHeader"`
	}
	if err := decodeBody(resp, 64<<10, &out); err != nil {
		return "", err
	}
	return out.SecretHeader, nil
}

func (c RelayClient) RevokeInbox(ctx context.Context, id, token string) error {
	return discard(c.do(ctx, "revoking", http.MethodDelete, "/inboxes/"+id, token, nil, http.StatusNoContent, http.StatusNotFound))
}

func (c RelayClient) ListInboxDeliveries(ctx context.Context, inboxID, token, cursor string) (InboxDeliveryPage, error) {
	path := "/inboxes/" + inboxID + "/deliveries"
	if cursor != "" {
		path += "?cursor=" + url.QueryEscape(cursor)
	}
	var out InboxDeliveryPage
	resp, err := c.do(ctx, "listing", http.MethodGet, path, token, nil, http.StatusOK)
	if err != nil {
		return out, err
	}
	return out, decodeBody(resp, 256<<10, &out)
}

func (c RelayClient) FetchInboxDelivery(ctx context.Context, inboxID, deliveryID, token string) ([]byte, error) {
	resp, err := c.do(ctx, "fetching", http.MethodGet, "/inboxes/"+inboxID+"/deliveries/"+deliveryID, token, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

func (c RelayClient) AcknowledgeInboxDelivery(ctx context.Context, inboxID, deliveryID, token string) error {
	return discard(c.do(ctx, "acknowledging", http.MethodPost, "/inboxes/"+inboxID+"/deliveries/"+deliveryID+"/ack", token, nil, http.StatusNoContent))
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
