package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HelperClient owns the native transport for temporary integration sessions.
// Approval policy and prompts remain with the caller; native IDs never leave
// the authenticated helper lifecycle except through canonical projection.
type HelperClient struct{ HTTPClient *http.Client }

func (j HelperClient) CreateSession(ctx context.Context, port, directory, title string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"directory": directory, "title": title})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%s/session", port), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := j.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("create session: upstream HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		ID string `json:"id"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.ID == "" {
		return "", fmt.Errorf("create session: could not parse session ID from response")
	}
	return parsed.ID, nil
}

func (j HelperClient) DeleteSession(ctx context.Context, port, sessionID string) error {
	if port == "" || sessionID == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("http://127.0.0.1:%s/session/%s", port, sessionID), nil)
	if err != nil {
		return err
	}
	resp, err := j.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete session: upstream HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (j HelperClient) SendPrompt(ctx context.Context, port, sessionID, prompt, provider, model, agent string) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"parts": []map[string]string{{"type": "text", "text": prompt}},
		"model": map[string]string{"providerID": provider, "modelID": model},
		"agent": agent,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%s/session/%s/prompt_async", port, sessionID), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := j.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("send prompt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("send prompt: upstream HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

const pollInterval = 500 * time.Millisecond

func (j HelperClient) CollectResponse(ctx context.Context, port, sessionID string) (string, error) {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/message", port, sessionID)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			return "", err
		}
		resp, err := j.HTTPClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("fetching messages: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("reading messages: %w", err)
		}
		if resp.StatusCode >= 400 {
			return "", fmt.Errorf("messages endpoint HTTP %d", resp.StatusCode)
		}
		var messages []map[string]interface{}
		if err := json.Unmarshal(body, &messages); err != nil {
			return "", fmt.Errorf("decoding messages: %w", err)
		}
		for i := len(messages) - 1; i >= 0; i-- {
			msg := messages[i]
			info, _ := msg["info"].(map[string]interface{})
			if info == nil {
				continue
			}
			if role, _ := info["role"].(string); role != "assistant" {
				continue
			}
			if finish, _ := info["finish"].(string); finish == "" {
				break
			}
			return helperText(msg), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func helperText(msg map[string]any) string {
	parts, _ := msg["parts"].([]any)
	var text strings.Builder
	for _, raw := range parts {
		part, _ := raw.(map[string]any)
		if part["type"] == "text" {
			value, _ := part["text"].(string)
			text.WriteString(value)
		}
	}
	return text.String()
}
