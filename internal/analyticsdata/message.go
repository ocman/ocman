// Package analyticsdata holds the shared message accounting and timing rules
// used by legacy readers and ingestion-maintained history summaries.
package analyticsdata

import "encoding/json"

type MessageData struct {
	Role           string          `json:"role"`
	Agent          string          `json:"agent"`
	Mode           string          `json:"mode"`
	ModelID        string          `json:"modelID"`
	ProviderID     string          `json:"providerID"`
	Cost           float64         `json:"cost"`
	UsageAvailable *bool           `json:"usageAvailable,omitempty"`
	Tokens         *TokenInfo      `json:"tokens"`
	Time           *TimeInfo       `json:"time"`
	Model          *ModelRef       `json:"model"`
	Finish         string          `json:"finish"`
	Error          json.RawMessage `json:"error"`
}
type TokenInfo struct {
	Input     int64      `json:"input"`
	Output    int64      `json:"output"`
	Reasoning int64      `json:"reasoning"`
	Cache     *CacheInfo `json:"cache"`
}
type CacheInfo struct {
	Read  int64 `json:"read"`
	Write int64 `json:"write"`
}
type TimeInfo struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
}
type ModelRef struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

func ModelProvider(md MessageData) (provider, model string) {
	provider, model = md.ProviderID, md.ModelID
	if provider == "" && md.Model != nil {
		provider, model = md.Model.ProviderID, md.Model.ModelID
	}
	return provider, model
}

func ModelKey(provider, model string) string {
	if model == "" {
		return ""
	}
	if provider == "" {
		return model
	}
	return provider + "/" + model
}
