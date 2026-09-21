package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func conversationDescription() Description {
	return Description{
		ID: "org.example.chat", Name: "Chat", Version: "1", Protocol: Version{Major: 1}, Scope: ScopeOwner, MaxConcurrency: 1,
		Capabilities:    []Capability{ConversationCapability},
		RequestedGrants: []string{ConversationSessionGrant},
		Settings:        []Setting{{Key: ConversationProjectSetting, Label: "Project", Type: "string", Required: true}},
	}
}

func TestConversationMessageValidation(t *testing.T) {
	for name, m := range map[string]ConversationMessage{
		"empty thread":    {AccountID: "T1", ThreadID: "", EventID: "Ev1", Text: "hi"},
		"thread charset":  {AccountID: "T1", ThreadID: "chan nel:1", EventID: "Ev1", Text: "hi"},
		"thread leading":  {AccountID: "T1", ThreadID: "-c:1", EventID: "Ev1", Text: "hi"},
		"empty text":      {AccountID: "T1", ThreadID: "C1:1.0", EventID: "Ev1", Text: ""},
		"blank text":      {AccountID: "T1", ThreadID: "C1:1.0", EventID: "Ev1", Text: "   \n"},
		"control text":    {AccountID: "T1", ThreadID: "C1:1.0", EventID: "Ev1", Text: "a\x00b"},
		"oversized text":  {AccountID: "T1", ThreadID: "C1:1.0", EventID: "Ev1", Text: strings.Repeat("x", ConversationMaxTextBytes+1)},
		"invalid utf8":    {AccountID: "T1", ThreadID: "C1:1.0", EventID: "Ev1", Text: string([]byte{0xff, 0xfe})},
		"long project":    {AccountID: "T1", ThreadID: "C1:1.0", EventID: "Ev1", Text: "hi", Project: strings.Repeat("/p", 800)},
		"long thread":     {AccountID: "T1", ThreadID: "C" + strings.Repeat("1", 128), EventID: "Ev1", Text: "hi"},
		"reply not text?": {AccountID: "T1", ThreadID: "C1:1.0", EventID: "Ev1"},
		// Without an account there is no workspace to attribute the thread to,
		// and without a stable event id a redelivery cannot be deduplicated.
		"missing account": {ThreadID: "C1:1.0", EventID: "Ev1", Text: "hi"},
		"account charset": {AccountID: "team one", ThreadID: "C1:1.0", EventID: "Ev1", Text: "hi"},
		"missing event":   {AccountID: "T1", ThreadID: "C1:1.0", Text: "hi"},
		"event charset":   {AccountID: "T1", ThreadID: "C1:1.0", EventID: "ev 1", Text: "hi"},
	} {
		t.Run(name, func(t *testing.T) {
			if m.Validate() == nil {
				t.Fatalf("accepted %+v", m)
			}
		})
	}
	valid := ConversationMessage{
		AccountID: "T0001", ThreadID: "C123:1515449522.000016", EventID: "Ev0001",
		Text: "line one\n\tline two", Project: "/repo",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("rejected valid message: %v", err)
	}
	if (ConversationReply{AccountID: valid.AccountID, ThreadID: valid.ThreadID, Text: valid.Text}).Validate() != nil {
		t.Fatal("rejected valid reply")
	}
	if (ConversationReply{ThreadID: valid.ThreadID, Text: valid.Text}).Validate() == nil {
		t.Fatal("accepted a reply with no account to post into")
	}
}

