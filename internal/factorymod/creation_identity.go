package factorymod

import (
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/google/uuid"
)

func factoryCreationIdentity(attemptID, epicID, workID string) *platforms.CreationIdentity {
	originID := attemptID
	if originID == "" {
		originID = epicID
	}
	if originID == "" {
		originID = workID
	}
	return &platforms.CreationIdentity{RequestKey: uuid.NewString(), Origin: "factory", OriginID: originID}
}
