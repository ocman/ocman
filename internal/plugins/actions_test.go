package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestActionBroker(t *testing.T) {
	d := Description{Actions: []ActionDescriptor{{ID: "test", Label: "Test", Placement: "global", Surfaces: []string{"command-palette"}, RequiredGrants: []string{"context.owner"}, Confirmation: "Run test?"}}}
	var mu sync.Mutex
	grants := []string{"context.owner", "context.project"}
	calls := 0
	b := NewActionBroker(func(ctx context.Context, id string, use func(Description, []string) error) error {
		mu.Lock()
		defer mu.Unlock()
		return use(d, grants)
	}, func(ctx context.Context, id string, c Call) (<-chan Reply, error) {
		calls++
		var invocation ActionInvocation
		if err := json.Unmarshal(c.Params, &invocation); err != nil {
			t.Fatal(err)
		}
		if invocation.Context.OwnerID != "local" || invocation.Context.ProjectID != "" || invocation.Context.SessionID != "" || invocation.Context.Route != "" || len(invocation.Context.Selection) != 0 {
			t.Fatalf("context leaked: %+v", invocation.Context)
		}
		if c.Capability != "action" || c.Method != "invoke" || c.OperationID != "op-1" || c.DeadlineUnixMS <= time.Now().UnixMilli() {
			t.Fatalf("call: %+v", c)
		}
		ch := make(chan Reply, 1)
		ch <- Reply{Message: Envelope{Type: TypeResult, Result: &Result{Value: json.RawMessage(`{"results":[{"kind":"notice","text":"Done"}]}`)}}}
		close(ch)
		return ch, nil
	})
	r := ActionRequest{PluginID: "org.example.test", ActionID: "test", OperationID: "op-1", Placement: "global", Surface: "command-palette", Context: ActionContext{OwnerID: "local", ProjectID: "project-1", SessionID: "ses-1", Route: "session", Selection: []ActionSelection{{Kind: "session", ID: "ses-1"}}}}
	first := b.Invoke(context.Background(), r)
	if first.Confirmation == nil || calls != 0 {
		t.Fatalf("confirmation bypass: %+v, %d", first, calls)
	}
	r.ConfirmationToken = first.Confirmation.Token
	result := b.Invoke(context.Background(), r)
	if result.Error != nil || len(result.Results) != 1 || calls != 1 {
		t.Fatalf("invoke: %+v, %d", result, calls)
	}
	if got := b.Invoke(context.Background(), r); got.Error != nil || calls != 1 {
		t.Fatalf("duplicate: %+v, %d", got, calls)
	}
	r.Context.ProjectID = "different"
	if got := b.Invoke(context.Background(), r); got.Error == nil || got.Error.Category != ErrorConflict {
		t.Fatalf("operation reuse: %+v", got)
	}
	r.Context.ProjectID = "project-1"
	mu.Lock()
	grants = nil
	mu.Unlock()
	if got := b.Invoke(context.Background(), r); got.Error == nil || got.Error.Category != ErrorPermissionDenied {
		t.Fatalf("revoked cached result: %+v", got)
	}
}

