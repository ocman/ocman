package opencode

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// OpenGlobalEvents connects the owner-local native event transport. Callers own
// cancellation and stream silence detection, including the wait for headers.
// Canonical adapters project these frames before application callbacks run.
func OpenGlobalEvents(ctx context.Context, client *http.Client, port string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/global/event", nil)
	if err != nil {
		return nil, fmt.Errorf("build /global/event request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect /global/event: %w", err)
	}
	if resp.StatusCode >= 400 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("/global/event upstream HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}
