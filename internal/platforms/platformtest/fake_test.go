package platformtest

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestZeroFakeDefaults(t *testing.T) {
	ctx := context.Background()
	f := &Fake{}
	if f.ID() != "fake" || f.DisplayName() != "fake" || !f.Available(ctx) {
		t.Fatalf("identity = %q %q %v", f.ID(), f.DisplayName(), f.Available(ctx))
	}
	if _, err := f.Session(ctx, "s", 0, 0); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("Session err = %v", err)
	}
	if f.Owns(ctx, "s") || f.LiveStatus("s") != nil {
		t.Fatal("zero fake owns or reports live state")
	}
	if cat, err := f.DirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{}); err != nil || cat.Models == nil {
		t.Fatalf("DirectoryCatalog = %+v, %v", cat, err)
	}
	if err := f.DisposeSession(ctx, platforms.DisposeSessionRequest{}); err != nil {
		t.Fatalf("DisposeSession = %v", err)
	}
	reads := map[string]error{}
	reads["SessionChanges"] = second(f.SessionChanges(ctx, "s"))
	reads["SessionInfo"] = second(f.SessionInfo(ctx, "s"))
	reads["PermissionRules"] = second(f.PermissionRules(ctx, "s"))
	reads["ForkSession"] = second(f.ForkSession(ctx, platforms.ForkSessionRequest{}))
	reads["CreateSession"] = second(f.CreateSession(ctx, platforms.CreateSessionRequest{}))
	for name, err := range reads {
		if !errors.Is(err, platforms.ErrUnsupported) {
			t.Errorf("%s err = %v, want ErrUnsupported", name, err)
		}
	}
	for name, err := range operations(ctx, f) {
		if !errors.Is(err, platforms.ErrUnsupported) {
			t.Errorf("%s err = %v, want ErrUnsupported", name, err)
		}
	}
	for name, err := range map[string]error{
		"Sessions":               second(f.Sessions(ctx, "", 0)),
		"SessionsInactiveBefore": second(f.SessionsInactiveBefore(ctx, 0)),
		"AgentCatalog":           second(f.AgentCatalog(ctx, "s")),
		"SlashCommands":          second(f.SlashCommands(ctx, "s")),
		"SessionModels":          second(f.SessionModels(ctx, "s")),
		"ListPermissions":        second(f.ListPermissions(ctx, "s")),
		"ListQuestions":          second(f.ListQuestions(ctx, "s")),
	} {
		if err != nil {
			t.Errorf("%s err = %v", name, err)
		}
	}
}

func TestFakeConfiguredFields(t *testing.T) {
	ctx := context.Background()
	down := false
	f := &Fake{
		PlatformID:   "p",
		Name:         "Platform",
		Availability: &down,
		Caps:         platforms.Capabilities{Composer: true},
		SessionList:  []db.Session{{ID: "s"}},
		Inactive:     []db.SessionArchiveCandidate{{}},
		Live:         &platforms.LiveState{},
		Changes:      &platforms.SessionChanges{},
		Info:         &platforms.SessionInfo{},
	}
	if f.ID() != "p" || f.DisplayName() != "Platform" || f.Available(ctx) || !f.Capabilities().Composer {
		t.Fatal("identity fields ignored")
	}
	if got, _ := f.Sessions(ctx, "", 0); len(got) != 1 || !f.Owns(ctx, "s") {
		t.Fatalf("sessions = %+v", got)
	}
	if got, _ := f.SessionsInactiveBefore(ctx, 0); len(got) != 1 || f.LiveStatus("s") == nil {
		t.Fatal("inactive or live state ignored")
	}
	if got, err := f.SessionChanges(ctx, "s"); got == nil || err != nil {
		t.Fatalf("changes = %v, %v", got, err)
	}
	if got, err := f.SessionInfo(ctx, "s"); got == nil || err != nil {
		t.Fatalf("info = %v, %v", got, err)
	}
}

