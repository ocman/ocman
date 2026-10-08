package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Project defaults are hub preferences, scoped to an owner and repository.
// Empty values inherit the existing defaults.
type projectDefaults struct {
	Model          string `json:"model"`
	Agent          string `json:"agent"`
	Worktree       string `json:"worktree"`
	PermissionMode string `json:"permissionMode,omitempty"`
}

func projectDefaultsKey(dir, owner string) string {
	if owner == "" {
		owner = "local"
	}
	key, _ := json.Marshal([]string{owner, state.ProjectRootForDirectory(dir)})
	return "project-defaults:" + string(key)
}

func (s *Server) getProjectDefaults(ctx context.Context, dir, owner string) (*projectDefaults, error) {
	raw, ok, err := s.stateDB.GetSetting(ctx, projectDefaultsKey(dir, owner))
	if err != nil || !ok {
		return nil, err
	}
	var defaults projectDefaults
	if err := json.Unmarshal([]byte(raw), &defaults); err != nil {
		return nil, fmt.Errorf("decoding project defaults: %w", err)
	}
	return &defaults, nil
}

func (d projectDefaults) validate() error {
	if d.Model != "" {
		if err := state.ValidateProjectModels([]string{d.Model}); err != nil {
			return err
		}
	}
	if len(d.Agent) > 200 || strings.IndexFunc(d.Agent, unicode.IsControl) >= 0 || strings.TrimSpace(d.Agent) != d.Agent {
		return fmt.Errorf("invalid default agent")
	}
	if d.Worktree != "" && d.Worktree != "worktree" && d.Worktree != "current" {
		return fmt.Errorf("worktree must be worktree, current, or empty")
	}
	if _, err := d.permissionRules(); err != nil {
		return err
	}
	return nil
}

func (d projectDefaults) permissionRules() ([]platforms.PermissionRule, error) {
	switch d.PermissionMode {
	case "", "default":
		return nil, nil
	case "plan":
		return []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "deny"}, {Permission: "bash", Pattern: "*", Action: "deny"}}, nil
	case "auto-edit":
		return []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "allow"}, {Permission: "bash", Pattern: "*", Action: "ask"}}, nil
	case "yolo":
		return []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "allow"}}, nil
	default:
		return nil, fmt.Errorf("invalid default permission mode")
	}
}
