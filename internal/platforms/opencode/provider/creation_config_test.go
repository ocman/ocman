package opencode

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestCreationConfigurationDefaultsAndRejection(t *testing.T) {
	for _, creation := range []*platforms.CreationIdentity{nil, {}} {
		body := map[string]any{}
		if err := configureCreation(body, creation); err != nil || len(body) != 0 {
			t.Fatalf("unconfigured creation changed defaults: %v %v", body, err)
		}
	}
	body := map[string]any{}
	if err := configureCreation(body, &platforms.CreationIdentity{Configured: true}); err != nil {
		t.Fatal(err)
	}
	if rules, ok := body["permission"].([]platforms.PermissionRule); !ok || rules == nil || len(rules) != 0 {
		t.Fatalf("explicit Default must send an empty ruleset: %#v", body)
	}
	if err := configureCreation(map[string]any{}, &platforms.CreationIdentity{Model: "claudecode:anthropic/sonnet"}); !errors.Is(err, platforms.ErrCreationRejected) {
		t.Fatalf("wrong-platform model must be rejected before native allocation: %v", err)
	}
}
