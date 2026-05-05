// Package pricing fetches and caches model pricing from the LiteLLM community
// pricing file and provides a cost calculation helper.
package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const liteLLMPricingURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// ModelPrice holds per-token costs for a single model (all values are per token, not per million).
type ModelPrice struct {
	InputPerToken      float64
	OutputPerToken     float64
	CacheReadPerToken  float64
	CacheWritePerToken float64
}

// Table is a loaded pricing table.
//
// A *Table can be constructed in two ways:
//
//   - Production callers use [Load], which returns a process-wide
//     default Table populated lazily from the LiteLLM URL.
//   - Tests use [New] (with a [httptest.Server] URL and a real
//     [*http.Client]) to obtain a fully isolated instance with no
//     shared global state.
type Table struct {
	httpClient *http.Client
	url        string

	mu     sync.RWMutex
	prices map[string]ModelPrice // key: lower-cased model name from LiteLLM
	keys   []string              // sorted keys for prefix/suffix matching
}

// New returns a fresh Table that fetches pricing from url using client.
// The Table is empty until [Table.LoadCtx] is called.
//
// New does not consult or mutate any package-level state, so each call
// returns a fully independent Table — safe for parallel tests.
func New(client *http.Client, url string) *Table {
	if client == nil {
		client = http.DefaultClient
	}
	return &Table{
		httpClient: client,
		url:        url,
	}
}

var (
	globalTable     *Table
	globalTableOnce sync.Once
)

// Load fetches the LiteLLM pricing JSON and returns the process-wide
// default Table. It is safe to call from multiple goroutines; the
// fetch happens only once.
//
// Tests should prefer [New] + [Table.LoadCtx] for isolated state.
func Load() *Table {
	globalTableOnce.Do(func() {
		t := New(&http.Client{
			Timeout:   15 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}, liteLLMPricingURL)
		if err := t.LoadCtx(context.Background()); err != nil {
			// FR-3: failure to load pricing is observable via the
			// existing logrus logger. We deliberately keep the
			// returned Table non-nil with an empty price map so
			// callers continue to see "no match" / cost=0 — the
			// behaviour they had before — but the maintainer can
			// see in the logs that pricing was never populated.
			log.WithError(err).
				WithField("url", liteLLMPricingURL).
				Warn("pricing: failed to fetch model pricing; calculated costs will be zero")
		}
		globalTable = t
	})
	return globalTable
}

// liteLLMEntry mirrors the relevant fields from the LiteLLM pricing JSON.
type liteLLMEntry struct {
	Mode                    string  `json:"mode"`
	InputCostPerToken       float64 `json:"input_cost_per_token"`
	OutputCostPerToken      float64 `json:"output_cost_per_token"`
	CacheReadInputTokenCost float64 `json:"cache_read_input_token_cost"`
	// LiteLLM calls cache-write "cache_creation_input_token_cost"
	CacheCreationInputTokenCost float64 `json:"cache_creation_input_token_cost"`
}

// LoadCtx fetches the pricing JSON, parses it, and replaces the
// Table's contents on success. On error it returns the error and
// leaves the table empty (or as it was — fetch is all-or-nothing).
//
// LoadCtx logs a single WARN line on failure (FR-3) including the
// configured URL and the underlying error so a misconfigured pricing
// source is observable in production.
func (t *Table) LoadCtx(ctx context.Context) error {
	if t == nil {
		return fmt.Errorf("pricing: nil table")
	}
	url := t.url
	if url == "" {
		url = liteLLMPricingURL
	}
	client := t.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.logFetchFailure(url, err)
		return fmt.Errorf("build pricing request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.logFetchFailure(url, err)
		return fmt.Errorf("GET pricing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("GET pricing: HTTP %d", resp.StatusCode)
		t.logFetchFailure(url, err)
		return err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.logFetchFailure(url, err)
		return fmt.Errorf("read pricing body: %w", err)
	}

	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(body, &raw); err != nil {
		t.logFetchFailure(url, err)
		return fmt.Errorf("unmarshal pricing: %w", err)
	}

	prices := make(map[string]ModelPrice, len(raw))
	for key, val := range raw {
		var entry liteLLMEntry
		if err := json.Unmarshal(val, &entry); err != nil {
			continue
		}
		if entry.Mode != "chat" && entry.Mode != "completion" {
			continue
		}
		if entry.InputCostPerToken == 0 && entry.OutputCostPerToken == 0 {
			continue
		}
		prices[strings.ToLower(key)] = ModelPrice{
			InputPerToken:      entry.InputCostPerToken,
			OutputPerToken:     entry.OutputCostPerToken,
			CacheReadPerToken:  entry.CacheReadInputTokenCost,
			CacheWritePerToken: entry.CacheCreationInputTokenCost,
		}
	}

	t.mu.Lock()
	t.prices = prices
	t.rebuildKeysLocked()
	t.mu.Unlock()

	log.WithField("models", len(prices)).Info("pricing: loaded model pricing table")
	return nil
}

func (t *Table) logFetchFailure(url string, err error) {
	log.WithError(err).
		WithField("url", url).
		Warn("pricing: failed to fetch model pricing; calculated costs will be zero")
}

// rebuildKeysLocked rebuilds the sorted-by-length-desc key list. The
// caller must hold t.mu for write.
func (t *Table) rebuildKeysLocked() {
	t.keys = make([]string, 0, len(t.prices))
	for k := range t.prices {
		t.keys = append(t.keys, k)
	}
	// Longer keys first so more-specific matches win.
	sort.Slice(t.keys, func(i, j int) bool {
		return len(t.keys[i]) > len(t.keys[j])
	})
}

// rebuildKeys is a test helper (no lock).
func (t *Table) rebuildKeys() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rebuildKeysLocked()
}

