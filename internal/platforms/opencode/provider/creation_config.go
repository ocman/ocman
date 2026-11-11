package opencode

import (
	"fmt"

	"github.com/ocman/ocman/internal/platforms"
)

func configureCreation(body map[string]any, creation *platforms.CreationIdentity) error {
	if creation == nil {
		return nil
	}
	if creation.Configured {
		rules := creation.PermissionRules
		if rules == nil {
			rules = []platforms.PermissionRule{}
		}
		body["permission"] = rules
	}
	if creation.Agent != "" {
		body["agent"] = creation.Agent
	}
	model, err := platforms.ModelFor(platforms.OpenCodeID, creation.Model)
	if err != nil {
		return fmt.Errorf("%w: %w", platforms.ErrCreationRejected, err)
	}
	if ref := parseOpenCodeModelRefInternal(model); ref != nil {
		ref.Variant = creation.Reasoning
		body["model"] = ref
	}
	return nil
}
