package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	openAIUsageURL    = "https://chatgpt.com/backend-api/wham/usage"
	anthropicUsageURL = "https://api.anthropic.com/api/oauth/usage"
)

type subscriptionUsageClient struct {
	http         *http.Client
	authPath     string
	openAIURL    string
	anthropicURL string
}

type subscriptionUsageResponse struct {
	Providers []subscriptionProviderUsage `json:"providers"`
}

type subscriptionProviderUsage struct {
	ID      string                    `json:"id"`
	Name    string                    `json:"name"`
	Plan    string                    `json:"plan,omitempty"`
	Status  string                    `json:"status"`
	Windows []subscriptionUsageWindow `json:"windows"`
}

type subscriptionUsageWindow struct {
	Name        string  `json:"name"`
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    string  `json:"resetsAt,omitempty"`
}

type subscriptionOAuth struct {
	Type      string `json:"type"`
	Access    string `json:"access"`
	Expires   int64  `json:"expires"`
	AccountID string `json:"accountId"`
}

func newSubscriptionUsageClient() subscriptionUsageClient {
	return subscriptionUsageClient{
		http: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		authPath:     openCodeAuthPath(),
		openAIURL:    openAIUsageURL,
		anthropicURL: anthropicUsageURL,
	}
}

func openCodeAuthPath() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "opencode", "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode", "auth.json")
}

func (s *Server) handleSubscriptionUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := fetchSubscriptionUsage(r.Context(), s.subscriptionUsage)
	if err != nil {
		serverError(w, "failed to read subscription usage", err)
		return
	}
	writeJSON(w, usage)
}

func fetchSubscriptionUsage(ctx context.Context, client subscriptionUsageClient) (subscriptionUsageResponse, error) {
	var auth struct {
		OpenAI    subscriptionOAuth `json:"openai"`
		Anthropic subscriptionOAuth `json:"anthropic"`
	}
	file, err := os.Open(client.authPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return subscriptionUsageResponse{Providers: []subscriptionProviderUsage{}}, nil
		}
		return subscriptionUsageResponse{}, errors.New("cannot open OpenCode auth file")
	}
	defer file.Close()
	if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&auth); err != nil {
		return subscriptionUsageResponse{}, errors.New("cannot decode OpenCode auth file")
	}

	providers := make([]subscriptionProviderUsage, 0, 2)
	if usableOAuth(auth.OpenAI) {
		providers = append(providers, fetchOpenAIUsage(ctx, client, auth.OpenAI))
	} else if configuredOAuth(auth.OpenAI) {
		providers = append(providers, subscriptionProviderUsage{ID: "openai", Name: "OpenAI", Status: "expired", Windows: []subscriptionUsageWindow{}})
	}
	if usableOAuth(auth.Anthropic) {
		providers = append(providers, fetchAnthropicUsage(ctx, client, auth.Anthropic))
	} else if configuredOAuth(auth.Anthropic) {
		providers = append(providers, subscriptionProviderUsage{ID: "anthropic", Name: "Anthropic", Status: "expired", Windows: []subscriptionUsageWindow{}})
	}
	return subscriptionUsageResponse{Providers: providers}, nil
}

func configuredOAuth(auth subscriptionOAuth) bool {
	return auth.Type == "oauth" && strings.TrimSpace(auth.Access) != ""
}

func usableOAuth(auth subscriptionOAuth) bool {
	return configuredOAuth(auth) && (auth.Expires <= 0 || auth.Expires > time.Now().UnixMilli())
}

type openAIWindow struct {
	UsedPercent       float64 `json:"used_percent"`
	LimitWindowSecond int64   `json:"limit_window_seconds"`
	ResetAt           int64   `json:"reset_at"`
}

type openAIRateLimit struct {
	Primary   *openAIWindow `json:"primary_window"`
	Secondary *openAIWindow `json:"secondary_window"`
}

