package platforms

// CreationIdentity travels with owner-routed creation, including across gRPC.
// It describes Ocman's intent, not platform-specific execution parameters.
type CreationIdentity struct {
	RequestKey      string           `json:"requestKey"`
	Origin          string           `json:"origin"`
	OriginID        string           `json:"originId,omitempty"`
	ParentID        string           `json:"parentId,omitempty"`
	Configured      bool             `json:"configured,omitempty"`
	PermissionRules []PermissionRule `json:"permissionRules,omitempty"`
	// Archived hides the session from the sidebar from creation onwards. The
	// caller decides; origin never implies it.
	Archived bool `json:"archived,omitempty"`
}
