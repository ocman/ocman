package platforms

// ProviderBudget is a platform-reported account budget in US dollars, never
// an estimate derived from Ocman's recorded tokens.
type ProviderBudget struct {
	ID           string  `json:"id,omitempty"`
	Name         string  `json:"name"`
	Description  string  `json:"description,omitempty"`
	LimitUSD     float64 `json:"limitUSD"`
	SpentUSD     float64 `json:"spentUSD"`
	RemainingUSD float64 `json:"remainingUSD"`
	ResetsAt     string  `json:"resetsAt,omitempty"`
}
