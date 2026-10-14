package claudeprovider

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func (l *live) read(p *proc, stdout io.Reader) {
	streamErr := l.readMessages(p, stdout)
	if streamErr != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
	close(p.done)
	// A terminal result followed by process exit keeps its actual error text.
	// Missing accepted inputs or corrupt output are still transport failures.
	l.mu.Lock()
	l.stopRunCeilingLocked(p)
	var result struct {
		IsError bool   `json:"is_error"`
		Subtype string `json:"subtype"`
	}
	_ = json.Unmarshal(p.deferredResult, &result)
	terminalError := result.IsError || (result.Subtype != "" && result.Subtype != "success")
	if streamErr == nil && (p.cmd.ProcessState.Success() || terminalError) {
		p.state = ""
		clear(p.tasks)
		l.mu.Unlock()
		l.finishDeferredResult(p)
	} else {
		l.mu.Unlock()
	}
	l.cancel(p, "")
	l.mu.Lock()
	wasBusy := p.busy
	p.busy = false
	clear(p.pending)
	if l.procs[p.id] == p {
		delete(l.procs, p.id)
	}
	p.freeSlotLocked()
	l.children--
	gen := l.accepted[p.id]
	l.slots.Broadcast()
	l.mu.Unlock()
	if wasBusy {
		reason := or(p.stderr.String(), p.cmd.ProcessState.String())
		if streamErr != nil {
			reason = streamErr.Error()
		}
		l.fail(p.id, fmt.Errorf("claude exited during the turn: %s", reason), gen)
	}
}

func (l *live) readMessages(p *proc, stdout io.Reader) error {
	lines := bufio.NewScanner(stdout)
	lines.Buffer(nil, 32<<20)
	for lines.Scan() {
		raw := bytes.TrimSpace(lines.Bytes())
		if !bytes.HasPrefix(raw, []byte("{")) {
			continue // Claude can write non-JSON diagnostic lines to stdout.
		}
		if err := l.handle(p, raw); err != nil {
			return err
		}
	}
	return lines.Err()
}
