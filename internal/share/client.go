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
type RelayAllocation struct {
	ID          string `json:"id"`
	DeleteToken string `json:"deleteToken"`
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
		return RelayAllocation{}, fmt.Errorf("creating relay share: %w", err)
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
		return fmt.Errorf("uploading relay chunk: %w", err)
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
		return fmt.Errorf("deleting relay share: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return relayStatusError("deleting", resp)
	}
	return nil
}

func relayStatusError(action string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return fmt.Errorf("%s relay share: status %d: %s", action, resp.StatusCode, strings.TrimSpace(string(body)))
}
