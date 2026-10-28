package protocol

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestProjectSessionsNeedExplicitGrantAndLocalResolution(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		grant, declared, resolver, known bool
	}{
		{"allowed", true, true, true, true},
		{"grant missing", false, true, true, true},
		{"old capability", true, false, true, true},
		{"resolver missing", true, true, false, true},
		{"unknown project", true, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := sessionDescription()
			if tc.declared {
				d.Capabilities = []Capability{ProjectSessionCapability}
			}
			grants := []string{SessionRunGrant}
			if tc.grant {
				grants = append(grants, SessionProjectGrant)
			}
			h := &sessionHarness{description: d, grants: grants}
			b := h.broker()
			if tc.resolver {
				b.ResolveProject = func(_ context.Context, id string) (string, error) {
					if id != "project-a" || !tc.known {
						return "", errors.New("not local")
					}
					return "/local/project-a", nil
				}
			}
			call := sessionRunEvent(t, SessionRunParams{ProjectID: "project-a", Prompt: "analyze", NoTools: true})
			call.Version = ProjectSessionCapability.Version
			_, err := b.Run(t.Context(), "io.ocman.test", call)
			if tc.name == "allowed" {
				if err != nil || len(h.started) != 1 || !strings.HasPrefix(h.started[0], "/local/project-a|/local/project-a|") {
					t.Fatalf("local project not used: %v %v", err, h.started)
				}
			} else if err == nil || len(h.started) != 0 {
				t.Fatalf("unauthorized run started: %v", err)
			}
		})
	}
}

func TestProjectSessionDeclarationHasNoRequiredConfiguration(t *testing.T) {
	d := sessionDescription()
	d.Capabilities, d.Settings = []Capability{ProjectSessionCapability}, nil
	d.RequestedGrants = []string{SessionRunGrant, SessionProjectGrant}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	d.RequestedGrants = []string{SessionRunGrant}
	if err := d.Validate(); err == nil {
		t.Fatal("unconfigured legacy session declaration accepted")
	}
}

func TestSessionUsageValidation(t *testing.T) {
	for _, usage := range []SessionUsage{{PromptTokens: -1}, {CompletionTokens: -1}, {CostUSD: -1}, {CostUSD: math.NaN()}, {CostUSD: math.Inf(1)}} {
		if usage.Validate() == nil {
			t.Fatal("invalid usage accepted")
		}
	}
	if (SessionUsage{}).Validate() != nil {
		t.Fatal("known zero usage rejected")
	}
	if (SessionCompleted{SessionID: "s", Text: "reply", Usage: &SessionUsage{CostUSD: -1}}).Validate() == nil {
		t.Fatal("invalid completion accepted")
	}
}
