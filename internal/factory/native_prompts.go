package factory

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

//go:embed tracer-v2.toml
var tracerFormulaV2Source string

// Older revisions without explicit prompts inherit the frozen v2 Formula text.
func DefaultFormulaPrompts() map[string]string {
	compiled, err := compileNativeFormula(tracerFormulaV2Source)
	if err != nil {
		panic("invalid embedded Formula: " + err.Error())
	}
	return compiled.Prompts
}

func effectiveFormulaPrompts(overrides map[string]string) map[string]string {
	prompts := DefaultFormulaPrompts()
	for stage, text := range overrides {
		prompts[stage] = text
	}
	return prompts
}

// Resolve the nearest Formula-owning Mol, including composed child Formulas.
func (s *NativeService) issuePrompt(ctx context.Context, epic model.NativeEpic, issueID, stage string) (string, error) {
	issues, err := s.store.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		return "", err
	}
	byID := make(map[string]model.NativeIssue, len(issues))
	for _, issue := range issues {
		byID[issue.ID] = issue
	}
	id, revision, hash := epic.FormulaID, epic.FormulaVersion, epic.FormulaHash
	for steps := 0; issueID != ""; steps++ {
		issue, found := byID[issueID]
		if !found || steps >= len(issues) {
			return "", fmt.Errorf("%w: invalid Formula ancestry", ErrFormulaCorrupt)
		}
		if issue.Workflow != nil {
			if stage == "scope_expansion" {
				for _, candidate := range issues {
					if candidate.Workflow != nil && candidate.Workflow.Kind == "planning" && candidate.Workflow.Config.ScopeExpansionPrompt != "" {
						return candidate.Workflow.Config.ScopeExpansionPrompt, nil
					}
				}
				return DefaultFormulaPrompts()[stage], nil
			}
			return issue.Workflow.Prompt, nil
		}
		if issue.FormulaID != "" {
			id, revision, hash = issue.FormulaID, issue.FormulaVersion, issue.FormulaHash
			break
		}
		issueID = issue.ParentID
	}
	formula, err := s.GetFormula(ctx, id, revision)
	if err != nil {
		return "", err
	}
	legacySourcePin := id == "ocman/tracer" && revision == 1 && formula.SourceHash == hash
	if formula.Hash != hash && !legacySourcePin {
		return "", fmt.Errorf("%w: pinned Formula hash mismatch", ErrFormulaCorrupt)
	}
	return formula.Prompts[stage], nil
}
