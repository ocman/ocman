package v1map

import "github.com/ocman/ocman/internal/convevents"

// session maps session lifecycle frames: the page session's metadata becomes
// session.updated, descendants become child.upserted.
func (m *Mapper) session(r *Result, typ string, p obj) {
	info := p.obj("info")
	id := info.str("id")
	if id == "" {
		id = p.str("sessionID")
	}
	parent := info.str("parentID")
	inTree := parent != "" && (parent == m.opts.SessionID || m.isChild(parent))
	switch {
	case id == "":
	case typ == "session.deleted":
		if m.isChild(id) {
			delete(m.children, id)
			delete(m.childLevel, id)
			child := childOf(id, info)
			child.Deleted = true
			m.emit(r, convevents.ChildUpserted{Child: child})
		}
	case id == m.opts.SessionID:
		if typ == "session.updated" {
			m.emit(r, convevents.SessionUpdated{Patch: patchOf(info)})
		}
	case inTree || m.isChild(id):
		m.children[id] = true
		m.emit(r, convevents.ChildUpserted{Child: childOf(id, info)})
	}
}

func childOf(id string, info obj) convevents.Child {
	t := info.obj("time")
	return convevents.Child{ID: id, ParentID: info.str("parentID"), Title: info.str("title"),
		TimeCreated: t.int("created"), TimeUpdated: t.int("updated"), Agent: info.str("agent")}
}

// patchOf reads the metadata a full v1 session info carries. A missing
// revert marker clears any earlier one.
func patchOf(info obj) convevents.SessionPatch {
	var patch convevents.SessionPatch
	if _, ok := info["title"]; ok {
		title := info.str("title")
		patch.Title = &title
	}
	if s := info.obj("summary"); s != nil {
		patch.SummaryAdditions, patch.SummaryDeletions, patch.SummaryFiles = intPtr(s, "additions"), intPtr(s, "deletions"), intPtr(s, "files")
	}
	if url := info.obj("share").str("url"); url != "" {
		patch.ShareURL = &url
	}
	if t := info.obj("time"); t != nil {
		archived := t.has("archived")
		patch.Archived = &archived
	}
	patch.Revert = convevents.Null[convevents.RevertMarker]()
	if rv := info.obj("revert"); rv.str("messageID") != "" {
		patch.Revert = convevents.Some(convevents.RevertMarker{MessageID: rv.str("messageID"), PartID: rv.str("partID")})
	}
	return patch
}

func intPtr(o obj, key string) *int {
	n, ok := o.num(key)
	if !ok {
		return nil
	}
	v := int(n)
	return &v
}

func (m *Mapper) sessionError(r *Result, p obj) {
	e := p.obj("error")
	if e == nil || !m.own(p.str("sessionID")) {
		return
	}
	data := e.obj("data")
	info := convevents.ErrorInfo{Name: e.str("name"), Message: data.first("message"), StatusCode: int(data.int("statusCode"))}
	if info.Message == "" {
		info.Message = e.first("message", "name")
	}
	m.emit(r, convevents.SessionError{MessageID: p.str("messageID"), Error: info})
}
