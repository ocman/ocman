package server

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/forgehttp"
	log "github.com/sirupsen/logrus"
)

func writeProjectRateLimit(w http.ResponseWriter, rl forge.RateLimit) bool {
	if !rl.Limited {
		return false
	}
	if !rl.ResetAt.IsZero() && w.Header().Get("Retry-After") == "" {
		seconds := math.Max(1, math.Ceil(time.Until(rl.ResetAt).Seconds()))
		w.Header().Set("Retry-After", strconv.FormatInt(int64(seconds), 10))
	}
	writeJSONStatus(w, http.StatusTooManyRequests, map[string]any{
		"error": map[string]any{"code": "rate_limited", "message": "forge rate limit exceeded", "status": http.StatusTooManyRequests, "retryAfter": rl.ResetAt},
	})
	return true
}

func writeProjectForgeError(w http.ResponseWriter, r *http.Request, rem forge.Remote, err error) {
	if writeCancellation(w, "forge request canceled", err) {
		return
	}
	upstreamStatus := 0
	var response *forgehttp.ResponseError
	if errors.As(err, &response) {
		upstreamStatus = response.Status
		if response.RateLimit.Limited {
			w.Header().Set("Retry-After", response.RetryAfter)
			writeProjectRateLimit(w, response.RateLimit)
			return
		}
	}
	if errors.Is(err, forge.ErrRateLimited) {
		writeProjectRateLimit(w, forge.RateLimit{Limited: true})
		return
	}
	// Do not log the error/body: forge responses may contain credentials.
	log.WithFields(log.Fields{"forge_host": rem.Host, "repo": rem.Repo, "route": r.URL.Path, "upstream_status": upstreamStatus}).Warn("forge request failed")
	writeProjectListError(w, http.StatusBadGateway, "upstream_status", "forge request failed")
}
