package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// fakePlatform records mutation calls. The embedded interface panics on
// any method the test didn't expect to be called.
type fakePlatform struct {
	platforms.Platform

	id        platforms.ID
	available bool

	sendErr       error
	permissionErr error
	ruleErr       error
	createResp    *platforms.CreateSessionResponse
	createNil     bool

	sent        []platforms.SendMessageRequest
	commands    []platforms.ExecuteCommandRequest
	shells      []platforms.RunShellRequest
	renames     []platforms.RenameSessionRequest
	aborts      []platforms.AbortRequest
	compacts    []platforms.CompactRequest
	rules       []platforms.SetPermissionRulesRequest
	permissions []platforms.RespondPermissionRequest
	answers     []platforms.RespondQuestionRequest
	rejects     []platforms.RejectQuestionRequest
	creates     []platforms.CreateSessionRequest
	forks       []platforms.ForkSessionRequest
	moves       []platforms.MoveSessionRequest
	reverts     []platforms.RevertSessionRequest
	unreverts   []platforms.UnrevertSessionRequest
	owned       map[string]bool
}

func (f *fakePlatform) ID() platforms.ID                 { return f.id }
func (f *fakePlatform) Available(_ context.Context) bool { return f.available }
func (f *fakePlatform) SendMessage(_ context.Context, req platforms.SendMessageRequest) error {
	f.sent = append(f.sent, req)
	return f.sendErr
}
func (f *fakePlatform) ExecuteCommand(_ context.Context, req platforms.ExecuteCommandRequest) error {
	f.commands = append(f.commands, req)
	return nil
}
func (f *fakePlatform) RunShell(_ context.Context, req platforms.RunShellRequest) error {
	f.shells = append(f.shells, req)
	return nil
}
func (f *fakePlatform) RenameSession(_ context.Context, req platforms.RenameSessionRequest) error {
	f.renames = append(f.renames, req)
	return nil
}
func (f *fakePlatform) Abort(_ context.Context, req platforms.AbortRequest) error {
	f.aborts = append(f.aborts, req)
	return nil
}
func (f *fakePlatform) DisposeSession(_ context.Context, req platforms.DisposeSessionRequest) error {
	delete(f.owned, req.SessionID)
	return nil
}
func (f *fakePlatform) Compact(_ context.Context, req platforms.CompactRequest) error {
	f.compacts = append(f.compacts, req)
	return nil
}
func (f *fakePlatform) SetPermissionRules(_ context.Context, req platforms.SetPermissionRulesRequest) error {
	f.rules = append(f.rules, req)
	return f.ruleErr
}

func TestCreateConfiguredDoesNotPublishOrLeakSessionWhenRulesFail(t *testing.T) {
	platform := &fakePlatform{id: "opencode", available: true, ruleErr: errors.New("rules failed"), owned: map[string]bool{"new-session": true}}
	registry := &fakeRegistry{byID: map[platforms.ID]platforms.Platform{"opencode": platform}}
	published := 0
	svc := New(registry, Hooks{SessionCreated: func(CreatedSession) { published++ }})

	_, err := svc.CreateConfigured(context.Background(), "opencode", platforms.CreateSessionRequest{Directory: "/repo"}, []platforms.PermissionRule{{Permission: "read", Pattern: "*", Action: "allow"}})
	if err == nil || published != 0 || platform.owned["new-session"] {
		t.Fatalf("CreateConfigured error = %v, published = %d, session remains discoverable = %v", err, published, platform.owned["new-session"])
	}
}

