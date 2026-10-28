package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

func waitSessionIdentity(t *testing.T, base, platform, nativeID string) platforms.SessionIdentity {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	query := url.Values{"platform": {platform}, "nativeId": {nativeID}}
	var lastErr error
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		resp, err := client.Get(base + "/api/session-identity?" + query.Encode())
		if err != nil {
			lastErr = err
			continue
		}
		var identity platforms.SessionIdentity
		err = json.NewDecoder(resp.Body).Decode(&identity)
		_ = resp.Body.Close()
		if err == nil && resp.StatusCode == http.StatusOK && strings.HasPrefix(identity.ID, "ocm_") && identity.NativeID == "" && identity.SourceKey == "" && identity.Platform == platform {
			return identity
		}
		lastErr = fmt.Errorf("identity response: status %d, identity %+v", resp.StatusCode, identity)
		if err != nil {
			lastErr = fmt.Errorf("%w: %w", lastErr, err)
		}
	}
	t.Fatalf("session identity never ready: %v", lastErr)
	return platforms.SessionIdentity{}
}
