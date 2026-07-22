package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// openCodeClient is an HTTP client with a reasonable timeout for API
// calls to local OpenCode instances.
var openCodeClient = &http.Client{
	Timeout:   10 * time.Second,
	Transport: otelhttp.NewTransport(ocapi.New("").Transport(http.DefaultTransport)),
}

func configureHTTPAuth(auth ocapi.Auth) {
	// Keep auth inside instrumentation so even opt-in HTTP header capture
	// observes the original request without Authorization.
	openCodeClient.Transport = otelhttp.NewTransport(auth.Transport(http.DefaultTransport))
}

// --- Fetching session data from the OpenCode HTTP API ---

// rawGet performs a plain GET against the OpenCode instance and
// returns the response body if the status is 200.
func rawGet(port, path string) ([]byte, bool) {
	url := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	resp, err := openCodeClient.Get(url)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	return body, true
}

// getInto performs a plain GET against the OpenCode instance and
// unmarshals a 200 JSON response into v. Returns false on any
// transport, status, or decode failure.
func getInto(port, path string, v interface{}) bool {
	body, ok := rawGet(port, path)
	if !ok {
		return false
	}
	return json.Unmarshal(body, v) == nil
}

// fetchOpenCodeSession fetches session metadata from the OpenCode HTTP API.
func fetchOpenCodeSession(port, sessionID string) (map[string]interface{}, error) {
	path := "/session/" + sessionID
	body, ok := sessionCache.getOrFetch(port, path, func() ([]byte, bool) {
		return rawGet(port, path)
	})
	if !ok {
		return nil, fmt.Errorf("session API: upstream fetch failed")
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding session: %w", err)
	}
	return result, nil
}

// fetchOpenCodeSmallModel fetches the resolved OpenCode config and extracts small_model.
func fetchOpenCodeSmallModel(port string) (providerID, modelID string, ok bool) {
	var cfg struct {
		SmallModel string `json:"small_model"`
	}
	if !getInto(port, "/config", &cfg) {
		return "", "", false
	}
	slash := strings.IndexByte(cfg.SmallModel, '/')
	if slash <= 0 || slash == len(cfg.SmallModel)-1 {
		return "", "", false
	}
	return cfg.SmallModel[:slash], cfg.SmallModel[slash+1:], true
}

// OpenCodeProviderModel is the minimal subset of a model entry needed for the picker.
type OpenCodeProviderModel struct {
	ID       string                          `json:"id"`
	Name     string                          `json:"name,omitempty"`
	Status   string                          `json:"status,omitempty"`
	Variants map[string]OpenCodeModelVariant `json:"variants,omitempty"`
	Limit    OpenCodeModelLimit              `json:"limit,omitempty"`
}

// OpenCodeModelLimit mirrors the `limit` block on a model entry.
type OpenCodeModelLimit struct {
	Context int64 `json:"context,omitempty"`
	Output  int64 `json:"output,omitempty"`
}

// OpenCodeModelVariant is a single variant entry from OpenCode's /provider payload.
type OpenCodeModelVariant struct {
	Disabled bool `json:"disabled,omitempty"`
}

// OpenCodeProvider is a trimmed provider entry.
type OpenCodeProvider struct {
	ID     string                           `json:"id"`
	Name   string                           `json:"name,omitempty"`
	Models map[string]OpenCodeProviderModel `json:"models"`
}

// OpenCodeProvidersResponse is the shape returned by OpenCode's GET /provider.
type OpenCodeProvidersResponse struct {
	All       []OpenCodeProvider `json:"all"`
	Connected []string           `json:"connected"`
	Default   map[string]string  `json:"default"`
}

// fetchOpenCodeProviders calls GET /provider on the running OpenCode instance.
func fetchOpenCodeProviders(port string) (OpenCodeProvidersResponse, bool) {
	var empty OpenCodeProvidersResponse
	body, ok := getJSONCached(context.Background(), port, "/provider")
	if !ok {
		return empty, false
	}
	var parsed OpenCodeProvidersResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return empty, false
	}
	return parsed, true
}

// fetchOpenCodeMessages fetches messages for a session from the OpenCode HTTP API.
func fetchOpenCodeMessages(port, sessionID string) ([]map[string]interface{}, error) {
	path := "/session/" + sessionID + "/message"
	body, ok := sessionCache.getOrFetch(port, path, func() ([]byte, bool) {
		return rawGet(port, path)
	})
	if !ok {
		return nil, fmt.Errorf("messages API: upstream fetch failed")
	}
	var result []map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding messages: %w", err)
	}
	return result, nil
}

// isSynthesizedTerminal reports whether a raw OpenCode message originated
// from a non-LLM source (e.g. POST /session/{id}/shell) and has finished.
func isSynthesizedTerminal(raw map[string]interface{}) bool {
	rawParts, ok := raw["parts"].([]interface{})
	if !ok || len(rawParts) == 0 {
		return false
	}
	hasPart := false
	for _, p := range rawParts {
		part, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		hasPart = true
		if t, _ := part["type"].(string); t == "step-start" {
			return false
		}
		if state, ok := part["state"].(map[string]interface{}); ok {
			if status, _ := state["status"].(string); status == "running" {
				return false
			}
		}
	}
	return hasPart
}

