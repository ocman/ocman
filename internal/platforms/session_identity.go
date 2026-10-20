package platforms

import "context"

type SessionIdentityRequest struct {
	ID       string `json:"id,omitempty"`
	NativeID string `json:"nativeId,omitempty"`
}

// SessionIdentity is an owner-authenticated binding, not an execution handle.
// SourceKey stays on the backend; public views omit this physical locator.
type SessionIdentity struct {
	ID             string `json:"id"`
	OwnerID        string `json:"ownerId"`
	Platform       string `json:"platform"`
	SourceKey      string `json:"sourceKey,omitempty"`
	NativeID       string `json:"nativeId"`
	ParentID       string `json:"parentId,omitempty"`
	NativeParentID string `json:"nativeParentId,omitempty"`
	Origin         string `json:"origin"`
	OriginID       string `json:"originId,omitempty"`
	CreationState  string `json:"creationState"`
	Purged         bool   `json:"purged,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

// SessionIdentityResolver is implemented by an owner-routed adapter. Unknown
// Ocman IDs must never be retried as native IDs on another owner.
type SessionIdentityResolver interface {
	SessionIdentity(context.Context, SessionIdentityRequest) (*SessionIdentity, error)
	SessionIdentities(context.Context, []string) ([]SessionIdentity, error)
	SessionOwnerID() string
}
