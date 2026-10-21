package owned

import "github.com/NoUseFreak/ocman/internal/sessionview"

var eventProperties = &sessionview.Shape{Variants: map[string]*sessionview.Shape{}}

func init() {
	message := &sessionview.Shape{References: sessionview.ReferenceShape.References, Children: map[string]*sessionview.Shape{"info": sessionview.ReferenceShape, "part": sessionview.ReferenceShape, "message": sessionview.ReferenceShape}}
	session := &sessionview.Shape{References: sessionview.ReferenceShape.References, Children: map[string]*sessionview.Shape{"info": sessionview.SessionShape}}
	for _, kind := range []string{"session.created", "session.updated", "session.deleted"} {
		eventProperties.Variants[kind] = &sessionview.Shape{References: sessionview.ReferenceShape.References, Children: map[string]*sessionview.Shape{"properties": session, "info": sessionview.SessionShape}}
	}
	for _, kind := range []string{"message.updated", "message.removed", "message.part.updated", "message.part.removed", "message.part.delta", "ocman.queue.changed"} {
		eventProperties.Variants[kind] = &sessionview.Shape{References: sessionview.ReferenceShape.References, Children: map[string]*sessionview.Shape{"properties": message, "info": sessionview.ReferenceShape, "part": sessionview.ReferenceShape, "message": sessionview.ReferenceShape}}
	}
	for _, kind := range []string{"session.status", "session.idle", "session.error", "session.diff", "session.compacted", "permission.asked", "permission.v2.asked", "permission.updated", "permission.replied", "permission.v2.replied", "permission.rejected", "question.asked", "question.replied", "question.rejected", "ocman.session.background", "ocman.session.changed"} {
		eventProperties.Variants[kind] = &sessionview.Shape{References: sessionview.ReferenceShape.References, Children: map[string]*sessionview.Shape{"properties": sessionview.ReferenceShape}}
	}
	// OpenCode's owner-global stream wraps its ordinary event in payload.
	eventProperties.Variants[""] = &sessionview.Shape{Children: map[string]*sessionview.Shape{"payload": eventProperties}}
}
