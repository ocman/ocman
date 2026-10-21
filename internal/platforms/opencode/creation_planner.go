package opencode

import (
	"strings"

	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
)

// V2 accepts a client-chosen session primary key at creation. Derive it from
// the owner's random canonical ID so a lost HTTP response cannot lose identity.
// V1 and forks retain their existing uncertain-outcome behavior.
func (a *Adapter) PlannedSessionID(canonicalID string) string {
	if !ocv2.InstalledV2() || !identity.IsSessionID(canonicalID) {
		return ""
	}
	return "ses_ocman" + strings.ReplaceAll(strings.TrimPrefix(canonicalID, "ocm_"), "-", "")
}
