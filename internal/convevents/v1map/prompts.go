package v1map

import (
	"encoding/json"
	"strings"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
)

// Prompts of every session pass through with the asking session; the hub
// routes descendants' prompts to the visible ancestor and drops the rest.
func (m *Mapper) asker(p obj) string {
	if sid := p.first("sessionID", "sessionId"); sid != "" {
		return sid
	}
	return m.opts.SessionID
}

func (m *Mapper) permissionAsked(r *Result, p obj) {
	id := p.first("id", "requestID")
	if id == "" {
		return
	}
	req := &convevents.PermissionRequest{Permission: p.str("permission"), Patterns: p.strings("patterns")}
	if req.Permission == "" {
		req.Permission = "Permission required"
	}
	if p.arr("always") != nil {
		req.Always = p.strings("always")
	}
	if meta := p.obj("metadata"); meta != nil {
		_ = json.Unmarshal(p["metadata"], &req.Metadata)
	}
	m.emit(r, convevents.PromptAsked{Prompt: convevents.Prompt{ID: id, Kind: convevents.PromptPermission,
		SessionID: m.asker(p), AskedAt: m.opts.Now(), Permission: req}})
}

func (m *Mapper) permissionReplied(r *Result, p obj) {
	id := p.first("requestID", "requestId", "permissionID", "id")
	if id == "" {
		return
	}
	outcome := p.str("reply")
	switch {
	case p.str("reason") == "cancelled":
		outcome = convevents.ResolvedCancelled
	case outcome != convevents.ResolvedOnce && outcome != convevents.ResolvedAlways && outcome != convevents.ResolvedReject:
		outcome = convevents.ResolvedDismissed
	}
	m.emit(r, convevents.PromptResolved{PromptID: id, Kind: convevents.PromptPermission, Outcome: outcome, By: convevents.ByUser})
}

func (m *Mapper) questionAsked(r *Result, p obj) {
	id := p.first("id", "requestID", "requestId")
	questions := questionItems(p["questions"])
	if id == "" || len(questions) == 0 {
		return
	}
	m.emit(r, convevents.PromptAsked{Prompt: convevents.Prompt{ID: id, Kind: convevents.PromptQuestion,
		SessionID: m.asker(p), AskedAt: m.opts.Now(), Question: &convevents.QuestionRequest{Questions: questions}}})
}

// questionItems accepts an array, a {questions} wrapper or either as a JSON
// string, and keeps items with a string question and an options array.
func questionItems(raw json.RawMessage) []convevents.QuestionItem {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = json.RawMessage(s)
	}
	if wrapped := parseObj(raw); wrapped != nil && wrapped.arr("questions") != nil {
		raw = wrapped["questions"]
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []convevents.QuestionItem
	for _, item := range items {
		q := parseObj(item)
		if _, ok := q["question"]; !ok || q.arr("options") == nil {
			continue
		}
		var s string
		if json.Unmarshal(q["question"], &s) != nil {
			continue
		}
		qi := convevents.QuestionItem{Question: s, Header: q.str("header"), Options: []convevents.QuestionOption{}}
		_ = json.Unmarshal(q["multiple"], &qi.Multiple)
		_ = json.Unmarshal(q["custom"], &qi.Custom)
		for _, opt := range q.arr("options") {
			o := parseObj(opt)
			qi.Options = append(qi.Options, convevents.QuestionOption{Label: o.str("label"), Description: o.str("description")})
		}
		out = append(out, qi)
	}
	return out
}

func (m *Mapper) questionCleared(r *Result, typ string, p obj) {
	id := p.first("requestID", "requestId", "id")
	if id == "" {
		return
	}
	outcome := convevents.ResolvedAnswered
	if typ == "question.rejected" {
		outcome = convevents.ResolvedReject
	}
	m.resolved[id] = true
	m.emit(r, convevents.PromptResolved{PromptID: id, Kind: convevents.PromptQuestion, Outcome: outcome, By: convevents.ByUser})
}

// judge maps the auto-approve progress synthetics of legacy streams.
func (m *Mapper) judge(r *Result, typ string, p obj) {
	id := p.str("permissionId")
	if id == "" {
		return
	}
	ev := convevents.PromptJudge{PromptID: id, Phase: strings.TrimPrefix(typ, "ocman.permission.")}
	switch ev.Phase {
	case "pending":
		ev.Phase, ev.StartsAt = convevents.JudgeScheduled, p.int("judgeStartsAt")
	case convevents.JudgeFlagged:
		ev.Reasoning = p.str("reasoning")
	}
	m.emit(r, ev)
}

// noticeData is the auto-approved part, field for field what history reads
// inject (injectApprovalNotices), so stream and REST agree.
type noticeData struct {
	Type       string          `json:"type"`
	Permission string          `json:"permission"`
	Patterns   []string        `json:"patterns"`
	Reasoning  string          `json:"reasoning"`
	ApprovedBy string          `json:"approvedBy"`
	Reply      string          `json:"reply"`
	Metadata   json.RawMessage `json:"metadata"`
	AskedAt    int64           `json:"askedAt"`
	ApprovedAt int64           `json:"approvedAt"`
}

// approved turns an approval synthetic into the notice records and the
// prompt resolution.
func (m *Mapper) approved(r *Result, p obj) {
	id, permission := p.str("permissionId"), p.str("permission")
	if id == "" || permission == "" {
		return
	}
	data := noticeData{Type: "auto-approved", Permission: permission, Patterns: p.strings("patterns"),
		Reasoning: p.str("reasoning"), ApprovedBy: convevents.ByAI, Reply: p.str("reply"),
		Metadata: json.RawMessage("null"), AskedAt: p.int("askedAt"), ApprovedAt: p.int("approvedAt")}
	if p.str("approvedBy") == convevents.ByUser {
		data.ApprovedBy, data.Reasoning = convevents.ByUser, ""
	}
	if p.obj("metadata") != nil {
		data.Metadata = p["metadata"]
	}
	if data.ApprovedAt == 0 {
		data.ApprovedAt = m.opts.Now()
	}
	outcome := data.Reply
	if outcome != convevents.ResolvedAlways {
		outcome = convevents.ResolvedOnce
	}
	key := "ocman-notice-" + id
	m.emit(r,
		convevents.MessageUpserted{Message: db.Message{ID: key, SessionID: m.opts.SessionID, TimeCreated: data.ApprovedAt,
			Data: json.RawMessage(`{"role":"notice"}`)}},
		convevents.PartUpserted{Final: true, Part: convevents.Part{Part: db.Part{ID: key + "-part", MessageID: key,
			SessionID: m.opts.SessionID, TimeCreated: data.ApprovedAt, Data: mustMarshal(data)}}},
		convevents.PromptResolved{PromptID: id, Kind: convevents.PromptPermission, Outcome: outcome, By: data.ApprovedBy},
	)
}
