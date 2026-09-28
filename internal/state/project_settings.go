package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// ProjectSettings is a project's ordered model list. Models[0] is the
// project default; the rest are fallthrough candidates. Off disables
// fallthrough while keeping the list configured.
type ProjectSettings struct {
	Models []string `json:"models"`
	Off    bool     `json:"off"`
}

const (
	maxProjectModels   = 10
	maxProjectModelLen = 300
)

// projectModelRe is provider/model: a slash-free provider, then a model
// id that may itself contain slashes (openrouter/anthropic/claude-x).
var projectModelRe = regexp.MustCompile(`^[^\s/]+/\S+$`)

// ValidateProjectModels checks shape only. It deliberately does not
// consult the live provider catalogue: a provider disconnected today may
// be connected tomorrow, and the runtime skips unusable models anyway.
func ValidateProjectModels(models []string) error {
	if len(models) > maxProjectModels {
		return fmt.Errorf("at most %d models allowed", maxProjectModels)
	}
	seen := make(map[string]bool, len(models))
	for _, m := range models {
		if len(m) > maxProjectModelLen {
			return fmt.Errorf("model %.40q... exceeds %d characters", m, maxProjectModelLen)
		}
		if !projectModelRe.MatchString(m) {
			return fmt.Errorf("model %q must be provider/model with no whitespace", m)
		}
		if seen[m] {
			return fmt.Errorf("duplicate model %q", m)
		}
		seen[m] = true
	}
	return nil
}

// projectSettingKey folds dir to its project root so a worktree shares
// its repository's entry.
func projectSettingKey(dir string) string {
	return "project:" + ProjectRootForDirectory(dir)
}

// GetProjectSettings returns the settings for dir's project, or empty
// settings (non-nil Models) when it was never configured.
func (d *DB) GetProjectSettings(ctx context.Context, dir string) (ProjectSettings, error) {
	ps := ProjectSettings{Models: []string{}}
	val, ok, err := d.GetSetting(ctx, projectSettingKey(dir))
	if err != nil || !ok {
		return ps, err
	}
	if err := json.Unmarshal([]byte(val), &ps); err != nil {
		return ProjectSettings{Models: []string{}}, fmt.Errorf("decoding project settings: %w", err)
	}
	if ps.Models == nil {
		ps.Models = []string{}
	}
	return ps, nil
}

// ErrInvalidProjectSettings wraps validation failures so callers can map
// them to a 400.
var ErrInvalidProjectSettings = errors.New("invalid project settings")

// SetProjectSettings validates and persists ps for dir's project. An
// empty model list removes the entry instead of storing an empty blob.
func (d *DB) SetProjectSettings(ctx context.Context, dir string, ps ProjectSettings) error {
	if err := ValidateProjectModels(ps.Models); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidProjectSettings, err)
	}
	key := projectSettingKey(dir)
	if len(ps.Models) == 0 {
		if _, err := d.db.ExecContext(ctx, `DELETE FROM setting WHERE key = ?`, key); err != nil {
			return fmt.Errorf("deleting setting %q: %w", key, err)
		}
		return nil
	}
	raw, err := json.Marshal(ps)
	if err != nil {
		return err
	}
	return d.SetSetting(ctx, key, string(raw))
}