func TestCreateConfiguredPublishesAfterRules(t *testing.T) {
	platform := &fakePlatform{id: "opencode", available: true}
	registry := &fakeRegistry{byID: map[platforms.ID]platforms.Platform{"opencode": platform}}
	publishedAfterRules := false
	svc := New(registry, Hooks{SessionCreated: func(CreatedSession) { publishedAfterRules = len(platform.rules) == 1 }})

	if _, err := svc.CreateConfigured(context.Background(), "opencode", platforms.CreateSessionRequest{Directory: "/repo"}, []platforms.PermissionRule{{Permission: "read", Action: "allow"}}); err != nil {
		t.Fatal(err)
	}
	if !publishedAfterRules || len(platform.aborts) != 0 {
		t.Fatalf("publishedAfterRules = %v, aborts = %#v", publishedAfterRules, platform.aborts)
	}
}
func (f *fakePlatform) RespondPermission(_ context.Context, req platforms.RespondPermissionRequest) error {
	f.permissions = append(f.permissions, req)
	return f.permissionErr
}
func (f *fakePlatform) RespondQuestion(_ context.Context, req platforms.RespondQuestionRequest) error {
	f.answers = append(f.answers, req)
	return nil
}
func (f *fakePlatform) RejectQuestion(_ context.Context, req platforms.RejectQuestionRequest) error {
	f.rejects = append(f.rejects, req)
	return nil
}
func (f *fakePlatform) CreateSession(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	f.creates = append(f.creates, req)
	if f.createNil {
		return nil, nil
	}
	if f.createResp != nil {
		return f.createResp, nil
	}
	return &platforms.CreateSessionResponse{ID: "new-session"}, nil
}
func (f *fakePlatform) ForkSession(_ context.Context, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	f.forks = append(f.forks, req)
	return &platforms.CreateSessionResponse{ID: "forked-session"}, nil
}
func (f *fakePlatform) MoveSession(_ context.Context, req platforms.MoveSessionRequest) error {
	f.moves = append(f.moves, req)
	return nil
}
func (f *fakePlatform) Revert(_ context.Context, req platforms.RevertSessionRequest) error {
	f.reverts = append(f.reverts, req)
	return nil
}
func (f *fakePlatform) Unrevert(_ context.Context, req platforms.UnrevertSessionRequest) error {
	f.unreverts = append(f.unreverts, req)
	return nil
}

// fakeRegistry serves Get from byID and PlatformForSession from owner.
type fakeRegistry struct {
	byID  map[platforms.ID]platforms.Platform
	owner platforms.Platform
}

func (r *fakeRegistry) Get(id platforms.ID) (platforms.Platform, bool) {
	p, ok := r.byID[id]
	return p, ok
}

func (r *fakeRegistry) Platforms() []platforms.Platform {
	out := make([]platforms.Platform, 0, len(r.byID))
	for _, p := range r.byID {
		out = append(out, p)
	}
	return out
}

func (r *fakeRegistry) PlatformForSession(_ context.Context, _ string) (platforms.Platform, bool) {
	if r.owner == nil {
		return nil, false
	}
	return r.owner, true
}

func newService(p *fakePlatform, hooks Hooks) (*Service, *fakeRegistry) {
	reg := &fakeRegistry{
		byID:  map[platforms.ID]platforms.Platform{p.id: p},
		owner: p,
	}
	return New(reg, hooks), reg
}

