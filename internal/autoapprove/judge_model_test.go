package autoapprove

import (
	"errors"
	"testing"
)

// fakeSettingStore implements judgeModelStore for loadJudgeModel tests.
type fakeSettingStore struct {
	val string
	ok  bool
	err error
}

func (f fakeSettingStore) GetSetting(string) (string, bool, error) {
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
			provider, model, ok := loadJudgeModel(tt.store)
			if ok != tt.wantOK || provider != tt.wantProvider || model != tt.wantModel {
				t.Fatalf("loadJudgeModel = (%q, %q, %v), want (%q, %q, %v)",
					provider, model, ok, tt.wantProvider, tt.wantModel, tt.wantOK)
			}
		})
	}
}
