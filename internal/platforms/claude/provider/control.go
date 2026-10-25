package claudeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const controlTimeout = 60 * time.Second

type controlResult struct {
	data json.RawMessage
	err  error
}

func sdkInitializeRequest() map[string]any {
	return map[string]any{"subtype": "initialize", "hooks": nil, "sdkMcpServers": []string{},
		"sdkCapabilities": map[string]any{}, "forwardSubagentText": true}
}

// control waits for the CLI's acknowledgment, exit or deadline, never holding l.mu.
func (l *live) control(ctx context.Context, p *proc, id string, request map[string]any) (json.RawMessage, error) {
	return l.controlAt(ctx, p, id, request, nil)
}

// controlAt runs boundary while holding the stdin write lock, just before the
// request is written: it observes exactly the inputs written ahead of it.
func (l *live) controlAt(ctx context.Context, p *proc, id string, request map[string]any, boundary func()) (json.RawMessage, error) {
	if id == "" {
		id = "req_" + uuid.NewString()
	}
	response := make(chan controlResult, 1)
	p.controlMu.Lock()
	p.controls[id] = response
	p.controlMu.Unlock()
	defer func() {
		p.controlMu.Lock()
		delete(p.controls, id)
		p.controlMu.Unlock()
	}()
	p.writeMu.Lock()
	if boundary != nil {
		boundary()
	}
	err := l.writeLocked(p, map[string]any{"type": "control_request", "request_id": id, "request": request})
	p.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	select {
	case result := <-response:
		return result.data, result.err
	case <-p.done:
		// A response and EOF can arrive together. Prefer the explicit CLI error.
		select {
		case result := <-response:
			return result.data, result.err
		default:
			return nil, fmt.Errorf("claude exited awaiting %s: %s", request["subtype"], p.stderr.String())
		}
	case <-ctx.Done():
		return nil, fmt.Errorf("claude %s: %w", request["subtype"], ctx.Err())
	}
}

func (l *live) receiveControl(p *proc, raw []byte) {
	var message struct {
		Response struct {
			RequestID string          `json:"request_id"`
			Subtype   string          `json:"subtype"`
			Error     string          `json:"error"`
			Response  json.RawMessage `json:"response"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return
	}
	r := message.Response
	result := controlResult{data: r.Response}
	if r.Subtype != "success" {
		result.err = errors.New(or(r.Error, "invalid Claude control response"))
	}
	p.controlMu.Lock()
	defer p.controlMu.Unlock()
	if r.RequestID == initRequestID && result.err == nil {
		p.initialized = true
	}
	if pending := p.controls[r.RequestID]; pending != nil {
		select {
		case pending <- result:
		default: // duplicate acknowledgment
		}
	}
}

// A startup failure can be a result frame rather than an initialize response.
func (l *live) initializationFailure(p *proc, raw []byte) bool {
	p.controlMu.Lock()
	defer p.controlMu.Unlock()
	if p.initialized || p.controls[initRequestID] == nil {
		return false
	}
	var result struct {
		IsError bool     `json:"is_error"`
		Subtype string   `json:"subtype"`
		Result  string   `json:"result"`
		Errors  []string `json:"errors"`
	}
	_ = json.Unmarshal(raw, &result)
	if !result.IsError && (result.Subtype == "" || result.Subtype == "success") {
		return false
	}
	err := errors.New(or(strings.Join(result.Errors, "; "), or(result.Result, result.Subtype)))
	select {
	case p.controls[initRequestID] <- controlResult{err: err}:
	default:
	}
	return true
}

func (l *live) initializeProc(p *proc) error {
	p.initializeOnce.Do(func() {
		_, p.initializeErr = l.control(context.Background(), p, initRequestID, l.initializeRequest())
	})
	return p.initializeErr
}

func (l *live) changeModel(p *proc, model string) error {
	l.nativeModeMu.Lock()
	defer l.nativeModeMu.Unlock()
	l.mu.Lock()
	changed := model != "" && model != p.model
	l.mu.Unlock()
	if !changed {
		return nil
	}
	if _, err := l.control(context.Background(), p, "", map[string]any{"subtype": "set_model", "model": model}); err != nil {
		return err
	}
	l.mu.Lock()
	p.model = model
	l.mu.Unlock()
	return nil
}
