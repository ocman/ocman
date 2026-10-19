package autoapprove

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// HostedTypeSafe excludes unkeyed and custom compatible servers from paid usage.
func (c JudgeEndpoint) HostedTypeSafe() bool {
	u, err := url.Parse(c.Endpoint)
	return err == nil && c.Format == "typesafe" && strings.TrimSpace(c.APIKey) != "" &&
		u.Scheme == "https" && u.Host == "api.typesafe.ai" && u.Path == "/v1/systemone" && u.User == nil
}

type typeSafeUsage struct {
	Model string `json:"model"`
	Usage struct {
		Input  *int64 `json:"input_tokens"`
		Output *int64 `json:"output_tokens"`
	} `json:"usage"`
}

func readTypeSafeUsage(body []byte) *typeSafeUsage {
	var usage typeSafeUsage
	if json.Unmarshal(body, &usage) != nil {
		return &typeSafeUsage{} // A successful response without usable accounting is unpriced.
	}
	return &usage
}

func (j *PermissionJudge) recordTypeSafeUsage(ctx context.Context, config JudgeEndpoint, usage *typeSafeUsage) {
	if j.recordUsage == nil || usage == nil || !config.HostedTypeSafe() {
		return
	}
	// The upstream call may have consumed the deadline after incurring a charge.
	// Keep the write synchronous and bounded so shutdown still joins it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := j.recordUsage(ctx, usage.Model, usage.Usage.Input, usage.Usage.Output); err != nil {
		log.WithError(err).Warn("failed to record TypeSafe usage")
	}
}
