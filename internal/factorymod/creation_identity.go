package factorymod

import (
	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
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
