// Package pricing fetches and caches model pricing from the LiteLLM community
// pricing file and provides a cost calculation helper.
package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
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
type Table struct {
	mu     sync.RWMutex
	prices map[string]ModelPrice // key: lower-cased model name from LiteLLM
	keys   []string              // sorted keys for prefix/suffix matching
}

var (
	globalTable     *Table
	globalTableOnce sync.Once
)

// Load fetches the LiteLLM pricing JSON and returns a Table.
// It is safe to call from multiple goroutines; the fetch happens only once.
func Load() *Table {
	globalTableOnce.Do(func() {
		t := &Table{}
		if err := t.fetch(); err != nil {
			log.WithError(err).Warn("pricing: failed to fetch model pricing; calculated costs will be zero")
			t.prices = map[string]ModelPrice{}
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

func (t *Table) fetch() error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(liteLLMPricingURL)
	if err != nil {
		return fmt.Errorf("GET pricing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET pricing: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read pricing body: %w", err)
	}

	raw := make(map[string]json.RawMessage)
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("unmarshal pricing: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.prices = make(map[string]ModelPrice, len(raw))

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
		t.prices[strings.ToLower(key)] = ModelPrice{
			InputPerToken:      entry.InputCostPerToken,
			OutputPerToken:     entry.OutputCostPerToken,
			CacheReadPerToken:  entry.CacheReadInputTokenCost,
			CacheWritePerToken: entry.CacheCreationInputTokenCost,
		}
	}

	t.keys = make([]string, 0, len(t.prices))
	for k := range t.prices {
		t.keys = append(t.keys, k)
	}
	// Longer keys first so more-specific matches win.
	sort.Slice(t.keys, func(i, j int) bool {
		return len(t.keys[i]) > len(t.keys[j])
	})

	log.WithField("models", len(t.prices)).Info("pricing: loaded model pricing table")
	return nil
}

// Lookup finds pricing for modelID (e.g. "claude-opus-4-5" or "anthropic/claude-opus-4-5").
// It tries exact match first, then suffix/substring matching on the table keys.
// Returns zero ModelPrice if not found.
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

	// 2. Find the longest key that contains needle as a substring, or
	//    needle contains the key as a substring (handles versioned names).
	bestKey := ""
	bestLen := 0
	for _, k := range t.keys {
		if strings.Contains(k, needle) || strings.Contains(needle, k) {
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
