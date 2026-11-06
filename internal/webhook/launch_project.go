package webhook

import (
	"context"
	"fmt"
	"strings"

	"github.com/ocman/ocman/internal/state"
)

type projectDispatcher interface {
	RunWebhookInProject(context.Context, string, string, string, int64, string) (state.RoutineRun, error)
}

func validProjectPointer(pointer string) bool {
	if pointer == "" {
		return true
	}
	if !strings.HasPrefix(pointer, "/") {
		return false
	}
	for _, token := range strings.Split(pointer[1:], "/") {
		if _, ok := unescapePointerToken(token); !ok {
			return false
		}
	}
	return true
}

func launchWebhook(ctx context.Context, svc RoutineDispatcher, routine state.Routine, sub state.WebhookSubscription, body []byte, data, label string, occurrence int64) (state.RoutineRun, error) {
	// Existing fixed-project subscriptions do not inspect project fields at all.
	if routine.Directory != "" || sub.ProjectPointer == "" {
		return svc.RunWebhook(ctx, sub.RoutineID, data, label, occurrence)
	}
	root, err := decodeJSON(body)
	if err != nil {
		return state.RoutineRun{}, fmt.Errorf("project field requires a JSON webhook body")
	}
	value, ok := pointerValue(root, sub.ProjectPointer)
	project, text := value.(string)
	if !ok || !text || strings.TrimSpace(project) == "" {
		return state.RoutineRun{}, fmt.Errorf("webhook project field must contain a nonempty string")
	}
	dispatcher, ok := svc.(projectDispatcher)
	if !ok {
		return state.RoutineRun{}, fmt.Errorf("routine dispatcher does not support launch projects")
	}
	return dispatcher.RunWebhookInProject(ctx, sub.RoutineID, data, label, occurrence, project)
}
