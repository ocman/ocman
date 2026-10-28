package opencode

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

func (a *Adapter) stopBackgroundTask(ctx context.Context, req platforms.AbortRequest) error {
	if !ocv2.InstalledV2() {
		return platforms.ErrUnsupported
	}
	port, session, err := a.resolvePortCtx(ctx, req.SessionID)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/session/%s/background/%s/stop?directory=%s", url.PathEscape(req.SessionID), url.PathEscape(req.TaskID), url.QueryEscape(session.Directory))
	return sendJSON(ctx, http.MethodPost, port, path, nil)
}
