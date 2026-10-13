package projectforge

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/forgehttp"
	"github.com/NoUseFreak/ocman/internal/httpx"
)

// writeRateLimit answers 429 with Retry-After when the forge reports its
// quota exhausted; the sidebar's CI polling backs off on it.
func writeRateLimit(w http.ResponseWriter, rl forge.RateLimit) bool {
	if !rl.Limited {
		return false
	}
	if !rl.ResetAt.IsZero() && w.Header().Get("Retry-After") == "" {
		seconds := math.Max(1, math.Ceil(time.Until(rl.ResetAt).Seconds()))
		w.Header().Set("Retry-After", strconv.FormatInt(int64(seconds), 10))
	}
	httpx.WriteJSONStatus(w, http.StatusTooManyRequests, map[string]any{
		"error": map[string]any{"code": "rate_limited", "message": "forge rate limit exceeded", "status": http.StatusTooManyRequests, "retryAfter": rl.ResetAt},
	})
	return true
}

func writeForgeError(w http.ResponseWriter, r *http.Request, rem forge.Remote, err error) {
	if httpx.WriteCancellation(w, "forge request canceled", err) {
		return
	}
	upstreamStatus := 0
	var response *forgehttp.ResponseError
	if errors.As(err, &response) {
		upstreamStatus = response.Status
		if response.RateLimit.Limited {
			w.Header().Set("Retry-After", response.RetryAfter)
			writeRateLimit(w, response.RateLimit)
			return
		}
	}
	if errors.Is(err, forge.ErrRateLimited) {
		writeRateLimit(w, forge.RateLimit{Limited: true})
		return
	}
	// Do not log the error/body: forge responses may contain credentials.
	log.WithFields(log.Fields{"forge_host": rem.Host, "repo": rem.Repo, "route": r.URL.Path, "upstream_status": upstreamStatus}).Warn("forge request failed")
	httpx.WriteJSONStatus(w, http.StatusBadGateway, map[string]any{
		"error": map[string]any{"code": "upstream_status", "message": "forge request failed", "status": http.StatusBadGateway, "upstreamStatus": upstreamStatus},
	})
}

// writeListError emits the structured error envelope documented in
// spec/pr-issue-sidebar/architecture.md's "API design" section.
func writeListError(w http.ResponseWriter, status int, code, message string) {
	httpx.WriteJSONStatus(w, status, map[string]interface{}{
		"error": map[string]interface{}{"code": code, "message": message, "status": status},
	})
}
