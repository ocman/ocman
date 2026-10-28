package sessioncatalog

import "github.com/ocman/ocman/internal/platforms/identity"

func IsID(id string) bool {
	return identity.IsSessionID(id)
}