func isValidation(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

func TestRevertAndUnrevert(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	svc, _ := newService(p, Hooks{})
	ctx := context.Background()

	if err := svc.Revert(ctx, "opencode", platforms.RevertSessionRequest{SessionID: "s1", MessageID: "m1"}); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if err := svc.Unrevert(ctx, "opencode", platforms.UnrevertSessionRequest{SessionID: "s1"}); err != nil {
		t.Fatalf("Unrevert: %v", err)
	}
	if got := p.reverts; len(got) != 1 || got[0].MessageID != "m1" {
		t.Fatalf("reverts = %#v", got)
	}
	if got := p.unreverts; len(got) != 1 || got[0].SessionID != "s1" {
		t.Fatalf("unreverts = %#v", got)
	}
}

func TestValidationErrors(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	svc, _ := newService(p, Hooks{})
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{"send message empty", func() error {
			return svc.SendMessage(ctx, "", platforms.SendMessageRequest{SessionID: "s1"})
		}},
		{"command empty", func() error {
			return svc.ExecuteCommand(ctx, "", platforms.ExecuteCommandRequest{SessionID: "s1"})
		}},
		{"shell whitespace", func() error {
			return svc.RunShell(ctx, "", platforms.RunShellRequest{SessionID: "s1", Command: "   "})
		}},
		{"rename empty title", func() error {
			return svc.Rename(ctx, "", platforms.RenameSessionRequest{SessionID: "s1"})
		}},
		{"compact missing model", func() error {
			return svc.Compact(ctx, "", platforms.CompactRequest{SessionID: "s1", ProviderID: "anthropic"})
		}},
		{"too many rules", func() error {
			return svc.SetPermissionRules(ctx, "", platforms.SetPermissionRulesRequest{
				SessionID: "s1", Rules: make([]platforms.PermissionRule, 101),
			})
		}},
		{"rule missing permission", func() error {
			return svc.SetPermissionRules(ctx, "", platforms.SetPermissionRulesRequest{
				SessionID: "s1", Rules: []platforms.PermissionRule{{Action: "allow"}},
			})
		}},
		{"rule bad action", func() error {
			return svc.SetPermissionRules(ctx, "", platforms.SetPermissionRulesRequest{
				SessionID: "s1", Rules: []platforms.PermissionRule{{Permission: "bash", Action: "maybe"}},
			})
		}},
		{"permission bad reply", func() error {
			return svc.RespondPermission(ctx, "", platforms.RespondPermissionRequest{
				SessionID: "s1", PermissionID: "p1", Reply: "yes",
			})
		}},
		{"permission missing id", func() error {
			return svc.RespondPermission(ctx, "", platforms.RespondPermissionRequest{
				SessionID: "s1", Reply: "once",
			})
		}},
		{"question missing id", func() error {
			return svc.RespondQuestion(ctx, "", platforms.RespondQuestionRequest{SessionID: "s1"})
		}},
		{"reject missing id", func() error {
			return svc.RejectQuestion(ctx, "", platforms.RejectQuestionRequest{SessionID: "s1"})
		}},
		{"fork missing session id", func() error {
			_, err := svc.Fork(ctx, "", platforms.ForkSessionRequest{})
			return err
		}},
		{"move missing directory", func() error {
			return svc.Move(ctx, "", platforms.MoveSessionRequest{SessionID: "s1", Directory: "   "})
		}},
		{"create missing directory", func() error {
			_, err := svc.Create(ctx, "", platforms.CreateSessionRequest{})
			return err
		}},
		{"create unknown platform", func() error {
			_, err := svc.Create(ctx, "nope", platforms.CreateSessionRequest{Directory: "/tmp"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if !isValidation(err) {
				t.Fatalf("expected ValidationError, got %v", err)
			}
		})
	}
	// None of the invalid calls may have reached the adapter.
	if len(p.sent)+len(p.commands)+len(p.shells)+len(p.renames)+len(p.compacts)+
		len(p.rules)+len(p.permissions)+len(p.answers)+len(p.rejects)+len(p.creates)+
		len(p.forks)+len(p.moves) != 0 {
		t.Fatal("validation failure leaked a platform call")
	}
}

// TestForkAndMoveDelegate covers the happy path for the two new
// mutations, including the SessionCreated hook firing (both change
// which project row a session belongs to).
func TestForkAndMoveDelegate(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	var created []CreatedSession
	svc, _ := newService(p, Hooks{SessionCreated: func(info CreatedSession) { created = append(created, info) }})
	ctx := context.Background()

	resp, err := svc.Fork(ctx, "", platforms.ForkSessionRequest{SessionID: "s1", MessageID: "m1"})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if resp.ID != "forked-session" {
		t.Errorf("Fork ID = %q, want forked-session", resp.ID)
	}
	if err := svc.Move(ctx, "", platforms.MoveSessionRequest{SessionID: "s1", Directory: "/tmp/dst"}); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if len(p.forks) != 1 || len(p.moves) != 1 {
		t.Fatalf("expected fork+move to reach adapter once each, got forks=%d moves=%d", len(p.forks), len(p.moves))
	}
	if len(created) != 2 || created[0].ID != "forked-session" || created[1].ID != "s1" {
		t.Errorf("SessionCreated hook calls = %v, want [forked-session s1]", created)
	}
	if created[1].Directory != "/tmp/dst" {
		t.Errorf("move hook directory = %q, want /tmp/dst", created[1].Directory)
	}
}

func TestResolveUnknownSessionAndPlatform(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	svc, reg := newService(p, Hooks{})
	ctx := context.Background()

	// Explicit unknown platform hint.
	err := svc.Abort(ctx, "ghost", platforms.AbortRequest{SessionID: "s1"})
	if !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown platform hint, got %v", err)
	}

	// No owner claims the session.
	reg.owner = nil
	err = svc.Abort(ctx, "", platforms.AbortRequest{SessionID: "s1"})
	if !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unowned session, got %v", err)
	}
}