func TestFakeHooksReplaceMethods(t *testing.T) {
	ctx := context.Background()
	hook := errors.New("hook")
	var calls int
	fail := func() error { calls++; return hook }
	f := &Fake{
		SessionsFn: func(context.Context, string, int64) ([]db.Session, error) { return nil, fail() },
		SessionFn:  func(string) (*platforms.SessionDetail, error) { return nil, fail() },
		SessionModelsFn: func() *platforms.SessionModelsResponse {
			calls++
			return &platforms.SessionModelsResponse{}
		},
		DirectoryCatalogFn:   func(platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) { return nil, fail() },
		ListPermissionsFn:    func(string) ([]platforms.LivePrompt, error) { return nil, fail() },
		ListQuestionsFn:      func(string) ([]platforms.LivePrompt, error) { return nil, fail() },
		PermissionRulesFn:    func(string) ([]platforms.PermissionRule, error) { return nil, fail() },
		SendMessageFn:        func(platforms.SendMessageRequest) error { return fail() },
		ExecuteCommandFn:     func(platforms.ExecuteCommandRequest) error { return fail() },
		RunShellFn:           fail,
		RespondPermissionFn:  func(platforms.RespondPermissionRequest) error { return fail() },
		RespondQuestionFn:    func(platforms.RespondQuestionRequest) error { return fail() },
		RejectQuestionFn:     func(platforms.RejectQuestionRequest) error { return fail() },
		AbortFn:              func(platforms.AbortRequest) error { return fail() },
		RevertFn:             func(platforms.RevertSessionRequest) error { return fail() },
		UnrevertFn:           func(platforms.UnrevertSessionRequest) error { return fail() },
		RenameSessionFn:      func(platforms.RenameSessionRequest) error { return fail() },
		SetPermissionRulesFn: func(platforms.SetPermissionRulesRequest) error { return fail() },
		CompactFn:            func(platforms.CompactRequest) error { return fail() },
		ForkSessionFn: func(platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
			return nil, fail()
		},
		MoveSessionFn: func(platforms.MoveSessionRequest) error { return fail() },
		CreateSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return nil, fail()
		},
		DisposeFn:     func(platforms.DisposeSessionRequest) error { return fail() },
		ProxyEventsFn: func(context.Context, string, io.Writer, func()) error { return fail() },
	}
	errs := []error{
		second(f.Sessions(ctx, "", 0)),
		second(f.Session(ctx, "s", 0, 0)),
		second(f.DirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{})),
		second(f.ListPermissions(ctx, "s")),
		second(f.ListQuestions(ctx, "s")),
		second(f.PermissionRules(ctx, "s")),
		second(f.ForkSession(ctx, platforms.ForkSessionRequest{})),
		second(f.CreateSession(ctx, platforms.CreateSessionRequest{})),
		f.DisposeSession(ctx, platforms.DisposeSessionRequest{}),
	}
	for _, err := range operations(ctx, f) {
		errs = append(errs, err)
	}
	for i, err := range errs {
		if !errors.Is(err, hook) {
			t.Errorf("call %d err = %v, want hook", i, err)
		}
	}
	if models, _ := f.SessionModels(ctx, "s"); models == nil {
		t.Fatal("SessionModelsFn ignored")
	}
	if want := len(errs) + 1; calls != want {
		t.Fatalf("hooks called %d times, want %d", calls, want)
	}
}

// operations calls every request-taking operation that defaults to
// ErrUnsupported.
func operations(ctx context.Context, f *Fake) map[string]error {
	return map[string]error{
		"SendMessage":        f.SendMessage(ctx, platforms.SendMessageRequest{}),
		"ExecuteCommand":     f.ExecuteCommand(ctx, platforms.ExecuteCommandRequest{}),
		"RunShell":           f.RunShell(ctx, platforms.RunShellRequest{}),
		"RespondPermission":  f.RespondPermission(ctx, platforms.RespondPermissionRequest{}),
		"RespondQuestion":    f.RespondQuestion(ctx, platforms.RespondQuestionRequest{}),
		"RejectQuestion":     f.RejectQuestion(ctx, platforms.RejectQuestionRequest{}),
		"Abort":              f.Abort(ctx, platforms.AbortRequest{}),
		"Revert":             f.Revert(ctx, platforms.RevertSessionRequest{}),
		"Unrevert":           f.Unrevert(ctx, platforms.UnrevertSessionRequest{}),
		"RenameSession":      f.RenameSession(ctx, platforms.RenameSessionRequest{}),
		"SetPermissionRules": f.SetPermissionRules(ctx, platforms.SetPermissionRulesRequest{}),
		"Compact":            f.Compact(ctx, platforms.CompactRequest{}),
		"MoveSession":        f.MoveSession(ctx, platforms.MoveSessionRequest{}),
		"ProxyEvents":        f.ProxyEvents(ctx, "s", io.Discard, func() {}),
	}
}

func second[T any](_ T, err error) error { return err }
