package autoapprove

import (
	"sync"
	"testing"
)

// TestJudgeModelConcurrentAccess reproduces the unsynchronised judge
// model fields. The judge is process-wide and shared: every background
// auto-approve goroutine re-applies the persisted setting while
// sendPrompt reads it and the ReloadJudgeModel HTTP handler writes it.
// Run under -race.
func TestJudgeModelConcurrentAccess(t *testing.T) {
	j := &PermissionJudge{modelProvider: judgeModelProvider, modelID: judgeModelID}

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				if i%2 == 0 {
					j.setModel("anthropic", "claude-opus-5")
				} else {
					provider, model := j.model()
					if provider == "" || model == "" {
						t.Error("model() returned an empty pair")
						return
					}
				}
			}
		}()
	}
	wg.Wait()

	provider, model := j.model()
	if provider != "anthropic" || model != "claude-opus-5" {
		t.Errorf("model() = (%q, %q), want (anthropic, claude-opus-5)", provider, model)
	}
}

// TestJudgeModelNilReceiver covers the nil guards on the accessors, so
// a Service without a judge doesn't panic.
func TestJudgeModelNilReceiver(t *testing.T) {
	var j *PermissionJudge
	j.setModel("x", "y") // must not panic
	if provider, model := j.model(); provider != "" || model != "" {
		t.Errorf("nil judge model() = (%q, %q), want empty", provider, model)
	}
}

// TestJudgeDelayMsConcurrentAccess reproduces the judgeDelayMs race:
// written from the settings HTTP handler, read from the watcher and SSE
// tee goroutines. Run under -race.
func TestJudgeDelayMsConcurrentAccess(t *testing.T) {
	s := &Service{}

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				if i%2 == 0 {
					s.SetJudgeDelayMs(3000)
				} else if got := s.JudgeDelayMs(); got != 0 && got != 3000 {
					t.Errorf("JudgeDelayMs() = %d, want 0 or 3000", got)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := s.JudgeDelayMs(); got != 3000 {
		t.Errorf("JudgeDelayMs() = %d, want 3000", got)
	}
}