func TestActionPluginProjectPathGrant(t *testing.T) {
	d, r := actionFixture()
	d.RequestedGrants = []string{"context.project", "context.project.path"}
	d.Actions[0].Placement = "project"
	d.Actions[0].RequiredGrants = []string{"context.project", "context.project.path"}
	r.Placement, r.Context.ProjectID = "project", "project-1"
	var mu sync.Mutex
	grants := append([]string(nil), d.RequestedGrants...)
	calls := 0
	var sent ActionContext
	b := NewActionBroker(func(ctx context.Context, id string, use func(Description, []string) error) error {
		mu.Lock()
		defer mu.Unlock()
		return use(d, grants)
	}, func(_ context.Context, _ string, c Call) (<-chan Reply, error) {
		calls++
		var invocation ActionInvocation
		if err := json.Unmarshal(c.Params, &invocation); err != nil {
			t.Fatal(err)
		}
		sent = invocation.Context
		ch := make(chan Reply, 1)
		ch <- Reply{Message: Envelope{Type: TypeResult, Result: &Result{Value: json.RawMessage(`{"results":[{"kind":"notice","text":"ok"}]}`)}}}
		close(ch)
		return ch, nil
	})
	b.ResolveDirectory = func(_ context.Context, projectID string) string {
		if projectID == "project-1" {
			return "/home/dev/project"
		}
		return ""
	}
	if got := b.Invoke(context.Background(), r); got.Error != nil || calls != 1 || sent.ProjectID != "project-1" || sent.Directory != "/home/dev/project" {
		t.Fatalf("granted path: %+v calls=%d sent=%+v", got, calls, sent)
	}
	// The operation record keeps only the opaque ID; host paths never enter the
	// replay hash or persisted artifact metadata.
	if op := b.operations[r.PluginID+"/op"]; op.request.Context.Directory != "" {
		t.Fatalf("persisted request carries path: %+v", op.request.Context)
	}
	r.OperationID = "op-2"
	mu.Lock()
	grants = []string{"context.project"}
	mu.Unlock()
	if got := b.Invoke(context.Background(), r); got.Error == nil || got.Error.Category != ErrorPermissionDenied || calls != 1 || sent.Directory != "/home/dev/project" {
		t.Fatalf("unapproved path grant: %+v calls=%d", got, calls)
	}
	mu.Lock()
	grants = append([]string(nil), d.RequestedGrants...)
	mu.Unlock()
	r.OperationID, r.Context.ProjectID = "op-3", "unknown-1"
	if got := b.Invoke(context.Background(), r); got.Error != nil || calls != 2 || sent.ProjectID != "unknown-1" || sent.Directory != "" {
		t.Fatalf("unknown project id: %+v calls=%d sent=%+v", got, calls, sent)
	}
	mu.Lock()
	grants = nil
	mu.Unlock()
	r.OperationID, r.Context.ProjectID = "op", "project-1"
	if got := b.Invoke(context.Background(), r); got.Error == nil || got.Error.Category != ErrorPermissionDenied || calls != 2 || sent.Directory != "" {
		t.Fatalf("revoked after checksum change: %+v calls=%d sent=%+v", got, calls, sent)
	}
}

func actionFixture() (Description, ActionRequest) {
	d := *hello(ModeDescribe).Hello.Description
	d.Actions = []ActionDescriptor{{ID: "test", Label: "Test", Placement: "global", Surfaces: []string{"command-palette"}}}
	r := ActionRequest{PluginID: d.ID, ActionID: "test", OperationID: "op", Placement: "global", Surface: "command-palette", Context: ActionContext{OwnerID: "local"}}
	return d, r
}

func TestActionDescriptorsAndContext(t *testing.T) {
	d, r := actionFixture()
	if d.Validate() != nil || r.Validate() != nil {
		t.Fatal("valid action rejected")
	}
	for name, mutate := range map[string]func(*Description){
		"capability":       func(d *Description) { d.Capabilities = nil },
		"major":            func(d *Description) { d.Capabilities[0].Version.Major = 2 },
		"limit":            func(d *Description) { d.Actions = make([]ActionDescriptor, 129) },
		"duplicate":        func(d *Description) { d.Actions = append(d.Actions, d.Actions[0]) },
		"id":               func(d *Description) { d.Actions[0].ID = "../bad" },
		"label":            func(d *Description) { d.Actions[0].Label = "<script>" },
		"placement":        func(d *Description) { d.Actions[0].Placement = "any" },
		"surface":          func(d *Description) { d.Actions[0].Surfaces = []string{"iframe"} },
		"confirmation":     func(d *Description) { d.Actions[0].Confirmation = "<html>" },
		"undeclared grant": func(d *Description) { d.Actions[0].RequiredGrants = []string{"context.owner"} },
		"undeclared path grant": func(d *Description) {
			d.RequestedGrants = []string{"context.project"}
			d.Actions[0].RequiredGrants = []string{"context.project.path"}
		},
		"unknown grant": func(d *Description) {
			d.RequestedGrants = []string{"filesystem"}
			d.Actions[0].RequiredGrants = d.RequestedGrants
		},
		"duplicate grant": func(d *Description) {
			d.RequestedGrants = []string{"context.owner"}
			d.Actions[0].RequiredGrants = []string{"context.owner", "context.owner"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			d, _ := actionFixture()
			mutate(&d)
			if d.Validate() == nil {
				t.Fatal("accepted invalid descriptor")
			}
		})
	}
	for name, mutate := range map[string]func(*ActionRequest){
		"owner":              func(r *ActionRequest) { r.OwnerID = "../other" },
		"operation":          func(r *ActionRequest) { r.OperationID = "" },
		"project required":   func(r *ActionRequest) { r.Placement = "project" },
		"session required":   func(r *ActionRequest) { r.Placement = "session" },
		"path":               func(r *ActionRequest) { r.Context.ProjectID = "/home/user/project" },
		"injected host path": func(r *ActionRequest) { r.Context.Directory = "/home/user/project" },
		"route":              func(r *ActionRequest) { r.Context.Route = "/session/secret?token=x" },
		"selection text":     func(r *ActionRequest) { r.Context.Selection = []ActionSelection{{Kind: "text", ID: "secret"}} },
		"selection path":     func(r *ActionRequest) { r.Context.Selection = []ActionSelection{{Kind: "project", ID: "/tmp/project"}} },
		"selection limit":    func(r *ActionRequest) { r.Context.Selection = make([]ActionSelection, 101) },
	} {
		t.Run(name, func(t *testing.T) {
			_, r := actionFixture()
			mutate(&r)
			if r.Validate() == nil {
				t.Fatal("accepted invalid context")
			}
		})
	}
	c := ActionContext{OwnerID: "local", ProjectID: "project", SessionID: "session", Route: "session", Selection: []ActionSelection{{Kind: "project", ID: "project"}}, Directory: "/home/dev/project"}
	grants := []string{"context.owner", "context.project", "context.project.path", "context.session", "context.route", "context.selection"}
	if got := (ActionDescriptor{Placement: "project", RequiredGrants: grants}).Minimize(c); !reflect.DeepEqual(got, c) {
		t.Fatalf("granted fields: %+v", got)
	}
	if got := (ActionDescriptor{}).Minimize(c); !reflect.DeepEqual(got, ActionContext{}) {
		t.Fatalf("ungranted fields: %+v", got)
	}
	if got := (ActionDescriptor{Placement: "global", RequiredGrants: grants}).Minimize(c); got.Directory != "" || got.ProjectID != "project" {
		t.Fatalf("path outside project placement: %+v", got)
	}
}

func TestActionFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reply   Reply
		closed  bool
		callErr error
		want    ErrorCategory
	}{
		{name: "unavailable", callErr: ErrUnavailable, want: ErrorUnavailable},
		{name: "busy", callErr: ErrBusy, want: ErrorUnavailable},
		{name: "cancelled", callErr: context.Canceled, want: ErrorCancelled},
		{name: "deadline", callErr: context.DeadlineExceeded, want: ErrorDeadlineExceeded},
		{name: "sensitive error", callErr: errors.New("/secret/password"), want: ErrorInternal},
		{name: "closed", closed: true, want: ErrorUnavailable},
		{name: "transport", reply: Reply{Err: ErrUnavailable}, want: ErrorUnavailable},
		{name: "stream", reply: Reply{Message: chunk(1, 1)}, want: ErrorInternal},
		{name: "plugin", reply: Reply{Message: Envelope{Type: TypeResult, Result: &Result{Error: &WireError{Category: ErrorPermissionDenied}}}}, want: ErrorPermissionDenied},
		{name: "invalid result", reply: Reply{Message: result(1)}, want: ErrorInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, r := actionFixture()
			calls := 0
			b := NewActionBroker(func(ctx context.Context, id string, use func(Description, []string) error) error { return use(d, nil) }, func(context.Context, string, Call) (<-chan Reply, error) {
				calls++
				ch := make(chan Reply, 1)
				if !tc.closed {
					ch <- tc.reply
				}
				close(ch)
				return ch, tc.callErr
			})
			for range 2 {
				got := b.Invoke(t.Context(), r)
				if got.Error == nil || got.Error.Category != tc.want {
					t.Fatalf("response: %+v", got)
				}
			}
			if calls != 1 {
				t.Fatalf("replayed failed operation: %d", calls)
			}
		})
	}
}

