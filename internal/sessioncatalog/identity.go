package sessioncatalog

import "github.com/NoUseFreak/ocman/internal/platforms/identity"

func IsID(id string) bool {
	return identity.IsSessionID(id)
}
