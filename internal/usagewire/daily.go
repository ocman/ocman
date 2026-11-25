package usagewire

const OtherModel = "other"

// Only exact, known public model IDs may leave an installation. Never accept
// arbitrary aliases, endpoint names or provider strings, even from a relay caller.
var publicModels = map[string]string{
	"gpt-5": "gpt-5", "gpt-5.1": "gpt-5.1", "gpt-5.2": "gpt-5.2",
	"gpt-5.3-codex": "gpt-5.3-codex", "gpt-5.4": "gpt-5.4", "gpt-5.5": "gpt-5.5",
	"claude-sonnet-4-5": "claude-sonnet-4-5", "claude-sonnet-4-6": "claude-sonnet-4-6",
	"claude-opus-4-5": "claude-opus-4-5", "claude-opus-4-6": "claude-opus-4-6",
	"claude-opus-4-7": "claude-opus-4-7", "claude-haiku-4-5": "claude-haiku-4-5",
	"gemini-2.5-pro": "gemini-2.5-pro", "gemini-2.5-flash": "gemini-2.5-flash",
}

func ModelBucket(model string) string {
	if canonical, ok := publicModels[model]; ok {
		return canonical
	}
	return OtherModel
}

// Daily is a complete closed-day snapshot. An active session had at least one
// valid assistant message on that UTC day, even if token accounting is missing.
type Daily struct {
	Sessions int64        `json:"sessions"`
	Models   []ModelUsage `json:"models"`
}

type ModelUsage struct {
	Model        string `json:"model"`
	Sessions     int64  `json:"sessions"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

func (d Daily) Valid() bool {
	if d.Sessions <= 0 || d.Sessions > 1_000_000 || len(d.Models) == 0 || len(d.Models) > len(publicModels)+1 {
		return false
	}
	seen := make(map[string]bool, len(d.Models))
	var total int64
	for _, m := range d.Models {
		if (m.Model != OtherModel && ModelBucket(m.Model) == OtherModel) || seen[m.Model] ||
			m.Sessions <= 0 || m.Sessions > d.Sessions ||
			m.InputTokens < 0 || m.InputTokens > 1_000_000_000_000 ||
			m.OutputTokens < 0 || m.OutputTokens > 1_000_000_000_000 {
			return false
		}
		seen[m.Model] = true
		total += m.Sessions
	}
	return total >= d.Sessions
}
