package autoapprove

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

// fakeSettingStore implements judgeModelStore for loadJudgeModel tests.
type fakeSettingStore struct {
	val string
	ok  bool
	err error
}

type contextSettingStore struct {
	got context.Context
}

func (s *contextSettingStore) GetAutoApprove(context.Context, string, string) (bool, bool, error) {
	return false, false, nil
}
func (s *contextSettingStore) GetJudgeDelayMs(context.Context) (int64, error) { return 0, nil }
func (s *contextSettingStore) GetPromptSections(context.Context) ([]state.PromptSection, error) {
	return nil, nil
}
func (s *contextSettingStore) GetSetting(ctx context.Context, _ string) (string, bool, error) {
	s.got = ctx
	return "anthropic/test", true, nil
}
func (s *contextSettingStore) RecordApprovedPermission(context.Context, string, string, state.ApprovedPermission) error {
	return nil
}

func (f fakeSettingStore) GetSetting(context.Context, string) (string, bool, error) {
	return f.val, f.ok, f.err
}

func TestLoadJudgeModel(t *testing.T) {
	tests := []struct {
		name         string
		store        judgeModelStore
		wantProvider string
		wantModel    string
		wantOK       bool
	}{
		{"nil store", nil, "", "", false},
		{"unset", fakeSettingStore{ok: false}, "", "", false},
		{"error", fakeSettingStore{val: "a/b", ok: true, err: errors.New("x")}, "", "", false},
		{"valid", fakeSettingStore{val: "anthropic/claude-haiku-4-5", ok: true}, "anthropic", "claude-haiku-4-5", true},
		{"model with slash", fakeSettingStore{val: "openrouter/anthropic/claude", ok: true}, "openrouter", "anthropic/claude", true},
		{"no slash", fakeSettingStore{val: "bogus", ok: true}, "", "", false},
		{"leading slash", fakeSettingStore{val: "/model", ok: true}, "", "", false},
		{"trailing slash", fakeSettingStore{val: "provider/", ok: true}, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, model, ok := loadJudgeModel(t.Context(), tt.store)
			if ok != tt.wantOK || provider != tt.wantProvider || model != tt.wantModel {
				t.Fatalf("loadJudgeModel = (%q, %q, %v), want (%q, %q, %v)",
					provider, model, ok, tt.wantProvider, tt.wantModel, tt.wantOK)
			}
		})
	}
}

func TestReloadJudgeModelUsesCallerContext(t *testing.T) {
	store := &contextSettingStore{}
	svc := NewService(Deps{Store: store})
	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "request")

	svc.ReloadJudgeModel(ctx)

	if got := store.got.Value(contextKey{}); got != "request" {
		t.Fatalf("context value = %v, want request", got)
	}
}
