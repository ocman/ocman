package plugins

import "time"

// The launch-time catalog opt-in delivers a read-only snapshot of the plugin's
// project directory as one more member of the fd-3 configuration object,
// beside the declared settings and delivered exactly like secrets: it never
// appears in arguments or environment variables.

const (
	// CatalogProjectSetting names the required setting holding the one
	// directory the catalog is resolved for. It mirrors the conversation.v1
	// required project setting and is the same setting when both are used.
	CatalogProjectSetting = "project"
	// CatalogLaunchKey is the reserved fd-3 member holding the snapshot. It
	// is never a declared setting key; Description.Validate rejects that
	// combination so the injected member cannot shadow a setting.
	CatalogLaunchKey = "catalog"
)

// The catalog DTOs mirror the host's directory catalog JSON. Unknown fields
// are ignored in both directions so the host may extend the snapshot without
// breaking opted-in plugins.

// CatalogAgent is one composer-level agent offered for the directory.
type CatalogAgent struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Model       string `json:"model,omitempty"`
	Color       string `json:"color,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Hidden      bool   `json:"hidden,omitempty"`
	BuiltIn     bool   `json:"builtIn,omitempty"`
}

// CatalogModel is one selectable model for the directory.
type CatalogModel struct {
	Provider          string     `json:"provider"`
	ProviderName      string     `json:"providerName,omitempty"`
	Model             string     `json:"model"`
	ModelName         string     `json:"modelName,omitempty"`
	RecentRank        int        `json:"recentRank,omitempty"`
	IsSessionDefault  bool       `json:"isSessionDefault,omitempty"`
	IsProviderDefault bool       `json:"isProviderDefault,omitempty"`
	IsAvailable       bool       `json:"isAvailable"`
	IsFavorite        bool       `json:"isFavorite,omitempty"`
	Reasoning         []string   `json:"reasoning,omitempty"`
	CooldownUntil     *time.Time `json:"cooldownUntil,omitempty"`
}

// CatalogModels is the model catalog with its defaults.
type CatalogModels struct {
	SessionDefault   string            `json:"sessionDefault,omitempty"`
	ProviderDefaults map[string]string `json:"providerDefaults,omitempty"`
	HasProviders     bool              `json:"hasProviders"`
	Models           []CatalogModel    `json:"models"`
}

// LaunchCatalog is the snapshot added under CatalogLaunchKey when the plugin
// opted in, the project setting resolved, and a local platform instance was
// reachable. Any of those failing omits the member entirely; the catalog is
// launch configuration, never a startup requirement.
type LaunchCatalog struct {
	Agents       []CatalogAgent `json:"agents"`
	Models       *CatalogModels `json:"models,omitempty"`
	DefaultAgent string         `json:"defaultAgent,omitempty"`
	DefaultModel string         `json:"defaultModel,omitempty"`
}

// validateCatalog enforces the opt-in contract: wantsCatalog is only
// meaningful when the plugin declares the single required, non-secret project
// setting the catalog is resolved for, mirroring the conversation.v1
// required-project trio. Non-opted plugins are unconstrained.
func (d Description) validateCatalog() error {
	if !d.WantsCatalog {
		return nil
	}
	project := false
	for _, s := range d.Settings {
		if s.Key == CatalogLaunchKey {
			// The injected member would silently replace the setting value.
			return ErrInvalidMessage
		}
		if s.Key == CatalogProjectSetting {
			if s.Type != "string" || !s.Required || s.Secret || s.List {
				return ErrInvalidMessage
			}
			project = true
		}
	}
	if !project {
		return ErrInvalidMessage
	}
	return nil
}
