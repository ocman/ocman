package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// Account asks the running OpenCode service for its own Console usage only.
// It never reads credentials or resolves another provider's subscription.
func (a *Adapter) Account(ctx context.Context) (*providerprotocol.Account, error) {
	port := a.rt.MachineServerPort()
	if port == "" {
		return nil, platforms.ErrPlatformUnreachable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/api/experimental/usage/limits", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, Transport: a.transport(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNotImplemented {
		return nil, platforms.ErrUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenCode usage: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data *consoleUsage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, err
	}
	if body.Data == nil {
		return nil, platforms.ErrNotFound
	}
	return body.Data.account(), nil
}

// These fields mirror the experimental UsageLimits.Info API. Money is USD
// and reset timestamps are milliseconds, not the Console API's microcents.
type consoleUsage struct {
	Go *struct {
		Rolling consoleMeter `json:"rolling"`
		Weekly  consoleMeter `json:"weekly"`
		Monthly consoleMeter `json:"monthly"`
	} `json:"go"`
	Budgets []consoleBudget `json:"budgets"`
}

type consoleBudget struct {
	ID                string   `json:"id"`
	Limit             float64  `json:"limit"`
	Spent             float64  `json:"spent"`
	ResetsAt          int64    `json:"resetsAt"`
	Scope             string   `json:"scope"`
	Per               string   `json:"per"`
	Coverage          string   `json:"coverage"`
	Shared            bool     `json:"shared"`
	Vendors           []string `json:"vendors"`
	InputPriceAtLeast *float64 `json:"inputPriceAtLeast"`
	Models            []struct {
		Name string `json:"name"`
	} `json:"models"`
}

func (b consoleBudget) label() (string, string) {
	name := "Personal monthly budget"
	if b.Scope == "combined" {
		name = "Group monthly budget"
	}
	if b.Shared {
		name = "Workspace monthly budget"
	}
	coverage := "All usage"
	if b.Coverage == "models" {
		coverage = strings.Join(b.Vendors, ", ")
		if coverage == "" {
			names := []string{}
			for _, model := range b.Models {
				names = append(names, model.Name)
			}
			coverage = strings.Join(names, ", ")
		}
		if coverage == "" {
			coverage = "Selected models"
		}
	}
	if b.InputPriceAtLeast != nil {
		coverage += fmt.Sprintf(" · input price at least $%g per million tokens", *b.InputPriceAtLeast)
	}
	if b.Per == "model" {
		coverage += ". Per-model limit; usage shown for the most-used model."
	}
	return name, coverage
}

type consoleMeter struct {
	Percent  float64 `json:"percent"`
	ResetsAt int64   `json:"resetsAt"`
}

func (u *consoleUsage) account() *providerprotocol.Account {
	out := &providerprotocol.Account{SubscriptionType: "OpenCode Zen", RateLimits: []providerprotocol.RateLimit{}}
	if u.Go != nil {
		out.SubscriptionType = "OpenCode Go"
		for _, item := range []struct {
			name  string
			meter consoleMeter
		}{{"Rolling", u.Go.Rolling}, {"7 days", u.Go.Weekly}, {"Monthly", u.Go.Monthly}} {
			out.RateLimits = append(out.RateLimits, providerprotocol.RateLimit{Name: item.name, Utilization: item.meter.Percent / 100, ResetsAt: item.meter.ResetsAt})
		}
	}
	for _, budget := range u.Budgets {
		reset := ""
		if budget.ResetsAt > 0 {
			reset = time.UnixMilli(budget.ResetsAt).UTC().Format(time.RFC3339)
		}
		name, description := budget.label()
		out.Budgets = append(out.Budgets, platforms.ProviderBudget{ID: budget.ID, Name: name, Description: description, LimitUSD: budget.Limit, SpentUSD: budget.Spent, RemainingUSD: max(0, budget.Limit-budget.Spent), ResetsAt: reset})
	}
	return out
}