func TestActionConcurrentDedupTimeoutAndRevocation(t *testing.T) {
	d, r := actionFixture()
	d.Actions[0].RequiredGrants = []string{"context.owner"}
	var revoked atomic.Bool
	var calls atomic.Int32
	started := make(chan struct{}, 10)
	replies := make(chan Reply, 1)
	b := NewActionBroker(func(ctx context.Context, id string, use func(Description, []string) error) error {
		var grants []string
		if !revoked.Load() {
			grants = []string{"context.owner"}
		}
		return use(d, grants)
	}, func(context.Context, string, Call) (<-chan Reply, error) {
		calls.Add(1)
		started <- struct{}{}
		return replies, nil
	})
	const n = 12
	results := make(chan ActionResponse, n)
	for range n {
		go func() { results <- b.Invoke(t.Context(), r) }()
	}
	<-started
	replies <- Reply{Message: Envelope{Type: TypeResult, Result: &Result{Value: json.RawMessage(`{"results":[{"kind":"notice","text":"ok"}]}`)}}}
	for range n {
		if got := <-results; got.Error != nil {
			t.Fatalf("duplicate: %+v", got)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate calls: %d", calls.Load())
	}

	r.OperationID = "revoke-in-flight"
	go func() { results <- b.Invoke(t.Context(), r) }()
	<-started
	revoked.Store(true)
	replies <- Reply{Message: Envelope{Type: TypeResult, Result: &Result{Value: json.RawMessage(`{"results":[{"kind":"notice","text":"secret"}]}`)}}}
	if got := <-results; got.Error == nil || got.Error.Category != ErrorPermissionDenied || len(got.Results) != 0 {
		t.Fatalf("revoked result: %+v", got)
	}
	revoked.Store(false)
	r.OperationID = "timeout"
	b.timeout = time.Millisecond
	if got := b.Invoke(t.Context(), r); got.Error == nil || got.Error.Category != ErrorDeadlineExceeded {
		t.Fatalf("deadline: %+v", got)
	}
	if got := b.Invoke(t.Context(), r); got.Error == nil || got.Error.Category != ErrorDeadlineExceeded || calls.Load() != 3 {
		t.Fatalf("replayed timeout: %+v calls=%d", got, calls.Load())
	}
}

func TestActionConfirmationBindingAndArtifact(t *testing.T) {
	d, r := actionFixture()
	d.Actions[0].Confirmation = "Proceed?"
	calls := 0
	b := NewActionBroker(func(ctx context.Context, id string, use func(Description, []string) error) error { return use(d, nil) }, func(context.Context, string, Call) (<-chan Reply, error) {
		calls++
		ch := make(chan Reply, 1)
		ch <- Reply{Message: Envelope{Type: TypeResult, Result: &Result{Value: json.RawMessage(`{"results":[{"kind":"artifact","label":"report.txt","data":"aGk="}]}`)}}}
		return ch, nil
	})
	first := b.Invoke(t.Context(), r)
	r.OperationID = "other"
	r.ConfirmationToken = first.Confirmation.Token
	other := b.Invoke(t.Context(), r)
	if other.Confirmation == nil || other.Confirmation.Token == r.ConfirmationToken || calls != 0 {
		t.Fatal("token not bound to operation")
	}
	r.OperationID = "op"
	got := b.Invoke(t.Context(), r)
	if got.Error != nil || len(got.Results) != 1 || len(got.Results[0].Data) != 0 || got.Results[0].Handle == "" {
		t.Fatalf("artifact normalization: %+v", got)
	}
	handle := got.Results[0].Handle
	name, data, err := b.Artifact(t.Context(), handle)
	if err != nil || name != "report.txt" || string(data) != "hi" {
		t.Fatalf("artifact: %q %q %v", name, data, err)
	}
	data[0] = 'x'
	_, data, _ = b.Artifact(t.Context(), handle)
	if string(data) != "hi" {
		t.Fatal("mutable artifact storage")
	}
	d.Actions[0].Label = "Changed"
	if _, _, err := b.Artifact(t.Context(), handle); err == nil {
		t.Fatal("changed declaration still authorized artifact")
	}
	if _, _, err := b.Artifact(t.Context(), "unknown"); err == nil {
		t.Fatal("unknown artifact")
	}
	d.Actions[0].Label = "Test"
	b.operations[r.PluginID+"/other"].confirmation.ExpiresAt = 1
	r.OperationID = "other"
	if got := b.Invoke(t.Context(), r); got.Error == nil || got.Error.Category != ErrorConflict {
		t.Fatalf("expired: %+v", got)
	}
}

func TestActionArtifactRestartRecovery(t *testing.T) {
	d, r := actionFixture()
	dir := t.TempDir()
	authorize := func(ctx context.Context, id string, use func(Description, []string) error) error { return use(d, nil) }
	root := func(id string) (string, error) {
		if id != d.ID {
			t.Fatalf("root for %q", id)
		}
		return dir, nil
	}
	b := NewActionBroker(authorize, func(context.Context, string, Call) (<-chan Reply, error) {
		ch := make(chan Reply, 1)
		ch <- Reply{Message: Envelope{Type: TypeResult, Result: &Result{Value: json.RawMessage(`{"results":[{"kind":"artifact","label":"report.txt","data":"aGk="}]}`)}}}
		return ch, nil
	})
	b.ArtifactRoot = root
	got := b.Invoke(t.Context(), r)
	if got.Error != nil || len(got.Results) != 1 {
		t.Fatalf("invoke: %+v", got)
	}
	handle := got.Results[0].Handle
	files, _ := filepath.Glob(filepath.Join(dir, "artifacts", "*"))
	if len(files) != 2 {
		t.Fatalf("persisted files: %v", files)
	}
	for _, f := range files {
		if info, err := os.Stat(f); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode: %v %v", f, info.Mode(), err)
		}
	}
	restarted := NewActionBroker(authorize, func(context.Context, string, Call) (<-chan Reply, error) { t.Fatal("unexpected call"); return nil, nil })
	restarted.ArtifactRoot = root
	if name, data, err := restarted.Artifact(t.Context(), handle); err != nil || name != "report.txt" || string(data) != "hi" {
		t.Fatalf("recovered artifact: %q %q %v", name, data, err)
	}
	d.Actions[0].Label = "Changed"
	if _, _, err := restarted.Artifact(t.Context(), handle); err == nil {
		t.Fatal("restart skipped authorization re-check")
	}
	d.Actions[0].Label = "Test"
	for _, forged := range []string{"unknown", handle + "/x", "!." + "AAAA", handle[:strings.Index(handle, ".")] + ".bad-id"} {
		if _, _, err := restarted.Artifact(t.Context(), forged); err == nil {
			t.Fatalf("forged handle %q served", forged)
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := restarted.Artifact(t.Context(), handle); err == nil {
		t.Fatal("artifact served after plugin data removal")
	}
	// The broker that produced the handle must not revive it from memory once
	// permanent removal deleted the data root and the plugin was reapproved.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Artifact(t.Context(), handle); err == nil {
		t.Fatal("same broker served a deleted artifact")
	}
}

func TestActionAdmissionLimits(t *testing.T) {
	d, r := actionFixture()
	b := NewActionBroker(func(ctx context.Context, id string, use func(Description, []string) error) error { return use(d, nil) }, func(context.Context, string, Call) (<-chan Reply, error) { t.Fatal("unexpected call"); return nil, nil })
	r.ActionID = "missing"
	if got := b.Invoke(t.Context(), r); got.Error.Category != ErrorNotFound {
		t.Fatal(got)
	}
	r.ActionID = "test"
	r.Placement, r.Context.ProjectID = "project", "project"
	if got := b.Invoke(t.Context(), r); got.Error.Category != ErrorPermissionDenied {
		t.Fatal(got)
	}
	r.Placement = "global"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := b.Invoke(ctx, r); got.Error.Category != ErrorCancelled {
		t.Fatal(got)
	}
	r.OperationID = ""
	if got := b.Invoke(t.Context(), r); got.Error.Category != ErrorInvalidArgument {
		t.Fatal(got)
	}
	r.OperationID = "op"
	for i := range 4096 {
		b.operations[fmt.Sprint(i)] = &actionOperation{}
	}
	if got := b.Invoke(t.Context(), r); got.Error.Category != ErrorUnavailable {
		t.Fatal(got)
	}
	b.bytes = 32 << 20
	if got := b.storeArtifacts(&actionOperation{}, ActionResponse{Results: []ActionResult{{Kind: "artifact", Data: []byte("x")}}}); got.Error.Category != ErrorUnavailable {
		t.Fatal(got)
	}
	if _, err := DecodeActionResults(json.RawMessage(strings.Repeat("x", MaxMessageBytes+1))); err == nil {
		t.Fatal("oversized result")
	}
}

func TestActionResultKinds(t *testing.T) {
	for _, raw := range []string{
		`{"results":[{"kind":"notice","text":"Done"}]}`,
		`{"results":[{"kind":"link","label":"Open","url":"https://example.com"}]}`,
		`{"results":[{"kind":"artifact","label":"report.txt","data":"aGk="}]}`,
		`{"results":[{"kind":"navigation","target":"sessions"}]}`,
		`{"results":[{"kind":"refresh","target":"actions"}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := DecodeActionResults(json.RawMessage(raw)); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, raw := range []string{
		`{"results":[{"kind":"html","text":"<b>Hi</b>"}]}`,
		`{"results":[{"kind":"notice","text":"Done","html":"secret"}]}`,
		`{"results":[{"kind":"link","label":"Bad","url":"javascript:alert(1)"}]}`,
		`{"results":[{"kind":"link","label":"Bad","url":"file:///etc/passwd"}]}`,
		`{"results":[{"kind":"artifact","label":"../file","data":"aGk="}]}`,
		`{"results":[{"kind":"artifact","label":"file","handle":"forged"}]}`,
		`{"results":[{"kind":"navigation","target":"/etc/passwd"}]}`,
		`{"results":[{"kind":"refresh","target":"arbitrary"}]}`,
		`null`, `{}',`, `{"results":[]}`, `{"results":[{"kind":"notice","text":"ok","url":"https://example.com"}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := DecodeActionResults(json.RawMessage(raw)); err == nil {
				t.Fatal("accepted unsafe result")
			}
		})
	}
}
