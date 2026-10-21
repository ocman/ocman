package identity

import (
	"strings"

	"github.com/google/uuid"
)

// PendingReference is an Ocman-owned lookup token, not a session identity or
// authority to execute on a remote owner. Only the compatibility integration
// retains the unresolved native reference behind this token.
func PendingReference() string { return "ocref_" + uuid.NewString() }

func IsPendingReference(id string) bool {
	raw, ok := strings.CutPrefix(id, "ocref_")
	if !ok || len(raw) != 36 {
		return false
	}
	parsed, err := uuid.Parse(raw)
	return err == nil && id == "ocref_"+parsed.String()
}

func IsSessionID(id string) bool {
	raw, ok := strings.CutPrefix(id, "ocm_")
	if !ok {
		return false
	}
	parsed, err := uuid.Parse(raw)
	return err == nil && id == "ocm_"+parsed.String()
}