func TestMutationsDelegateToOwner(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	svc, _ := newService(p, Hooks{})
	ctx := context.Background()

	if err := svc.SendMessage(ctx, "", platforms.SendMessageRequest{SessionID: "s1", Message: "hi"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if err := svc.ExecuteCommand(ctx, "", platforms.ExecuteCommandRequest{SessionID: "s1", Command: "/init"}); err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}
	if err := svc.RunShell(ctx, "", platforms.RunShellRequest{SessionID: "s1", Command: "ls"}); err != nil {
		t.Fatalf("RunShell: %v", err)
	}
	if err := svc.Rename(ctx, "", platforms.RenameSessionRequest{SessionID: "s1", Title: "t"}); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := svc.Abort(ctx, "", platforms.AbortRequest{SessionID: "s1"}); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if err := svc.Compact(ctx, "", platforms.CompactRequest{SessionID: "s1", ProviderID: "a", ModelID: "m"}); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if err := svc.RespondQuestion(ctx, "", platforms.RespondQuestionRequest{SessionID: "s1", RequestID: "q1"}); err != nil {
		t.Fatalf("RespondQuestion: %v", err)
	}
	if err := svc.RejectQuestion(ctx, "", platforms.RejectQuestionRequest{SessionID: "s1", RequestID: "q1"}); err != nil {
		t.Fatalf("RejectQuestion: %v", err)
	}
	if len(p.sent) != 1 || len(p.commands) != 1 || len(p.shells) != 1 || len(p.renames) != 1 ||
		len(p.aborts) != 1 || len(p.compacts) != 1 || len(p.answers) != 1 || len(p.rejects) != 1 {
		t.Fatal("expected each mutation to reach the adapter exactly once")
	}

	// Explicit platform hint routes via Get.
	if err := svc.Abort(ctx, "opencode", platforms.AbortRequest{SessionID: "s1"}); err != nil {
		t.Fatalf("Abort with hint: %v", err)
	}
	if len(p.aborts) != 2 {
		t.Fatalf("expected 2 aborts, got %d", len(p.aborts))
	}
}

func TestSetPermissionRulesNormalizesPattern(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	svc, _ := newService(p, Hooks{})

	err := svc.SetPermissionRules(context.Background(), "", platforms.SetPermissionRulesRequest{
		SessionID: "s1",
		Rules:     []platforms.PermissionRule{{Permission: "bash", Action: "allow"}},
	})
	if err != nil {
		t.Fatalf("SetPermissionRules: %v", err)
	}
	if got := p.rules[0].Rules[0].Pattern; got != "*" {
		t.Fatalf("expected empty pattern to default to *, got %q", got)
	}
}

func TestRespondPermissionFiresHookBeforeAdapter(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	var hooked []string
	svc, reg := newService(p, Hooks{
		PermissionReplied: func(sessionID, permissionID string) {
			if len(p.permissions) != 0 {
				t.Error("hook fired after the adapter call")
			}
			hooked = append(hooked, sessionID+"|"+permissionID)
		},
	})
	ctx := context.Background()

	err := svc.RespondPermission(ctx, "", platforms.RespondPermissionRequest{
		SessionID: "s1", PermissionID: "p1", Reply: "once",
	})
	if err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	if len(hooked) != 1 || hooked[0] != "s1|p1" {
		t.Fatalf("expected hook s1|p1, got %v", hooked)
	}
	if len(p.permissions) != 1 {
		t.Fatalf("expected 1 adapter call, got %d", len(p.permissions))
	}

	// Hook must not fire when resolution fails.
	reg.owner = nil
	_ = svc.RespondPermission(ctx, "", platforms.RespondPermissionRequest{
		SessionID: "s2", PermissionID: "p2", Reply: "once",
	})
	if len(hooked) != 1 {
		t.Fatal("hook fired for an unresolvable session")
	}
}

func TestRespondPermissionFiresSuccessHookOnlyAfterSuccess(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	var got platforms.RespondPermissionRequest
	var gotPlatform platforms.ID
	var hookCalls int
	svc, _ := newService(p, Hooks{
		PermissionReplySucceeded: func(ctx context.Context, platform platforms.ID, req platforms.RespondPermissionRequest) {
			hookCalls++
			gotPlatform, got = platform, req
			if ctx.Err() != nil {
				t.Errorf("success hook context already canceled: %v", ctx.Err())
			}
		},
	})
	req := platforms.RespondPermissionRequest{SessionID: "s1", PermissionID: "p1", Reply: "always"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.RespondPermission(ctx, "opencode", req); err != nil {
		t.Fatal(err)
	}
	if hookCalls != 1 || gotPlatform != "opencode" || got != req {
		t.Fatalf("success hook = calls %d platform %q req %#v", hookCalls, gotPlatform, got)
	}

	p.permissionErr = errors.New("send failed")
	if err := svc.RespondPermission(context.Background(), "opencode", req); err == nil {
		t.Fatal("expected adapter error")
	}
	if hookCalls != 1 {
		t.Fatalf("success hook fired after adapter failure: %d", hookCalls)
	}
}

func TestCreateSelectionPolicy(t *testing.T) {
	ctx := context.Background()
	oc := &fakePlatform{id: "opencode", available: true}
	other := &fakePlatform{id: "other", available: true}
	offline := &fakePlatform{id: "offline", available: false}

	// Exactly one available platform: auto-selected.
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{
		"opencode": oc, "offline": offline,
	}}, Hooks{})
	resp, err := svc.Create(ctx, "", platforms.CreateSessionRequest{Directory: "/tmp"})
	if err != nil || resp == nil || resp.ID != "new-session" {
		t.Fatalf("expected auto-selected create, got resp=%v err=%v", resp, err)
	}
	if len(oc.creates) != 1 {
		t.Fatalf("expected create on the sole available platform, got %d", len(oc.creates))
	}

	// Multiple available platforms without a hint: validation error.
	svc = New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{
		"opencode": oc, "other": other,
	}}, Hooks{})
	if _, err := svc.Create(ctx, "", platforms.CreateSessionRequest{Directory: "/tmp"}); !isValidation(err) {
		t.Fatalf("expected ValidationError for ambiguous platform, got %v", err)
	}

	// Explicit hint disambiguates.
	if _, err := svc.Create(ctx, "other", platforms.CreateSessionRequest{Directory: "/tmp"}); err != nil {
		t.Fatalf("Create with hint: %v", err)
	}
	if len(other.creates) != 1 {
		t.Fatalf("expected create on hinted platform, got %d", len(other.creates))
	}

	// No available platform.
	svc = New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{"offline": offline}}, Hooks{})
	if _, err := svc.Create(ctx, "", platforms.CreateSessionRequest{Directory: "/tmp"}); !errors.Is(err, ErrNoPlatformAvailable) {
		t.Fatalf("expected ErrNoPlatformAvailable, got %v", err)
	}
}

