package claudeprovider

import (
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// sdkSystem tracks the SDK's delegated-task ledger and authoritative run state.
// Like Claude Code's own session runner, every live background task except a
// teammate holds the turn, shells and monitors included: their completion
// wakes Claude for a follow-up turn, so the session is not done until then.
// An interrupt kills them, so one that never ends is ended by Stop.
func (l *live) sdkSystem(p *proc, raw []byte) {
	var event struct {
		Subtype  string `json:"subtype"`
		State    string `json:"state"`
		TaskID   string `json:"task_id"`
		TaskType string `json:"task_type"`
		Patch    struct {
			Status string `json:"status"`
		} `json:"patch"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	l.mu.Lock()
	previousCount := len(p.tasks)
	wake := false
	switch event.Subtype {
	case "session_state_changed":
		switch event.State {
		case "idle", "running", "requires_action":
			p.state = event.State
			wake = event.State != "idle" && !p.busy
		}
	case "task_started":
		if event.TaskID != "" && event.TaskType != "in_process_teammate" {
			if p.tasks == nil {
				p.tasks = map[string]bool{}
			}
			p.tasks[event.TaskID] = true
		}
	case "task_notification":
		delete(p.tasks, event.TaskID)
	case "task_updated":
		switch event.Patch.Status {
		case "completed", "failed", "killed", "stopped":
			delete(p.tasks, event.TaskID)
		}
	}
	l.armRunCeilingLocked(p)
	count := len(p.tasks)
	l.mu.Unlock()
	if count != previousCount {
		l.publishBackgroundCount(p, count)
	}
	if wake {
		l.resumeRun(p)
	}
	l.finishDeferredResult(p)
}

func (l *live) finishDeferredResult(p *proc) {
	l.mu.Lock()
	var raw []byte
	gen := p.deferredGen
	if len(p.pending) == 0 && len(p.deferredResult) > 0 &&
		len(p.tasks) == 0 && (p.state == "" || p.state == "idle") {
		raw, p.deferredResult = p.deferredResult, nil
	}
	l.mu.Unlock()
	if raw != nil {
		l.resultFor(p, raw, &gen)
	}
}

// A background completion can wake Claude without a new user prompt.
func (l *live) resumeRun(p *proc) {
	l.mu.Lock()
	p.deferredResult = nil // assistant activity means another result is now owed
	l.stopRunCeilingLocked(p)
	wake := !p.busy
	l.mu.Unlock()
	if !wake {
		return
	}
	props, _ := json.Marshal(map[string]any{"sessionID": p.id, "status": map[string]string{"type": "busy"}})
	var gen uint64
	_, err := l.sup.PublishIf(func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		gen = l.accepted[p.id]
		return l.procs[p.id] == p && !p.busy
	}, func(providerprotocol.Event) {
		l.mu.Lock()
		defer l.mu.Unlock()
		p.busy = true
		if p.idle != nil {
			p.idle.Stop()
		}
		l.accepted[p.id]++
		l.status[p.id] = db.Session{Status: db.StatusBusy}
	}, providerprotocol.Event{Type: "session.status", Properties: props})
	if err != nil {
		l.mu.Lock()
		if l.procs[p.id] == p && l.accepted[p.id] == gen {
			p.busy = true // Claude resumed even though its busy event could not be saved
		}
		l.mu.Unlock()
		l.turnFailed(p.id, err, gen)
	}
}

func runEndCeiling() time.Duration {
	ms, err := strconv.ParseInt(os.Getenv("CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS"), 10, 64)
	if err != nil || ms < 0 {
		return 10 * time.Minute
	}
	return time.Duration(min(ms, int64(1<<31-1))) * time.Millisecond
}

func (l *live) stopRunCeilingLocked(p *proc) {
	if p.runCeiling != nil {
		p.runCeiling.Stop()
		p.runCeiling = nil
	}
}

// Match the SDK's bounded wait between turns. A live task or permission wait
// never spends this budget; zero explicitly disables the ceiling.
func (l *live) armRunCeilingLocked(p *proc) {
	l.stopRunCeilingLocked(p)
	if len(p.deferredResult) == 0 || len(p.pending) > 0 || len(p.tasks) > 0 || p.state != "running" {
		return
	}
	ceiling := runEndCeiling()
	if ceiling == 0 {
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(ceiling, func() {
		l.mu.Lock()
		current := p.runCeiling == timer
		if current {
			p.runCeiling, p.state = nil, ""
		}
		l.mu.Unlock()
		if current {
			l.finishDeferredResult(p)
		}
	})
	p.runCeiling = timer
}
