package factory

import (
	"encoding/json"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func nativeProposal(proposal model.NativeProposalRevision) (ProposalRevision, error) {
	var stored struct {
		ProposalManifest
		Issues         []model.NativeIssue `json:"issues"`
		ExternalIssues []model.NativeIssue `json:"externalIssues"`
		BaseIssues     []model.NativeIssue `json:"baseIssues"`
	}
	if err := json.Unmarshal([]byte(proposal.ManifestJSON), &stored); err != nil {
		return ProposalRevision{}, fmt.Errorf("decoding proposal manifest: %w", err)
	}
	manifest := stored.ProposalManifest
	if stored.Issues != nil {
		manifest.Issues = nativeIssues(stored.Issues)
	}
	if stored.ExternalIssues != nil {
		manifest.ExternalIssues = nativeIssues(stored.ExternalIssues)
	}
	if stored.BaseIssues != nil {
		manifest.BaseIssues = nativeIssues(stored.BaseIssues)
	}
	return ProposalRevision{EpicID: proposal.EpicID, MolID: proposal.MolID, Project: proposal.Project, Revision: proposal.Revision, Manifest: manifest, RationaleMarkdown: proposal.RationaleMarkdown, ContentHash: proposal.ContentHash, CreatedAt: proposal.CreatedAt}, nil
}