func TestDecodeConversationMessage(t *testing.T) {
	for name, data := range map[string]string{
		"unknown field": `{"accountId":"T1","threadId":"C1:1.0","eventId":"Ev1","text":"hi","channel":"C1"}`,
		"trailing json": `{"accountId":"T1","threadId":"C1:1.0","eventId":"Ev1","text":"hi"} {}`,
		"duplicate key": `{"accountId":"T1","threadId":"C1:1.0","threadId":"C2:1.0","eventId":"Ev1","text":"hi"}`,
		"invalid":       `{"accountId":"T1","threadId":"","eventId":"Ev1","text":"hi"}`,
		"no event id":   `{"accountId":"T1","threadId":"C1:1.0","text":"hi"}`,
		"not an object": `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeConversationMessage(json.RawMessage(data)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	m, err := DecodeConversationMessage(json.RawMessage(`{"accountId":"T1","threadId":"C1:1.0","eventId":"Ev1","text":"hi","project":"/repo"}`))
	if err != nil || m.AccountID != "T1" || m.ThreadID != "C1:1.0" || m.EventID != "Ev1" || m.Text != "hi" || m.Project != "/repo" {
		t.Fatalf("decode: %+v %v", m, err)
	}
}

func TestConversationDeclarationContract(t *testing.T) {
	base := conversationDescription()
	if err := base.Validate(); err != nil {
		t.Fatalf("rejected conforming description: %v", err)
	}
	noGrant := conversationDescription()
	noGrant.RequestedGrants = nil
	noProject := conversationDescription()
	noProject.Settings = nil
	secretProject := conversationDescription()
	secretProject.Settings = []Setting{{Key: ConversationProjectSetting, Label: "Project", Type: "string", Required: true, Secret: true}}
	optionalProject := conversationDescription()
	optionalProject.Settings = []Setting{{Key: ConversationProjectSetting, Label: "Project", Type: "string"}}
	for name, d := range map[string]Description{
		"missing grant":    noGrant,
		"missing project":  noProject,
		"secret project":   secretProject,
		"optional project": optionalProject,
	} {
		t.Run(name, func(t *testing.T) {
			if d.Validate() == nil {
				t.Fatal("accepted")
			}
		})
	}
	// A plugin without the capability is unaffected by the contract.
	unrelated := conversationDescription()
	unrelated.Capabilities, unrelated.RequestedGrants, unrelated.Settings = []Capability{ActionCapability}, nil, nil
	if err := unrelated.Validate(); err != nil {
		t.Fatalf("action-only plugin rejected: %v", err)
	}
}

func TestConversationProjectAllowed(t *testing.T) {
	for _, tc := range []struct {
		configured, claimed string
		want                bool
	}{
		{"/repo", "", true},
		{"/repo", "/repo", true},
		{"/repo", "/repo/", true},
		{"/repo", "/other", false},
		{"/repo", "/repo/../other", false},
	} {
		if got := ConversationProjectAllowed(tc.configured, tc.claimed); got != tc.want {
			t.Fatalf("%q vs %q: %v", tc.configured, tc.claimed, got)
		}
	}
}

type conversationHarness struct {
	description Description
	grants      []string
	project     string
	started     []string
	calls       []Call
	result      Envelope
	callErr     error
}

func (h *conversationHarness) broker() *ConversationBroker {
	return NewConversationBroker(
		func(_ context.Context, _ string, use func(Description, []string, string) error) error {
			return use(h.description, h.grants, h.project)
		},
		func(_ context.Context, _, dir string, m ConversationMessage) error {
			h.started = append(h.started, m.AccountID+"|"+m.ThreadID+"|"+m.EventID+"|"+dir+"|"+m.Text)
			return nil
		},
		func(_ context.Context, _ string, call Call) (<-chan Reply, error) {
			h.calls = append(h.calls, call)
			if h.callErr != nil {
				return nil, h.callErr
			}
			replies := make(chan Reply, 1)
			replies <- Reply{Message: h.result}
			close(replies)
			return replies, nil
		})
}

func conversationEvent(t *testing.T, m ConversationMessage) Event {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return Event{Capability: ConversationCapability.Name, Name: ConversationMessageEvent, Data: data}
}

func TestConversationBrokerDeliver(t *testing.T) {
	message := ConversationMessage{AccountID: "T1", ThreadID: "C1:1.0", EventID: "Ev1", Text: "build it"}
	t.Run("granted", func(t *testing.T) {
		h := &conversationHarness{description: conversationDescription(), grants: []string{ConversationSessionGrant}, project: "/repo"}
		if err := h.broker().Deliver(t.Context(), "org.example.chat", conversationEvent(t, message)); err != nil {
			t.Fatal(err)
		}
		// The whole identity crosses the seam: the host needs the account and
		// event id to key the mapping and deduplicate the delivery.
		if len(h.started) != 1 || h.started[0] != "T1|C1:1.0|Ev1|/repo|build it" {
			t.Fatalf("started %v", h.started)
		}
	})
	t.Run("missing grant", func(t *testing.T) {
		h := &conversationHarness{description: conversationDescription(), project: "/repo"}
		err := h.broker().Deliver(t.Context(), "org.example.chat", conversationEvent(t, message))
		assertWire(t, err, ErrorPermissionDenied)
		if len(h.started) != 0 {
			t.Fatalf("started despite denial: %v", h.started)
		}
	})
	t.Run("capability not declared", func(t *testing.T) {
		d := conversationDescription()
		d.Capabilities = []Capability{ActionCapability}
		h := &conversationHarness{description: d, grants: []string{ConversationSessionGrant}, project: "/repo"}
		assertWire(t, h.broker().Deliver(t.Context(), "org.example.chat", conversationEvent(t, message)), ErrorNotFound)
	})
	t.Run("unapproved project", func(t *testing.T) {
		claim := message
		claim.Project = "/somewhere/else"
		h := &conversationHarness{description: conversationDescription(), grants: []string{ConversationSessionGrant}, project: "/repo"}
		assertWire(t, h.broker().Deliver(t.Context(), "org.example.chat", conversationEvent(t, claim)), ErrorPermissionDenied)
		if len(h.started) != 0 {
			t.Fatalf("started despite denial: %v", h.started)
		}
	})
	t.Run("no configured project", func(t *testing.T) {
		h := &conversationHarness{description: conversationDescription(), grants: []string{ConversationSessionGrant}}
		assertWire(t, h.broker().Deliver(t.Context(), "org.example.chat", conversationEvent(t, message)), ErrorPermissionDenied)
	})
	t.Run("wrong event", func(t *testing.T) {
		h := &conversationHarness{description: conversationDescription(), grants: []string{ConversationSessionGrant}, project: "/repo"}
		event := conversationEvent(t, message)
		event.Name = "other"
		assertWire(t, h.broker().Deliver(t.Context(), "org.example.chat", event), ErrorNotFound)
	})
	t.Run("invalid payload", func(t *testing.T) {
		h := &conversationHarness{description: conversationDescription(), grants: []string{ConversationSessionGrant}, project: "/repo"}
		event := conversationEvent(t, message)
		event.Data = json.RawMessage(`{"accountId":"T1","threadId":"C1:1.0","eventId":"Ev1","text":"hi","surprise":1}`)
		assertWire(t, h.broker().Deliver(t.Context(), "org.example.chat", event), ErrorInvalidArgument)
	})
}

func TestConversationBrokerReply(t *testing.T) {
	reply := ConversationReply{AccountID: "T1", ThreadID: "C1:1.0", Text: "done"}
	success := Envelope{Type: TypeResult, Result: &Result{ID: 1, Value: json.RawMessage(`{}`)}}
	t.Run("granted", func(t *testing.T) {
		h := &conversationHarness{description: conversationDescription(), grants: []string{ConversationSessionGrant}, project: "/repo", result: success}
		if err := h.broker().Reply(t.Context(), "org.example.chat", "ses:msg", reply); err != nil {
			t.Fatal(err)
		}
		if len(h.calls) != 1 || h.calls[0].Method != ConversationReplyMethod || h.calls[0].Capability != ConversationCapability.Name {
			t.Fatalf("calls %+v", h.calls)
		}
		if h.calls[0].DeadlineUnixMS <= time.Now().UnixMilli() {
			t.Fatal("reply call has no future deadline")
		}
		var sent ConversationReply
		if json.Unmarshal(h.calls[0].Params, &sent) != nil || sent != reply {
			t.Fatalf("params %s", h.calls[0].Params)
		}
	})
	t.Run("missing grant", func(t *testing.T) {
		h := &conversationHarness{description: conversationDescription(), project: "/repo", result: success}
		assertWire(t, h.broker().Reply(t.Context(), "org.example.chat", "ses:msg", reply), ErrorPermissionDenied)
		if len(h.calls) != 0 {
			t.Fatalf("called despite denial: %+v", h.calls)
		}
	})
	t.Run("invalid reply", func(t *testing.T) {
		h := &conversationHarness{description: conversationDescription(), grants: []string{ConversationSessionGrant}, project: "/repo", result: success}
		assertWire(t, h.broker().Reply(t.Context(), "org.example.chat", "ses:msg", ConversationReply{AccountID: "T1", ThreadID: "C1:1.0"}), ErrorInvalidArgument)
		assertWire(t, h.broker().Reply(t.Context(), "org.example.chat", "", reply), ErrorInvalidArgument)
	})
	t.Run("plugin error", func(t *testing.T) {
		h := &conversationHarness{description: conversationDescription(), grants: []string{ConversationSessionGrant}, project: "/repo",
			result: Envelope{Type: TypeResult, Result: &Result{ID: 1, Error: &WireError{Category: ErrorUnavailable}}}}
		assertWire(t, h.broker().Reply(t.Context(), "org.example.chat", "ses:msg", reply), ErrorUnavailable)
	})
	t.Run("process unavailable", func(t *testing.T) {
		h := &conversationHarness{description: conversationDescription(), grants: []string{ConversationSessionGrant}, project: "/repo", callErr: ErrUnavailable}
		if err := h.broker().Reply(t.Context(), "org.example.chat", "ses:msg", reply); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("non result frame", func(t *testing.T) {
		h := &conversationHarness{description: conversationDescription(), grants: []string{ConversationSessionGrant}, project: "/repo",
			result: Envelope{Type: TypeChunk, Chunk: &Chunk{ID: 1, Sequence: 1, Data: json.RawMessage(`{}`)}}}
		if err := h.broker().Reply(t.Context(), "org.example.chat", "ses:msg", reply); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("err %v", err)
		}
	})
}

func assertWire(t *testing.T, err error, want ErrorCategory) {
	t.Helper()
	var wire *WireError
	if !errors.As(err, &wire) || wire.Category != want {
		t.Fatalf("err %v, want %s", err, want)
	}
}
