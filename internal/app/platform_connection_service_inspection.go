package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	ocv2 "github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

// Inspection of an uncomposed migrated account must never create another DB
// writer. Only a registered service for the same physical DB may be probed.
// A disabled account with no matching service remains manageable but unready.
func (f connectionFactory) inspectMigratedOpenCode(ctx context.Context, c platformconnections.Connection, result hostsvc.ConnectionCheck) (hostsvc.ConnectionCheck, error) {
	result.Status = "dependencies-missing"
	result.Instructions = "The existing OpenCode runtime is unavailable. Start its native service or enable the account on the owning machine, then check again."
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !sharesServiceDB(f.openCodeConfig(c).DBPath) {
		return result, nil
	}
	service, err := ocv2.RunningService()
	if errors.Is(err, ocv2.ErrNoService) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	auth, err := f.connectionAuth(ctx, c)
	if err != nil {
		return result, err
	}
	if !auth.Enabled() {
		result.Instructions = "Configure authentication for the existing OpenCode service before checking this account."
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, service.URL+"/api/info", nil)
	if err != nil {
		return result, err
	}
	client := &http.Client{Transport: auth.IsolatedTransport(nil), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("existing OpenCode service inspection returned HTTP %d", response.StatusCode)
	}
	var info struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&info); err != nil {
		return result, err
	}
	if ocv2.Major(info.Version) < 2 {
		return result, errors.New("existing OpenCode service returned an unsupported version")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Status = "ready"
	result.Instructions = "Existing OpenCode service is available and authenticated. Native account settings are unchanged."
	result.Checks = append(result.Checks, hostsvc.ConnectionCheckItem{Label: "Existing native service", OK: true, Detail: result.Instructions})
	return result, nil
}
