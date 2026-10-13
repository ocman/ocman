package state

import (
	"context"
	"slices"

	"github.com/NoUseFreak/ocman/internal/platforms/identity"
)

const DefaultPlatformSetting = "platform.default"

// SetEnabledPlatforms tells DefaultPlatform which local platforms exist, so
// a saved or built-in default naming a disabled platform is never returned.
// enabled is read on every call; nil keeps the unfiltered behaviour.
func (d *DB) SetEnabledPlatforms(enabled func() []string) {
	if d != nil {
		d.enabledPlatforms.Store(&enabled)
	}
}

// DefaultPlatform is the platform a new session starts on when neither a
// platform nor a prefixed model reference names one. It is always an enabled
// platform when any is enabled (FR-1/2): a saved choice (no longer set from
// the UI; the model picks the platform), else OpenCode, else the lowest id.
func (d *DB) DefaultPlatform(ctx context.Context) (string, error) {
	if d == nil {
		return identity.OpenCode, nil
	}
	value, _, err := d.GetSetting(ctx, DefaultPlatformSetting)
	if value == "" {
		value = identity.OpenCode
	}
	return PickEnabledPlatform(value, d.enabled()), err
}

func (d *DB) enabled() []string {
	if fn := d.enabledPlatforms.Load(); fn != nil && *fn != nil {
		return (*fn)()
	}
	return nil
}

// PickEnabledPlatform returns preferred when it is in enabled (or enabled
// is empty), else OpenCode when enabled, else the lowest enabled id.
func PickEnabledPlatform(preferred string, enabled []string) string {
	if len(enabled) == 0 || slices.Contains(enabled, preferred) {
		return preferred
	}
	if slices.Contains(enabled, identity.OpenCode) {
		return identity.OpenCode
	}
	return slices.Min(enabled)
}
