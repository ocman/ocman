package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func sessionDescription() Description {
	return Description{
		ID: "org.example.run", Name: "Run", Version: "1", Protocol: Version{Major: 1}, Scope: ScopeOwner, MaxConcurrency: 1,
		Capabilities:    []Capability{SessionCapability},
		RequestedGrants: []string{SessionRunGrant},
		Settings:        []Setting{{Key: SessionProjectSetting, Label: "Project", Type: "string", Required: true}},
	}
}

func TestSessionDeclarationContract(t *testing.T) {
	if err := sessionDescription().Validate(); err != nil {
		t.Fatalf("rejected conforming description: %v", err)
	}
	noGrant := sessionDescription()
	noGrant.RequestedGrants = nil
	noProject := sessionDescription()
	noProject.Settings = nil
	secretProject := sessionDescription()
	secretProject.Settings = []Setting{{Key: SessionProjectSetting, Label: "Project", Type: "string", Required: true, Secret: true}}
	optionalProject := sessionDescription()
	optionalProject.Settings = []Setting{{Key: SessionProjectSetting, Label: "Project", Type: "string"}}
	numberProject := sessionDescription()
	numberProject.Settings = []Setting{{Key: SessionProjectSetting, Label: "Project", Type: "number", Required: true}}
	listProject := sessionDescription()
	listProject.Settings[0].List = true
	// A session driver starts turns with the installing machine's approval
	// state, so it belongs to its owner like a connector does.
	hubScope := sessionDescription()
	hubScope.Scope = ScopeHub
	globalScope := sessionDescription()
	globalScope.Scope = ScopeGlobal
	for name, d := range map[string]Description{
		"missing grant":    noGrant,
		"missing project":  noProject,
		"secret project":   secretProject,
		"optional project": optionalProject,
		"typed project":    numberProject,
		"list project":     listProject,
		"hub scope":        hubScope,
		"global scope":     globalScope,
	} {
		t.Run(name, func(t *testing.T) {
			if d.Validate() == nil {
				t.Fatal("accepted")
			}
		})
	}
	// A plugin without the capability is unaffected by the contract.
	unrelated := sessionDescription()
	unrelated.Capabilities, unrelated.RequestedGrants, unrelated.Settings = []Capability{ActionCapability}, nil, nil
	unrelated.Scope = ScopeHub
	if err := unrelated.Validate(); err != nil {
		t.Fatalf("action-only plugin rejected: %v", err)
	}
}

func TestSessionRunParamsValidation(t *testing.T) {
	for name, p := range map[string]SessionRunParams{
		"empty prompt":   {Prompt: ""},
		"blank prompt":   {Prompt: "   \n"},
		"control prompt": {Prompt: "a\x00b"},
		"oversized":      {Prompt: strings.Repeat("x", SessionMaxPromptBytes+1)},
		"invalid utf8":   {Prompt: string([]byte{0xff, 0xfe})},
		"agent spaces":   {Prompt: "go", Agent: " build"},
		"model control":  {Prompt: "go", Model: "provider/model\n"},
		"long profile":   {Prompt: "go", PermissionProfile: strings.Repeat("p", 129)},
	} {
		t.Run(name, func(t *testing.T) {
			if p.Validate() == nil {
				t.Fatalf("accepted %+v", p)
			}
		})
	}
	valid := SessionRunParams{Prompt: "line one\n\tline two", Agent: "build", Model: "litellm/gpt", PermissionProfile: "read-only", Subpath: "packages/app"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("rejected valid params: %v", err)
	}
	if (SessionCompleted{SessionID: "ses_1", Text: "done"}).Validate() != nil {
		t.Fatal("rejected valid completion")
	}
	for _, c := range []SessionCompleted{{Text: "done"}, {SessionID: "ses 1"}, {SessionID: "ses_1", Text: ""}, {SessionID: "ses_1", Text: "a\x00b"}} {
		if c.Validate() == nil {
			t.Fatalf("accepted completion %+v", c)
		}
	}
}