// Lookup finds pricing for modelID (e.g. "claude-opus-4-5" or
// "anthropic/claude-opus-4-5"). Matching rules, in order:
//
//  1. The provider prefix ("foo/") is stripped if present.
//  2. An exact (case-insensitive) match against a table key wins.
//  3. Otherwise we look for the longest table key that is either a
//     prefix of the query (handles versioned names like
//     "gpt-4-turbo-2024-04-09" → "gpt-4-turbo") or contains the
//     query as a substring.
//
// Returns zero [ModelPrice] when no match is found. A nil receiver is
// safe and returns zero — callers that fall back to "no pricing"
// behaviour don't need a nil check.
//
// Known limitation: if the table contains both "gpt-4" and "gpt-4o",
// the query "gpt-4" returns the exact match (rule 2). The query
// "gpt-4o" likewise wins exactly. Ambiguity only arises for queries
// that are not exact matches — there we prefer the longest prefix,
// which is usually correct (the model id includes a version
// suffix).
func (t *Table) Lookup(modelID string) ModelPrice {
	if t == nil {
		return ModelPrice{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	needle := strings.ToLower(strings.TrimSpace(modelID))
	// Strip provider prefix if present (e.g. "anthropic/claude-..." → "claude-...")
	if idx := strings.LastIndex(needle, "/"); idx >= 0 {
		needle = needle[idx+1:]
	}
	if needle == "" {
		return ModelPrice{}
	}

	// 1. Exact match.
	if p, ok := t.prices[needle]; ok {
		return p
	}

	// 2. Find the longest key that is a prefix of needle, OR (as a
	//    fallback) any longer key that contains needle. Prefix-of-needle
	//    handles the common case of a versioned id ("gpt-4-turbo-2024")
	//    mapping to its base entry ("gpt-4-turbo"). The contains-needle
	//    fallback covers a handful of upstream rows whose key is a
	//    decorated form of the model id ("gpt-4o-2024-05-13" in the
	//    table, queried as "gpt-4o").
	bestKey := ""
	bestLen := 0
	for _, k := range t.keys {
		if strings.HasPrefix(needle, k) {
			if len(k) > bestLen {
				bestKey = k
				bestLen = len(k)
			}
		}
	}
	if bestKey != "" {
		return t.prices[bestKey]
	}
	for _, k := range t.keys {
		if strings.Contains(k, needle) {
			if len(k) > bestLen {
				bestKey = k
				bestLen = len(k)
			}
		}
	}
	if bestKey != "" {
		return t.prices[bestKey]
	}

	return ModelPrice{}
}

// CalcCost computes the API cost for a request given token counts and a pricing table.
func (t *Table) CalcCost(modelID string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) float64 {
	p := t.Lookup(modelID)
	if p.InputPerToken == 0 && p.OutputPerToken == 0 {
		return 0
	}
	return float64(inputTokens)*p.InputPerToken +
		float64(outputTokens)*p.OutputPerToken +
		float64(cacheReadTokens)*p.CacheReadPerToken +
		float64(cacheWriteTokens)*p.CacheWritePerToken
}