// fetchSessionFromOpenCodeCtx tries to get session data from the running
// OpenCode HTTP API, with per-phase Server-Timing instrumentation.
func (a *Adapter) fetchSessionFromOpenCodeCtx(ctx context.Context, sessionID string, limit, offset int) (*platforms.SessionDetail, bool) {
	if a.db == nil {
		return nil, false
	}
	dbPhase := srvtiming.Begin(ctx, "db_get_session")
	dbSession, err := a.db.GetSession(sessionID)
	dbPhase.End()
	if err != nil {
		return nil, false
	}

	port := resolveOpenCodePortForSessionCtx(ctx, sessionID, dbSession.Directory)
	if port == "" {
		return nil, false
	}

	var ocSession map[string]interface{}
	var ocMessages []map[string]interface{}
	var sessionErr, messagesErr error
	parallelPhase := srvtiming.Begin(ctx, "http_parallel")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		p := srvtiming.Begin(ctx, "http_session")
		ocSession, sessionErr = fetchOpenCodeSession(port, sessionID)
		p.EndWithDesc("GET /session/{id}")
	}()
	go func() {
		defer wg.Done()
		p := srvtiming.Begin(ctx, "http_messages")
		ocMessages, messagesErr = fetchOpenCodeMessages(port, sessionID)
		p.EndWithDesc("GET /session/{id}/message")
	}()
	wg.Wait()
	parallelPhase.EndWithDesc("wall-clock for both fetches")

	if sessionErr != nil || messagesErr != nil || ocSession == nil {
		forgetSessionPort(sessionID, port)
		return nil, false
	}

	convPhase := srvtiming.Begin(ctx, "convert")
	untypedMessages, untypedParts := convertOpenCodeMessages(ocMessages)
	stats := computeMessageStats(untypedMessages)
	totalMessages := len(untypedMessages)
	pagedMessages, pagedMsgIDs := paginateUntyped(untypedMessages, limit, offset)
	pagedParts := filterPartsUntyped(untypedParts, pagedMsgIDs)
	convPhase.EndWithDesc("convertOpenCodeMessages + paginate")

	defaultsPhase := srvtiming.Begin(ctx, "db_session_defaults")
	defaults, err := getSessionDefaultsCached(a.db, sessionID, dbSession.Directory)
	defaultsPhase.End()
	if err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).
			Warn("opencode: fetching session defaults for live path")
	}

	sessionStatus := "done"
	lastErrorName := ""
	lastErrorMessage := ""
	lastErrorAt := int64(0)
	if n := len(untypedMessages); n > 0 {
		lastMessage := untypedMessages[n-1]
		if info, ok := lastMessage["data"].(map[string]interface{}); ok {
			role, _ := info["role"].(string)
			finish, _ := info["finish"].(string)
			lastErr := ""
			if rawError, hasError := info["error"]; hasError {
				lastErr = "true"
				if errorMap, ok := rawError.(map[string]interface{}); ok {
					lastErrorName, _ = errorMap["name"].(string)
					lastErrorMessage, _ = errorMap["message"].(string)
					if dataMap, ok := errorMap["data"].(map[string]interface{}); ok {
						if dataMessage, _ := dataMap["message"].(string); dataMessage != "" {
							lastErrorMessage = dataMessage
						}
					}
				}
			}
			switch v := lastMessage["timeCreated"].(type) {
			case float64:
				lastErrorAt = int64(v)
			case int64:
				lastErrorAt = v
			}
			synthTerminal := false
			if rawIdx := len(ocMessages) - 1; rawIdx >= 0 {
				synthTerminal = isSynthesizedTerminal(ocMessages[rawIdx])
			}
			sessionStatus = db.InferSessionStatus(role, finish, lastErr, synthTerminal)
		}
	}

	userMsgCount := 0
	for _, m := range untypedMessages {
		if info, ok := m["data"].(map[string]interface{}); ok {
			if role, _ := info["role"].(string); role == "user" {
				userMsgCount++
			}
		}
	}

	typedPhase := srvtiming.Begin(ctx, "typed")
	session := sessionFromOpenCode(ocSession, stats, userMsgCount, sessionStatus)
	session.LastErrorName = lastErrorName
	session.LastErrorMessage = lastErrorMessage
	session.LastErrorAt = lastErrorAt
	messages := typedMessagesFromUntyped(pagedMessages)
	parts := typedPartsFromUntyped(pagedParts)
	typedPhase.EndWithDesc("untyped->typed conversion")

	return &platforms.SessionDetail{
		Session:           session,
		Messages:          messages,
		Parts:             parts,
		TotalMessages:     totalMessages,
		ContextTokenCount: int64(stats.contextTokenCount),
		DefaultAgent:      defaults.Agent,
		DefaultModel:      defaults.Model,
		Warnings:          sessionWarningsForDirectory(dbSession.Directory),
	}, true
}
