package server

import (
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/sessionview"
)

var inboxSessionShape = &sessionview.Shape{OwnerField: "remoteId", Children: map[string]*sessionview.Shape{
	"session": sessionview.ReferenceShape, "permission": sessionview.ReferenceShape,
}}

var inboxResponseShape = &sessionview.Shape{OwnerField: "remoteId", Children: map[string]*sessionview.Shape{
	"items": inboxSessionShape, "session": sessionview.ReferenceShape, "permission": sessionview.ReferenceShape,
}}

var routineSessionShape = func() *sessionview.Shape {
	s := &sessionview.Shape{OwnerField: "remoteId", References: []string{"sessionId", "targetSessionId"}}
	s.Children = map[string]*sessionview.Shape{"latestRun": s}
	return s
}()

var factorySessionShape = func() *sessionview.Shape {
	s := &sessionview.Shape{References: sessionview.ReferenceShape.References}
	s.Children = map[string]*sessionview.Shape{
		"session": sessionview.SessionShape, "attempts": s, "dispatch": s,
		"controlSession": sessionview.ReferenceShape,
	}
	return s
}()

var artifactSessionShape = &sessionview.Shape{References: sessionview.ReferenceShape.References,
	Children: map[string]*sessionview.Shape{"artifacts": sessionview.ReferenceShape, "owners": sessionview.ReferenceShape},
}

var webhookSessionShape = &sessionview.Shape{References: sessionview.ReferenceShape.References,
	Children: map[string]*sessionview.Shape{"deliveries": {Children: map[string]*sessionview.Shape{"dispatches": sessionview.ReferenceShape}}, "dispatches": sessionview.ReferenceShape},
}

var taskSessionShape = &sessionview.Shape{Children: map[string]*sessionview.Shape{
	"tasks": {Dictionary: sessionview.ConversationShape},
}}

// Public contracts are deliberately route-specific. Unknown feature payloads
// are not recursively searched for ID-like keys. A new endpoint returning a
// session reference must register its normalized shape here.
func publicSessionContract(r *http.Request) (*sessionview.Shape, bool) {
	path := r.URL.Path
	switch {
	case path == "/api/sessions":
		return sessionview.SessionShape, r.Method == http.MethodGet
	case path == "/api/sessions/notify":
		return sessionview.SessionShape, true
	case path == "/api/sessions/start":
		return sessionview.ReferenceShape, false
	case path == "/api/events":
		return sessionview.ConversationShape, false
	case strings.HasPrefix(path, "/api/session/"):
		if strings.HasSuffix(path, "/fork") {
			return sessionview.SessionShape, false
		}
		if strings.HasSuffix(path, "/tasks") {
			return taskSessionShape, false
		}
		return sessionview.ConversationShape, false
	case path == "/api/routines" || strings.HasPrefix(path, "/api/routines/"):
		return routineSessionShape, false
	case path == "/api/inbox" || strings.HasPrefix(path, "/api/inbox/"):
		return inboxResponseShape, false
	case strings.HasPrefix(path, "/api/factory/"):
		return factorySessionShape, false
	case path == "/api/worktree/create-and-launch" || path == "/api/project/handle":
		return sessionview.ReferenceShape, false
	case path == "/api/shares":
		return sessionview.ReferenceShape, false
	case path == "/api/artifacts" || strings.HasPrefix(path, "/api/artifacts/"):
		return artifactSessionShape, false
	case path == "/api/webhook-inboxes" || strings.HasPrefix(path, "/api/webhook-inboxes/"):
		return webhookSessionShape, false
	default:
		return nil, false
	}
}