func TestDecodeSessionRunParams(t *testing.T) {
	for name, data := range map[string]string{
		"unknown field":  `{"prompt":"go","directory":"/etc"}`,
		"trailing json":  `{"prompt":"go"} {}`,
		"duplicate key":  `{"prompt":"go","prompt":"stop"}`,
		"missing prompt": `{}`,
		"not an object":  `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeSessionRunParams(json.RawMessage(data)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	p, err := DecodeSessionRunParams(json.RawMessage(`{"prompt":"go","agent":"build","model":"m","permissionProfile":"ro","subpath":"a/b"}`))
	if err != nil || p.Prompt != "go" || p.Agent != "build" || p.Model != "m" || p.PermissionProfile != "ro" || p.Subpath != "a/b" {
		t.Fatalf("decode: %+v %v", p, err)
	}
}

func TestSessionDirectoryFor(t *testing.T) {
	for _, tc := range []struct {
		project, subpath string
		want             string
		ok               bool
	}{
		{"/repo", "", "/repo", true},
		{"/repo/", "", "/repo", true},
		{"/repo", "packages/app", "/repo/packages/app", true},
		{"/repo", "./sub", "/repo/sub", true},
		{"/repo", ".", "/repo", true},
		{"/repo", "a/../b", "", false},
		{"/repo", "../escape", "", false},
		{"/repo", "a/../../b", "", false},
		{"/repo", "..", "", false},
		{"/repo", "/etc", "", false},
		{"/repo", `\etc`, "", false},
	} {
		got, ok := SessionDirectoryFor(tc.project, tc.subpath)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("%q+%q = %q,%v want %q,%v", tc.project, tc.subpath, got, ok, tc.want, tc.ok)
		}
	}
}

type sessionHarness struct {
	description Description
	grants      []string
	project     string
	started     []string
	startErr    error
	startID     string
	startEmpty  bool
	calls       []Call
	result      Envelope
	callErr     error
}

func (h *sessionHarness) broker() *SessionBroker {
	return NewSessionBroker(
		func(_ context.Context, _ string, use func(Description, []string, Config) error) error {
			return use(h.description, h.grants, Config{Project: h.project})
		},
		func(_ context.Context, _ string, config Config, params SessionRunParams, directory, operationID string) (string, error) {
			h.started = append(h.started, config.Project+"|"+directory+"|"+params.Prompt+"|"+operationID)
			if h.startErr != nil {
				return "", h.startErr
			}
			if h.startEmpty {
				return "", nil
			}
			if h.startID != "" {
				return h.startID, nil
			}
			return "ses-generated", nil
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

func sessionRunEvent(t *testing.T, params SessionRunParams) Call {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return Call{ID: 1, OperationID: "caller-operation", Capability: SessionCapability.Name, Version: SessionCapability.Version, Method: SessionRunMethod, Params: data, DeadlineUnixMS: time.Now().Add(time.Minute).UnixMilli()}
}

func TestSessionBrokerRun(t *testing.T) {
	params := SessionRunParams{Prompt: "build it", Subpath: "packages/app"}
	t.Run("granted", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo", startID: "ses-conformance-1"}
		result, err := h.broker().Run(t.Context(), "org.example.run", sessionRunEvent(t, params))
		if err != nil || result.SessionID != "ses-conformance-1" {
			t.Fatalf("run: %+v %v", result, err)
		}
		// The directory is the approved project joined with a contained
		// subpath, and the operation id reaches the host for the title.
		if len(h.started) != 1 || h.started[0] != "/repo|/repo/packages/app|build it|caller-operation" {
			t.Fatalf("started %v", h.started)
		}
	})
	t.Run("missing grant", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), project: "/repo"}
		_, err := h.broker().Run(t.Context(), "org.example.run", sessionRunEvent(t, params))
		assertWire(t, err, ErrorPermissionDenied)
		if len(h.started) != 0 {
			t.Fatalf("started despite denial: %v", h.started)
		}
	})
	t.Run("capability not declared", func(t *testing.T) {
		d := sessionDescription()
		d.Capabilities = []Capability{ActionCapability}
		h := &sessionHarness{description: d, grants: []string{SessionRunGrant}, project: "/repo"}
		_, err := h.broker().Run(t.Context(), "org.example.run", sessionRunEvent(t, params))
		assertWire(t, err, ErrorNotFound)
	})
	t.Run("no configured project", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}}
		_, err := h.broker().Run(t.Context(), "org.example.run", sessionRunEvent(t, params))
		assertWire(t, err, ErrorPermissionDenied)
	})
	t.Run("subpath traversal", func(t *testing.T) {
		for _, bad := range []string{"../escape", "/etc", "..", "a/../../b"} {
			h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo"}
			traversal := params
			traversal.Subpath = bad
			if _, err := h.broker().Run(t.Context(), "org.example.run", sessionRunEvent(t, traversal)); err == nil {
				t.Fatalf("accepted subpath %q", bad)
			}
			if len(h.started) != 0 {
				t.Fatalf("started despite traversal %q: %v", bad, h.started)
			}
		}
	})
	t.Run("invalid payload", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo"}
		event := sessionRunEvent(t, params)
		event.Params = json.RawMessage(`{"prompt":"go","directory":"/etc"}`)
		if _, err := h.broker().Run(t.Context(), "org.example.run", event); err == nil {
			t.Fatal("accepted unknown parameter")
		}
	})
	t.Run("wrong event", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo"}
		event := sessionRunEvent(t, params)
		event.Method = "other"
		if _, err := h.broker().Run(t.Context(), "org.example.run", event); err == nil {
			t.Fatal("accepted wrong event name")
		}
	})
	t.Run("host returned no session", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo", startEmpty: true}
		// A host hook that succeeds but names no session is a broken host.
		result, err := h.broker().Run(t.Context(), "org.example.run", sessionRunEvent(t, params))
		if !errors.Is(err, ErrInvalidMessage) || result.SessionID != "" {
			t.Fatalf("accepted: %+v %v", result, err)
		}
	})
}

func TestSessionBrokerCompleted(t *testing.T) {
	completion := SessionCompleted{SessionID: "ses_1", Text: "done"}
	success := Envelope{Type: TypeResult, Result: &Result{ID: 1, Value: json.RawMessage(`{}`)}}
	for _, value := range []string{`null`, `[]`, `true`, `{"extra":1}`} {
		t.Run("invalid ack "+value, func(t *testing.T) {
			h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo",
				result: Envelope{Type: TypeResult, Result: &Result{ID: 1, Value: json.RawMessage(value)}}}
			if err := h.broker().Completed(t.Context(), "org.example.run", "sess-out:9", completion); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("accepted ack %s: %v", value, err)
			}
		})
	}
	t.Run("granted", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo", result: success}
		if err := h.broker().Completed(t.Context(), "org.example.run", "sess-out:9", completion); err != nil {
			t.Fatal(err)
		}
		if len(h.calls) != 1 || h.calls[0].Method != SessionCompletedMethod || h.calls[0].Capability != SessionCapability.Name {
			t.Fatalf("calls %+v", h.calls)
		}
		if h.calls[0].DeadlineUnixMS <= time.Now().UnixMilli() {
			t.Fatal("completion call has no future deadline")
		}
		var sent SessionCompleted
		if json.Unmarshal(h.calls[0].Params, &sent) != nil || sent != completion {
			t.Fatalf("params %s", h.calls[0].Params)
		}
	})
	t.Run("missing grant", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), project: "/repo", result: success}
		assertWire(t, h.broker().Completed(t.Context(), "org.example.run", "sess-out:9", completion), ErrorPermissionDenied)
		if len(h.calls) != 0 {
			t.Fatalf("called despite denial: %+v", h.calls)
		}
	})
	t.Run("invalid completion", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo", result: success}
		assertWire(t, h.broker().Completed(t.Context(), "org.example.run", "sess-out:9", SessionCompleted{SessionID: "ses_1"}), ErrorInvalidArgument)
		assertWire(t, h.broker().Completed(t.Context(), "org.example.run", "", completion), ErrorInvalidArgument)
	})
	t.Run("plugin error", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo",
			result: Envelope{Type: TypeResult, Result: &Result{ID: 1, Error: &WireError{Category: ErrorUnavailable}}}}
		assertWire(t, h.broker().Completed(t.Context(), "org.example.run", "sess-out:9", completion), ErrorUnavailable)
	})
	t.Run("process unavailable", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo", callErr: ErrUnavailable}
		if err := h.broker().Completed(t.Context(), "org.example.run", "sess-out:9", completion); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("non result frame", func(t *testing.T) {
		h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, project: "/repo",
			result: Envelope{Type: TypeChunk, Chunk: &Chunk{ID: 1, Sequence: 1, Data: json.RawMessage(`{}`)}}}
		if err := h.broker().Completed(t.Context(), "org.example.run", "sess-out:9", completion); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("err %v", err)
		}
	})
}
