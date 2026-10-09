package autoapprove

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func (j *PermissionJudge) createSession(ctx context.Context, port, directory, title string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"directory": directory, "title": title})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%s/session", port), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := j.httpClient.Do(req)
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

func (j *PermissionJudge) deleteSession(ctx context.Context, port, sessionID string) error {
	if port == "" || sessionID == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("http://127.0.0.1:%s/session/%s", port, sessionID), nil)
	if err != nil {
		return err
	}
	resp, err := j.httpClient.Do(req)
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

func (j *PermissionJudge) sendPrompt(ctx context.Context, port, sessionID, permission string, patterns []string, metadata map[string]any, customSections []PromptSection) error {
	provider, model := j.model()
	payload, _ := json.Marshal(map[string]interface{}{
		"parts": []map[string]string{{"type": "text", "text": judgePrompt(permission, patterns, metadata, customSections)}},
		"model": map[string]string{"providerID": provider, "modelID": model},
		"agent": judgeAgent,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%s/session/%s/prompt_async", port, sessionID), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := j.httpClient.Do(req)
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

const recentUserMessagesLimit = 6

func (j *PermissionJudge) recentUserMessages(ctx context.Context, port, sessionID string) []string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%s/session/%s/message", port, sessionID), nil)
	if err != nil {
		return nil
	}
	resp, err := j.httpClient.Do(req)
	if err != nil {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode >= 400 {
		return nil
	}
	var messages []map[string]interface{}
	if err := json.Unmarshal(body, &messages); err != nil {
		return nil
	}
	var out []string
	for _, m := range messages {
		info, _ := m["info"].(map[string]interface{})
		if info == nil {
			continue
		}
		if role, _ := info["role"].(string); role != "user" {
			continue
		}
		if txt := extractTextFromParts(m); txt != "" {
			out = append(out, txt)
		}
	}
	if len(out) > recentUserMessagesLimit {
		out = out[len(out)-recentUserMessagesLimit:]
	}
	return out
}

const pollInterval = 500 * time.Millisecond

func (j *PermissionJudge) collectResponse(ctx context.Context, port, sessionID string) (string, error) {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session/%s/message", port, sessionID)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			return "", err
		}
		resp, err := j.httpClient.Do(req)
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
			return extractTextFromParts(msg), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