func TestCreateFiresSessionCreatedHook(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	var created []CreatedSession
	svc, _ := newService(p, Hooks{SessionCreated: func(info CreatedSession) { created = append(created, info) }})

	if _, err := svc.Create(context.Background(), "opencode", platforms.CreateSessionRequest{Directory: "/tmp", Title: "hi"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(created) != 1 || created[0].ID != "new-session" {
		t.Fatalf("expected SessionCreated hook for new-session, got %v", created)
	}
	if created[0].Directory != "/tmp" || created[0].Title != "hi" || created[0].Platform != "opencode" {
		t.Fatalf("expected provisional fields on hook, got %+v", created[0])
	}
}

func TestCreateRejectsInvalidAdapterResponse(t *testing.T) {
	for _, p := range []*fakePlatform{
		{id: "opencode", available: true, createNil: true},
		{id: "opencode", available: true, createResp: &platforms.CreateSessionResponse{}},
	} {
		svc, _ := newService(p, Hooks{})
		if _, err := svc.Create(context.Background(), "opencode", platforms.CreateSessionRequest{Directory: "/tmp"}); err == nil {
			t.Fatal("Create accepted an invalid adapter response")
		}
	}
}

func TestClientBindsPlatform(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	svc, _ := newService(p, Hooks{})
	client := svc.Client("opencode")
	ctx := context.Background()

	resp, err := client.CreateSession(ctx, platforms.CreateSessionRequest{Directory: "/tmp"})
	if err != nil || resp.ID != "new-session" {
		t.Fatalf("CreateSession: resp=%v err=%v", resp, err)
	}
	if err := client.SendMessage(ctx, platforms.SendMessageRequest{SessionID: "new-session", Message: "go"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if err := client.SetPermissionRules(ctx, platforms.SetPermissionRulesRequest{
		SessionID: "new-session",
		Rules:     []platforms.PermissionRule{{Permission: "edit", Pattern: "**", Action: "deny"}},
	}); err != nil {
		t.Fatalf("SetPermissionRules: %v", err)
	}
	if len(p.creates) != 1 || len(p.sent) != 1 || len(p.rules) != 1 {
		t.Fatal("expected client calls to reach the bound adapter")
	}
	if p.rules[0].SessionID != "new-session" || len(p.rules[0].Rules) != 1 {
		t.Fatalf("permission rules not forwarded through client: %+v", p.rules)
	}

	// Unknown bound platform surfaces as an error, not a panic.
	ghost := svc.Client("ghost")
	if _, err := ghost.CreateSession(ctx, platforms.CreateSessionRequest{Directory: "/tmp"}); err == nil {
		t.Fatal("expected error for unknown bound platform")
	}
	if err := ghost.SendMessage(ctx, platforms.SendMessageRequest{SessionID: "x", Message: "hi"}); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := ghost.SetPermissionRules(ctx, platforms.SetPermissionRulesRequest{SessionID: "x"}); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("expected ErrNotFound from SetPermissionRules, got %v", err)
	}
}

// #533: transports call KnownPlatform to reject an unknown platform
// before running side effects (e.g. launching a managed opencode) that
// Create would only reject afterwards. It must answer from the same
// registry Create uses, and never panic on a partially-built service.
func TestKnownPlatform(t *testing.T) {
	svc, _ := newService(&fakePlatform{id: "opencode", available: true}, Hooks{})

	tests := []struct {
		name     string
		svc      *Service
		platform string
		want     bool
	}{
		{"registered", svc, "opencode", true},
		{"unregistered", svc, "bogus", false},
		{"empty is auto-pick", svc, "", true},
		{"nil registry", New(nil, Hooks{}), "opencode", false},
		{"nil service", nil, "opencode", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.svc.KnownPlatform(tt.platform); got != tt.want {
				t.Fatalf("KnownPlatform(%q) = %v; want %v", tt.platform, got, tt.want)
			}
		})
	}
}
