package server

import (
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
)

func quota429(provider string) string {
	return `{"role":"assistant","providerID":"` + provider + `","error":{"name":"APIError","data":{"statusCode":429,"responseHeaders":{"retry-after":"3600"}}}}`
}

// dieOnQuota ends s1's turn on a 429 from provider and waits for the edge.
func (r *cooldownRig) dieOnQuota(t *testing.T, provider string) {
	t.Helper()
	r.mu.Lock()
	r.last = quota429(provider)
	r.mu.Unlock()
	r.srv.onSessionIdle("opencode", "s1")
	if err := r.srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (r *cooldownRig) notice() *db.SessionNotice {
	sess := db.Session{ID: "s1"}
	r.srv.applyFallNotice("opencode", &sess)
	return sess.Notice
}

func TestContinuationWalksListThenStops(t *testing.T) {
	r := newCooldownRigWith(t, state.ProjectSettings{Models: []string{"a/x", "b/y", "c/z"}})
	for _, step := range []struct{ dies, next string }{{"a", "b/y"}, {"b", "c/z"}} {
		r.dieOnQuota(t, step.dies)
		r.mu.Lock()
		sent, msg := r.sent[len(r.sent)-1], r.msgs[len(r.msgs)-1]
		r.mu.Unlock()
		if sent != step.next || msg != continuationPrompt {
			t.Fatalf("after %s died: sent %q %q", step.dies, sent, msg)
		}
		if n := r.notice(); n == nil || n.Kind != "model_switch" || !strings.Contains(n.Message, step.next) {
			t.Fatalf("switch notice = %+v", n)
		}
	}
	r.dieOnQuota(t, "c")
	if len(r.sent) != 2 {
		t.Fatalf("sent after exhaustion = %v, want one attempt per remaining model", r.sent)
	}
	n := r.notice()
	if n == nil || n.Kind != "models_exhausted" {
		t.Fatalf("exhausted notice = %+v", n)
	}
	// Every cooldown here is the one-hour retry-after: the earliest is a's.
	if at := time.UnixMilli(n.RetryAt); time.Until(at) < 55*time.Minute || time.Until(at) > time.Hour {
		t.Fatalf("earliest reset = %v", at)
	}
}

func TestContinuationPromptInspectsWorkingTree(t *testing.T) {
	for _, want := range []string{"interrupted before it finished", "inspect the current state of the working tree", "Before redoing any work", "complete the task"} {
		if !strings.Contains(continuationPrompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func TestContinuationDisabled(t *testing.T) {
	for name, ps := range map[string]state.ProjectSettings{
		"off":     {Models: []string{"a/x", "b/y"}, Off: true},
		"no list": {},
	} {
		t.Run(name, func(t *testing.T) {
			r := newCooldownRigWith(t, ps)
			r.dieOnQuota(t, "a")
			r.srv.onSessionRetry("s1", autoapprove.SessionStatus{Type: "retry", ActionReason: "free_tier_limit"})
			if len(r.sent) != 0 || r.aborts != 0 || r.notice() != nil {
				t.Fatalf("sent %v, aborts %d, notice %+v", r.sent, r.aborts, r.notice())
			}
		})
	}
}

// A turn parked on a quota retry is aborted once, and its idle edge
// continues on the next model.
func TestParkedRetryAbortsThenContinues(t *testing.T) {
	r := newCooldownRig(t)
	r.last = runningAssistant
	for range 2 {
		r.srv.onSessionRetry("s1", autoapprove.SessionStatus{Type: "retry", ActionReason: "free_tier_limit"})
	}
	if r.aborts != 1 {
		t.Fatalf("aborts = %d, want 1", r.aborts)
	}
	r.last = `{"role":"assistant","providerID":"a","error":{"name":"MessageAbortedError","data":{}}}`
	r.srv.onSessionIdle("opencode", "s1")
	if err := r.srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.sent) != 1 || r.sent[0] != "b/y" || r.msgs[0] != continuationPrompt {
		t.Fatalf("sent %v %q", r.sent, r.msgs)
	}
}

// A user abort, with no parked retry behind it, is never continued.
func TestUserAbortIsNotContinued(t *testing.T) {
	r := newCooldownRig(t)
	r.last = `{"role":"assistant","providerID":"a","error":{"name":"MessageAbortedError","data":{}}}`
	r.srv.onSessionIdle("opencode", "s1")
	if err := r.srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.sent) != 0 {
		t.Fatalf("sent %v", r.sent)
	}
}
