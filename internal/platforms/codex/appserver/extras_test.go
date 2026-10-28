package codexappserver_test

import (
	"encoding/json"
	"errors"
	"testing"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

func TestExtraMethods(t *testing.T) {
	c, s := attach(t, codexappserver.Options{})
	s.Handle("thread/shellCommand", func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	s.Handle("review/start", func(json.RawMessage) (any, error) {
		return map[string]any{"turn": map[string]any{"id": "rev", "status": "inProgress", "items": []any{}}}, nil
	})
	s.Handle("skills/list", func(json.RawMessage) (any, error) {
		return map[string]any{"data": []any{map[string]any{"cwd": "/r", "skills": []any{
			map[string]any{"name": "pdf", "description": "PDF tools", "path": "/s/pdf/SKILL.md", "scope": "user", "enabled": true}}}}}, nil
	})
	s.Handle("account/read", func(json.RawMessage) (any, error) {
		return map[string]any{"account": map[string]any{"type": "chatgpt", "email": "x", "planType": "pro"}, "requiresOpenaiAuth": true}, nil
	})
	s.Handle("account/rateLimits/read", func(json.RawMessage) (any, error) {
		return map[string]any{"rateLimits": map[string]any{"limitId": "codex", "primary": map[string]any{"usedPercent": 3, "windowDurationMins": 10080, "resetsAt": 1793602742}, "secondary": nil}}, nil
	})
	if err := c.ShellCommand(ctx(t), "thr", "echo hi"); err != nil {
		t.Fatal(err)
	}
	turn, err := c.StartReview(ctx(t), codexappserver.ReviewStartParams{ThreadID: "thr", Target: codexappserver.ReviewTarget{Type: "uncommittedChanges"}})
	if err != nil || turn.ID != "rev" {
		t.Fatalf("review = %+v %v", turn, err)
	}
	skills, err := c.ListSkills(ctx(t), "/r")
	if err != nil || len(skills) != 1 || skills[0].Skills[0].Path != "/s/pdf/SKILL.md" || !skills[0].Skills[0].Enabled {
		t.Fatalf("skills = %+v %v", skills, err)
	}
	account, err := c.ReadAccount(ctx(t))
	if err != nil || account.Account == nil || account.Account.PlanType != "pro" {
		t.Fatalf("account = %+v %v", account, err)
	}
	limits, err := c.ReadRateLimits(ctx(t))
	if err != nil || limits.Primary.UsedPercent != 3 || *limits.Primary.WindowDurationMins != 10080 || limits.Secondary != nil {
		t.Fatalf("limits = %+v %v", limits, err)
	}
	want := map[string]string{
		"thread/shellCommand": `{"threadId":"thr","command":"echo hi"}`,
		"review/start":        `{"threadId":"thr","target":{"type":"uncommittedChanges"}}`,
		"skills/list":         `{"cwds":["/r"]}`,
		"account/read":        `{}`,
	}
	for _, call := range s.Calls() {
		if w, ok := want[call.Method]; ok && string(call.Params) != w {
			t.Errorf("%s params = %s, want %s", call.Method, call.Params, w)
		}
	}

	for _, m := range []string{"review/start", "skills/list", "account/rateLimits/read"} {
		s.Handle(m, func(json.RawMessage) (any, error) { return nil, errors.New("down") })
	}
	if _, err := c.StartReview(ctx(t), codexappserver.ReviewStartParams{}); err == nil {
		t.Error("review error ignored")
	}
	if _, err := c.ListSkills(ctx(t)); err == nil {
		t.Error("skills error ignored")
	}
	if _, err := c.ReadRateLimits(ctx(t)); err == nil {
		t.Error("rate limits error ignored")
	}
}

func TestForkExcludesTurns(t *testing.T) {
	data, _ := json.Marshal(codexappserver.ThreadForkParams{ThreadID: "t", LastTurnID: "u", ExcludeTurns: true})
	if string(data) != `{"threadId":"t","lastTurnId":"u","excludeTurns":true}` {
		t.Fatalf("fork params = %s", data)
	}
}
