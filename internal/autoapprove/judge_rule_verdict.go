package autoapprove

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

type ruleScore struct {
	ID                     string
	Tier                   ruleTier
	Probability, Threshold float64
}

func parseTypeSafeRules(body []byte, rules []judgeRule, threshold float64, contextAvailable bool) JudgeResult {
	var response struct {
		Answers map[string]struct {
			Type string   `json:"type"`
			Noul *float64 `json:"noul"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return endpointFailure("Invalid TypeSafe-compatible reviewer response.")
	}
	if len(response.Answers) != len(rules) {
		return endpointFailure("Reviewer omitted rule probabilities or returned unexpected answers.")
	}
	threshold = math.Max(allowThreshold, threshold)
	scores := make([]ruleScore, 0, len(rules))
	for _, rule := range rules {
		answer, ok := response.Answers[rule.ID]
		if !ok || answer.Type != "noul" || answer.Noul == nil || math.IsNaN(*answer.Noul) || math.IsInf(*answer.Noul, 0) || *answer.Noul < 0 || *answer.Noul > 1 {
			return endpointFailure("Invalid reviewer probability for rule " + rule.ID + ".")
		}
		bar := threshold
		if rule.Tier == hardDeny || rule.Tier == softDeny {
			bar = denyThreshold
		}
		scores = append(scores, ruleScore{rule.ID, rule.Tier, *answer.Noul, bar})
	}
	result := JudgeResult{Verdict: verdictUnsafe, ruleScores: scores}
	if !contextAvailable {
		result.Reasoning = "Project or user context is unavailable or too large. Human review required."
		return result
	}
	var hard, soft []string
	var allowed, consent, sufficient, boundary bool
	for _, score := range scores {
		if score.Probability < score.Threshold {
			continue
		}
		switch score.Tier {
		case hardDeny:
			hard = append(hard, score.ID)
		case softDeny:
			soft = append(soft, score.ID)
			if score.ID == "user_boundary" {
				boundary = true
			}
		case allowRule:
			allowed = true
		case evidenceRule:
			switch score.ID {
			case "consent":
				consent = true
			case "sufficient":
				sufficient = true
			}
		}
	}
	switch {
	case len(hard) > 0:
		result.Reasoning = "Hard-deny rules matched: " + strings.Join(hard, ", ") + ". Human review required."
	case !sufficient:
		result.Reasoning = fmt.Sprintf("Insufficient evidence at the %.2f%% confidence threshold. Human review required.", threshold*100)
	case boundary && !consent:
		result.Reasoning = "An unlifted user boundary matched. Human review required."
	case len(soft) > 0 && !allowed && !consent:
		result.Reasoning = "Soft-deny rules matched without an exception or exact user consent: " + strings.Join(soft, ", ") + ". Human review required."
	default:
		result.Verdict = verdictSafe
		result.Reasoning = "No deny rule matched."
		if len(soft) > 0 {
			cleared := "a routine-work exception"
			if consent {
				cleared = "exact user consent"
			}
			result.Reasoning = "Soft-deny rules cleared by " + cleared + ": " + strings.Join(soft, ", ") + "."
		}
	}
	return result
}