func fetchOpenAIUsage(ctx context.Context, client subscriptionUsageClient, auth subscriptionOAuth) subscriptionProviderUsage {
	result := subscriptionProviderUsage{ID: "openai", Name: "OpenAI", Status: "upstream_error", Windows: []subscriptionUsageWindow{}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.openAIURL, nil)
	if err != nil {
		return result
	}
	req.Header.Set("Authorization", "Bearer "+auth.Access)
	req.Header.Set("Accept", "application/json")
	if auth.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", auth.AccountID)
	}

	resp, err := client.http.Do(req)
	if err != nil {
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		result.Status = subscriptionHTTPStatus(resp.StatusCode)
		return result
	}
	var body struct {
		PlanType             string          `json:"plan_type"`
		RateLimit            openAIRateLimit `json:"rate_limit"`
		AdditionalRateLimits []struct {
			Name      string          `json:"limit_name"`
			RateLimit openAIRateLimit `json:"rate_limit"`
		} `json:"additional_rate_limits"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil {
		return result
	}
	result.Status = "ok"
	result.Plan = body.PlanType
	result.Windows = appendOpenAIWindows(result.Windows, "", body.RateLimit)
	for _, additional := range body.AdditionalRateLimits {
		result.Windows = appendOpenAIWindows(result.Windows, additional.Name, additional.RateLimit)
	}
	return result
}

func appendOpenAIWindows(windows []subscriptionUsageWindow, prefix string, limits openAIRateLimit) []subscriptionUsageWindow {
	for _, window := range []*openAIWindow{limits.Primary, limits.Secondary} {
		if window == nil {
			continue
		}
		name := usageWindowName(window.LimitWindowSecond)
		if prefix != "" {
			name = prefix + " · " + name
		}
		windows = append(windows, subscriptionUsageWindow{Name: name, UsedPercent: clampPercent(window.UsedPercent), ResetsAt: unixTime(window.ResetAt)})
	}
	return windows
}

func usageWindowName(seconds int64) string {
	switch seconds {
	case 5 * 60 * 60:
		return "5 hours"
	case 7 * 24 * 60 * 60:
		return "7 days"
	case 2628000:
		return "Monthly"
	default:
		return "Usage window"
	}
}

func fetchAnthropicUsage(ctx context.Context, client subscriptionUsageClient, auth subscriptionOAuth) subscriptionProviderUsage {
	result := subscriptionProviderUsage{ID: "anthropic", Name: "Anthropic", Status: "upstream_error", Windows: []subscriptionUsageWindow{}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.anthropicURL, nil)
	if err != nil {
		return result
	}
	req.Header.Set("Authorization", "Bearer "+auth.Access)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := client.http.Do(req)
	if err != nil {
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		result.Status = subscriptionHTTPStatus(resp.StatusCode)
		return result
	}
	type window struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	}
	var body struct {
		FiveHour          *window `json:"five_hour"`
		SevenDay          *window `json:"seven_day"`
		SevenDayOAuthApps *window `json:"seven_day_oauth_apps"`
		SevenDayOpus      *window `json:"seven_day_opus"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil {
		return result
	}
	result.Status = "ok"
	for _, item := range []struct {
		name   string
		window *window
	}{
		{"5 hours", body.FiveHour},
		{"7 days", body.SevenDay},
		{"7 days · OAuth apps", body.SevenDayOAuthApps},
		{"7 days · Opus", body.SevenDayOpus},
	} {
		if item.window != nil {
			result.Windows = append(result.Windows, subscriptionUsageWindow{Name: item.name, UsedPercent: clampPercent(item.window.Utilization), ResetsAt: item.window.ResetsAt})
		}
	}
	return result
}

func subscriptionHTTPStatus(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "unauthorized"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		return "upstream_error"
	}
}

func clampPercent(value float64) float64 {
	return max(0, min(100, value))
}

func unixTime(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}
