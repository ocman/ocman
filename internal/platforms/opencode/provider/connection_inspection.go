package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

// The no-secret integration connection ordering was verified against the 2.1
// API contract. Unknown minor versions need verification rather than guessing
// which saved credential the native server would choose.
var inspectionVersion = regexp.MustCompile(`^2\.1\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

// InspectConfiguration checks a private, owner-bootstrapped endpoint before its
// connection reservation is saved. It requires no native DB reader or durable
// runtime record. The owner must launch through its isolated Host lifecycle and
// pass exactly that endpoint, never a discovered shared service.
func InspectConfiguration(ctx context.Context, cfg ConnectionConfig, endpoint string) (platforms.ConnectionInspection, error) {
	if err := cfg.Native.Validate(); err != nil {
		return platforms.ConnectionInspection{}, err
	}
	if !cfg.Auth.Enabled() {
		return platforms.ConnectionInspection{}, errors.New("private OpenCode server authentication is required")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Hostname() != "127.0.0.1" {
		return platforms.ConnectionInspection{}, errors.New("private loopback OpenCode endpoint is required")
	}
	http := newHTTPState(cfg.Auth)
	discovery := NewDiscovery()
	discovery.httpState = http
	discovery.SetMachineServer(u.Port())
	a := &Adapter{httpState: http, auth: cfg.Auth, connection: &connectionOwner{}, rt: discovery}
	return a.InspectConnection(ctx)
}

// InspectConnection reads the selected private server's native account metadata.
// Neither endpoint starts login, refreshes credentials or requests a model.
// /api/credential is intentionally not used: it returns credential secrets.
func (a *Adapter) InspectConnection(ctx context.Context) (platforms.ConnectionInspection, error) {
	result := platforms.ConnectionInspection{Message: "OpenCode account inspection requires a named isolated connection."}
	if a.connection == nil {
		return result, nil
	}
	port := a.rt.MachineServerPort()
	if port == "" {
		result.Message = "The private OpenCode server is not running."
		return result, nil
	}
	info, err := a.getJSON(ctx, port, "/api/info")
	if err != nil {
		return result, inspectionError(ctx, err)
	}
	var server struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(info, &server) != nil || !inspectionVersion.MatchString(server.Version) {
		result.Message = "This OpenCode version does not support verified isolated account inspection."
		return result, nil
	}
	result.NativeVersion = server.Version
	result.IsolationSupported = true
	body, err := a.getJSON(ctx, port, "/api/integration")
	if err != nil {
		return result, inspectionError(ctx, err)
	}
	var response struct {
		Data *[]struct {
			Connections []struct {
				Type   string          `json:"type"`
				ID     string          `json:"id"`
				Label  string          `json:"label"`
				Method string          `json:"method"`
				Status json.RawMessage `json:"status"`
			} `json:"connections"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || response.Data == nil {
		result.Message = "OpenCode returned an unsupported account inspection response."
		return result, nil
	}
	result.Message = "Connect a saved API-key or OAuth account to this private OpenCode server."
	for _, integration := range *response.Data {
		if len(integration.Connections) == 0 {
			continue
		}
		// Native Integration.connection.active selects only the first entry.
		active := integration.Connections[0]
		if active.Type != "credential" || active.ID == "" || (active.Method != "key" && active.Method != "oauth") {
			result.IsolationSupported = false
			result.Message = "The active OpenCode authentication mode is not supported for isolated accounts."
			return result, nil
		}
		if len(active.Status) != 0 && string(active.Status) != "null" {
			result.Message = "The selected OpenCode account needs authentication."
			return result, nil
		}
		label := inspectionLabel(active.Label)
		if label == "" {
			result.Message = "OpenCode did not provide a safe account label."
			return result, nil
		}
		if result.AccountLabel == "" {
			result.AccountLabel = label
			result.AuthMode = active.Method
		}
	}
	if result.AccountLabel != "" {
		result.Ready = true
		result.Message = "Saved OpenCode account is configured in private storage."
	}
	return result, nil
}

func inspectionError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ocapi.ErrAuthentication) {
		return ocapi.ErrAuthentication
	}
	return errors.New("OpenCode account inspection request failed")
}

func inspectionLabel(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > 80 || strings.Contains(value, "sk-") || strings.Contains(value, "Bearer ") {
		return ""
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ""
		}
	}
	return value
}

var _ platforms.ConnectionInspector = (*Adapter)(nil)
