package ownedjudge

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type judgeSource struct {
	platforms.Platform
	judge   autoapprove.Judge
	missing bool
}

func (*judgeSource) ID() platforms.ID { return "test" }
func (*judgeSource) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "test", SourceKey: "source"}, nil
}
func (p *judgeSource) SessionIdentityMetadata(context.Context, string) (*db.Session, error) {
	if p.missing {
		return nil, platforms.ErrNotFound
	}
	return &db.Session{ID: "native-helper", Directory: "/repo"}, nil
}
func (p *judgeSource) PermissionJudge() autoapprove.Judge { return p.judge }

type nativeJudge struct{ id string }

func (j nativeJudge) JudgeWithCallback(_ context.Context, _, _ string, _ []string, _ map[string]any, _ []autoapprove.PromptSection, callback func(string)) autoapprove.JudgeResult {
	callback(j.id)
	return autoapprove.JudgeResult{Verdict: "safe", SessionID: j.id}
}

func TestFallbackJudgePreservesCapabilityAndProjectsHelperCallbacks(t *testing.T) {
	core := statetest.Open(t, sessioncatalog.Schema)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	native := &judgeSource{judge: nativeJudge{id: "native-helper"}}
	canonical, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := Wrap(canonical, native)
	judge := wrapped.(interface{ PermissionJudge() autoapprove.Judge }).PermissionJudge()
	var observed string
	result := judge.JudgeWithCallback(t.Context(), "/repo", "read", nil, nil, nil, func(id string) { observed = id })
	if !identity.IsSessionID(observed) || result.SessionID != observed || result.EvaluationFailed || result.Verdict != "safe" {
		t.Fatalf("fallback judge lost canonical boundary: %q %+v", observed, result)
	}
	native.judge = nativeJudge{}
	wrapped = Wrap(canonical, native)
	result = wrapped.(interface{ PermissionJudge() autoapprove.Judge }).PermissionJudge().JudgeWithCallback(t.Context(), "/repo", "read", nil, nil, nil, nil)
	if result.EvaluationFailed || result.SessionID != "" {
		t.Fatalf("isolated helper without a session gained identity: %+v", result)
	}
	native.judge = nativeJudge{id: "unknown-helper"}
	native.missing = true
	wrapped = Wrap(canonical, native)
	observed = ""
	result = wrapped.(interface{ PermissionJudge() autoapprove.Judge }).PermissionJudge().JudgeWithCallback(t.Context(), "/repo", "read", nil, nil, nil, func(id string) { observed = id })
	if !result.EvaluationFailed || result.Verdict == "safe" || observed != "" || result.SessionID != "" {
		t.Fatalf("unproven helper crossed judge boundary: %q %+v", observed, result)
	}
	native.judge = nil
	if Wrap(canonical, native) != canonical {
		t.Fatal("nil judge capability was invented")
	}
	if Wrap(nil, native) != nil {
		t.Fatal("nil canonical adapter became an executable wrapper")
	}
}
