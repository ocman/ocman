package server

import (
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestFactoryCustomStagePromptsKeepRuntimeProtocol(t *testing.T) {
	for _, stage := range []string{"planning", "scope_expansion", "implementation", "delivery"} {
		t.Run(stage, func(t *testing.T) {
			var sent platforms.SendMessageRequest
			platform := &fakePlatform{id: "opencode", sendMessageFn: func(req platforms.SendMessageRequest) error { sent = req; return nil }}
			registry := platforms.NewRegistry()
			registry.Register(platform)
			srv := New(nil, nil, "", registry, nil)
			session := factory.PlanningSession{Platform: "opencode", ID: "session"}
			custom := "Follow this Formula's instructions: " + stage
			var err error
			action := "complete_attempt"
			if stage == "planning" || stage == "scope_expansion" {
				err = (factoryPlanningLauncher{server: srv}).PromptPlanningSession(t.Context(), session, factory.PlanningSessionRequest{EpicID: "epic", WorkID: "work", AttemptID: "attempt", AgentToken: "secret", Prompt: custom, ScopeExpansion: stage == "scope_expansion"})
				action = "submit_proposal"
				if stage == "scope_expansion" {
					action = "submit_scope_plan"
				}
			} else {
				err = (factoryImplementationLauncher{server: srv}).PromptImplementationSession(t.Context(), session, factory.ImplementationSessionRequest{EpicID: "epic", WorkID: "work", AttemptID: "attempt", AgentToken: "secret", Prompt: custom, Delivery: stage == "delivery"})
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{custom, action, "attempt_id attempt", "attempt_token secret", "Factory protocol:"} {
				if !strings.Contains(sent.Message, text) {
					t.Fatalf("missing %q in %q", text, sent.Message)
				}
			}
			if strings.Contains(sent.Message, factory.DefaultFormulaPrompts()[stage]) {
				t.Fatal("custom prompt did not replace the default")
			}
		})
	}
}
