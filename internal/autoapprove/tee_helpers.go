package autoapprove

// Ocman's own tool-denied helpers are ephemeral and hidden from browsing.
// Suppress only their UI/lifecycle callbacks, not permission/question handling
// or SSE forwarding. The separate history feed still retains their content.
func (t *Tee) internalHelperEvent(kind, data string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	var id string
	switch kind {
	case "session.created", "session.updated":
		props := parseSessionRef(data)
		id = deletedSessionID(data)
		if props.Info != nil && props.Info.ParentID == "" && internalHelperTitle(props.Info.Title) && id != "" {
			if t.internalHelpers == nil {
				t.internalHelpers = map[string]bool{}
			}
			if len(t.internalHelpers) < 4096 {
				t.internalHelpers[id] = true
			}
		}
	case "session.deleted":
		id = deletedSessionID(data)
		hidden := t.internalHelpers[id]
		delete(t.internalHelpers, id)
		return hidden
	case "session.idle", "session.status", "message.updated", "message.removed",
		"message.part.updated", "message.part.removed", "message.part.delta":
		id = messageEventSessionID(data)
	default:
		return false
	}
	return t.internalHelpers[id]
}

func internalHelperTitle(title string) bool {
	return title == "(worktree-name subagent)" || title == "(auto-approve subagent)"
}
