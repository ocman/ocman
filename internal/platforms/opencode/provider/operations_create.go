package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/apiv1"
	"github.com/ocman/ocman/internal/srvtiming"
)

// CreateSession creates a new OpenCode session bound to the given directory.
func (a *Adapter) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	// A caller-supplied managed port skips discovery, otherwise refresh on miss.
	port := req.Port
	if port == "" {
		portPhase := srvtiming.Begin(ctx, "runtime_ports_fresh")
		port = a.rt.discoverOpenCodePort(req.Directory)
		if port == "" {
			port = a.rt.discoverOpenCodePortFresh(req.Directory)
		}
		portPhase.EndWithDesc("port discovery (cached, fresh on miss)")
	}
	if port == "" {
		log.WithFields(log.Fields{
			"requested": req.Directory, "normalized": normalizePortDirectory(req.Directory),
			"discovered_dirs": a.rt.discoveredOpenCodeDirs(),
		}).Warn("no running OpenCode instance for directory")
		return nil, fmt.Errorf("no running OpenCode instance for directory %s: %w", req.Directory, platforms.ErrPlatformUnreachable)
	}
	createPhase := srvtiming.Begin(ctx, "http_create")
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/session", port)
	create := map[string]any{}
	if req.NativeID != "" {
		create["id"] = req.NativeID
	}
	if req.Title != "" {
		create["title"] = req.Title
	}
	if err := configureCreation(create, req.Creation); err != nil {
		return nil, err
	}
	createBody, err := marshalRequest(create)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(createBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// One server serves all projects. The native header is URL-encoded so
	// directories with spaces and non-ASCII bytes survive transport.
	if req.Directory != "" {
		httpReq.Header.Set("x-opencode-directory", url.PathEscape(req.Directory))
	}
	resp, err := a.llmClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("opencode create-session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("opencode create-session: upstream HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		ID                   string `json:"id"`
		Title                string `json:"title"`
		ConfigurationApplied bool   `json:"configurationApplied"`
	}
	body, readErr := apiv1.ReadLimited(resp.Body, maxUpstreamErrorBytes)
	if readErr != nil {
		return nil, fmt.Errorf("opencode create-session: %w", readErr)
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		if len(body) == 0 {
			return nil, errors.New("opencode create-session: empty response")
		}
		return nil, fmt.Errorf("opencode create-session: unparseable response: %s", strings.TrimSpace(string(body)))
	}
	if parsed.ID == "" {
		return nil, fmt.Errorf("opencode create-session: response carried no session id: %s", strings.TrimSpace(string(body)))
	}
	createPhase.EndWithDesc("opencode POST /session")
	// Older builds ignored create-time titles, so preserve the PATCH fallback.
	if req.Title != "" && parsed.Title != req.Title {
		if err := a.setSessionTitle(ctx, port, parsed.ID, req.Title); err != nil {
			log.WithError(err).Warn("failed to set custom title on new session")
		}
	}
	a.rt.rememberSessionPort(parsed.ID, port)
	return &platforms.CreateSessionResponse{ID: parsed.ID, ConfigurationApplied: parsed.ConfigurationApplied}, nil
}

func (h *httpState) setSessionTitle(ctx context.Context, port, sessionID, title string) error {
	payload, err := marshalRequest(map[string]string{"title": title})
	if err != nil {
		return err
	}
	if err := h.patchJSON(ctx, port, "/session/"+url.PathEscape(sessionID), payload); err != nil {
		return err
	}
	return nil
}
