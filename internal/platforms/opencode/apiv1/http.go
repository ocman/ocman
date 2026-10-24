// Package apiv1 implements the v1-shaped HTTP operations used by OpenCode callers.
// Callers supply an authenticated client, which may translate requests for v2.
package apiv1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Upstream response limits bound buffering even for a faulty local server.
const (
	MaxErrorBytes        int64 = 64 << 10
	MaxConfigBytes       int64 = 8 << 20
	MaxConversationBytes int64 = 64 << 20
)

// ReadLimited refuses oversized bodies rather than returning truncated JSON.
func ReadLimited(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("upstream response too large (over %d bytes)", limit)
	}
	return body, nil
}

// GetJSON reads a bounded config/catalog response with a JSON content type.
func GetJSON(ctx context.Context, client *http.Client, port, path string) ([]byte, error) {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return nil, fmt.Errorf("unexpected content-type %q", ct)
	}
	return ReadLimited(resp.Body, MaxConfigBytes)
}

// PostJSONReturning posts JSON and reads a bounded response. Rejections preserve
// their upstream status and human-readable message as platforms.UpstreamError.
func PostJSONReturning(ctx context.Context, client *http.Client, port, path string, payload []byte) ([]byte, error) {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opencode %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, readErr := ReadLimited(resp.Body, MaxConfigBytes)
	if readErr != nil {
		return nil, fmt.Errorf("opencode %s: %w", path, readErr)
	}
	if resp.StatusCode >= 400 {
		if resp.StatusCode < 500 {
			ue := &platforms.UpstreamError{
				Status:  resp.StatusCode,
				Message: ExtractErrorMessage(body),
			}
			return nil, fmt.Errorf("opencode %s: %w", path, ue)
		}
		if len(body) == 0 {
			return nil, fmt.Errorf("opencode %s: upstream HTTP %d", path, resp.StatusCode)
		}
		return nil, fmt.Errorf("opencode %s: upstream HTTP %d: %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}

// SendJSON sends a JSON mutation without buffering successful response bodies.
// Upstream 4xx responses wrap platforms.UpstreamError; other failures stay errors.
func SendJSON(ctx context.Context, client *http.Client, method, port, path string, payload []byte) error {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	req, err := http.NewRequestWithContext(ctx, method, apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("opencode %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, readErr := ReadLimited(resp.Body, MaxErrorBytes)
		if readErr != nil {
			return fmt.Errorf("opencode %s: upstream HTTP %d: %w", path, resp.StatusCode, readErr)
		}
		if resp.StatusCode < 500 {
			ue := &platforms.UpstreamError{
				Status:  resp.StatusCode,
				Message: ExtractErrorMessage(body),
			}
			return fmt.Errorf("opencode %s: %w", path, ue)
		}
		if len(body) == 0 {
			return fmt.Errorf("opencode %s: upstream HTTP %d", path, resp.StatusCode)
		}
		return fmt.Errorf("opencode %s: upstream HTTP %d: %s", path, resp.StatusCode, string(body))
	}
	return nil
}

// ExtractErrorMessage decodes native NamedError and effect-style error responses.
// Unknown shapes and non-JSON bodies pass through as trimmed text.
func ExtractErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var parsed struct {
		Name    string                 `json:"name"`
		Tag     string                 `json:"_tag"`
		Message string                 `json:"message"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return trimmed
	}
	if parsed.Name == "" {
		parsed.Name = parsed.Tag
	}
	if parsed.Name == "" {
		return trimmed
	}
	if msg, ok := parsed.Data["message"].(string); ok && msg != "" {
		return msg
	}
	if parsed.Message != "" {
		return parsed.Message
	}
	if len(parsed.Data) == 0 {
		return parsed.Name
	}
	keys := make([]string, 0, len(parsed.Data))
	for k := range parsed.Data {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, parsed.Data[k]))
	}
	return fmt.Sprintf("%s: %s", parsed.Name, strings.Join(parts, ", "))
}
