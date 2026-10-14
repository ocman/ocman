package claudeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// statusError carries the protocol status for an explicit, user-facing error.
type statusError struct {
	status int
	err    error
}

func (e statusError) Error() string { return e.err.Error() }
func (e statusError) Unwrap() error { return e.err }

func errStatus(err error) int {
	var se statusError
	switch {
	case errors.As(err, &se):
		return se.status
	case errors.Is(err, platforms.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, platforms.ErrUnsupported):
		return http.StatusNotImplemented
	case errors.Is(err, errReadOnly):
		return http.StatusForbidden
	}
	return http.StatusInternalServerError
}

// serve handles the mutating protocol operations. Shell is not offered: the
// CLI has no shell input in stream-json mode (spike §4).
func (l *live) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/permissions/") || strings.HasPrefix(r.URL.Path, "/questions/") {
		l.servePrompt(w, r)
		return
	}
	var v any
	var err error
	if r.URL.Path == "/hooks" {
		var req providerprotocol.HookEvent
		if err = decode(r, &req); err == nil {
			err = l.hook(req)
		}
		reply(w, nil, err)
		return
	}
	if r.URL.Path == "/sessions" {
		var req platforms.CreateSessionRequest
		if err = decode(r, &req); err == nil {
			v, err = l.create(req)
		}
		reply(w, v, err)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/sessions/")
	i := strings.LastIndexByte(rest, '/')
	if !ok || i <= 0 {
		reply(w, nil, platforms.ErrUnsupported)
		return
	}
	id, op := rest[:i], rest[i+1:]
	switch op {
	case "send":
		var req platforms.SendMessageRequest
		if err = decode(r, &req); err == nil {
			err = l.turn(r.Context(), id, req.Message, req.Images, req.Model, req.Reasoning)
		}
	case "command":
		var req platforms.ExecuteCommandRequest
		if err = decode(r, &req); err == nil {
			err = l.turn(r.Context(), id, strings.TrimSpace("/"+req.Command+" "+req.Arguments), nil, req.Model, req.Reasoning)
		}
	case "compact":
		err = l.turn(r.Context(), id, "/compact", nil, "", "")
	case "abort":
		err = l.abort(r.Context(), id)
	case "rename":
		var req platforms.RenameSessionRequest
		if err = decode(r, &req); err == nil {
			err = l.rename(r.Context(), id, req.Title)
		}
	case "auto-title":
		var req platforms.RenameSessionRequest
		if err = decode(r, &req); err == nil {
			err = l.renameWithCondition(r.Context(), id, req.Title, true)
		}
	case "fork":
		var req platforms.ForkSessionRequest
		if err = decode(r, &req); err == nil {
			v, err = l.fork(r.Context(), id, req.MessageID)
		}
	default:
		err = platforms.ErrUnsupported
	}
	reply(w, v, err)
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<20)).Decode(v); err != nil {
		return statusError{http.StatusBadRequest, fmt.Errorf("invalid request: %w", err)}
	}
	return nil
}

func reply(w http.ResponseWriter, v any, err error) {
	switch {
	case errors.Is(err, platforms.ErrReplyUnrecorded):
		writeJSON(w, errStatus(err), providerprotocol.Error{Code: providerprotocol.CodeReplyUnrecorded, Message: err.Error()})
	case err != nil:
		writeJSON(w, errStatus(err), providerprotocol.Error{Message: err.Error()})
	case v == nil:
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusOK, v)
	}
}

func (l *live) usable() error {
	if l.versionErr != nil {
		return statusError{http.StatusBadRequest, l.versionErr}
	}
	return nil
}

func (l *live) create(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	if err := l.usable(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(req.Directory) {
		return nil, statusError{http.StatusBadRequest, errors.New("directory must be absolute")}
	}
	id := uuid.NewString() // claude's --session-id, so ocman and claude share the id
	l.mu.Lock()
	err := l.updateOwnedLocked(func() {
		l.owned[id] = &owned{Directory: req.Directory, ProjectRoot: l.opts.ProjectRoot, Title: req.Title, Created: now(), Config: l.opts.ConfigDir}
	})
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &platforms.CreateSessionResponse{ID: id}, nil
}

// fork copies the transcript into a new session on its first send
// (--resume <src> --fork-session --session-id <new>).
func (l *live) fork(ctx context.Context, src, messageID string) (*platforms.CreateSessionResponse, error) {
	if err := l.usable(); err != nil {
		return nil, err
	}
	if messageID != "" {
		return nil, statusError{http.StatusNotImplemented, errors.New("claude sessions fork from the latest message only")}
	}
	o, isOwned := l.ownedSession(src)
	if isOwned && l.foreign(&o) {
		return nil, l.foreignError(o)
	}
	e, err := l.idx.get(ctx, src)
	if err != nil {
		return nil, err
	}
	switch {
	case e != nil:
		o.Directory = e.Directory
	case !isOwned:
		return nil, platforms.ErrNotFound
	case !o.Started:
		return nil, statusError{http.StatusConflict, errors.New("session has no messages to fork yet")}
	}
	id := uuid.NewString()
	root := l.opts.ProjectRoot
	if root == "" {
		root = o.ProjectRoot
	}
	l.mu.Lock()
	err = l.updateOwnedLocked(func() {
		l.owned[id] = &owned{Directory: o.Directory, ProjectRoot: root, Title: o.Title, Created: now(), ForkOf: src, Config: l.opts.ConfigDir}
	})
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &platforms.CreateSessionResponse{ID: id}, nil
}

// rename sets the title ocman shows and records it as Claude's own
// custom-title line, which the index and the CLI read. The line is appended
// only while no child is writing the transcript; until then (or until the
// transcript exists) it stays pending and the owned title is shown.
func (l *live) rename(ctx context.Context, id, title string) error {
	return l.renameWithCondition(ctx, id, title, false)
}

func (l *live) renameWithCondition(ctx context.Context, id, title string, onlyIfUntitled bool) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return statusError{http.StatusBadRequest, errors.New("title is required")}
	}
	l.mu.Lock()
	if o := l.owned[id]; onlyIfUntitled && o != nil && o.Title != "" {
		l.mu.Unlock()
		return nil
	}
	var o *owned
	err := l.updateOwnedLocked(func() {
		if o = l.owned[id]; o != nil && (!onlyIfUntitled || o.Title == "") {
			o.Title, o.TitlePending = title, true
		}
	})
	l.mu.Unlock()
	if err != nil {
		return err
	}
	if o == nil {
		return errReadOnly
	}
	return l.flushTitle(ctx, id)
}

// flushTitle appends a pending rename between turns. ponytail: a send landing
// between the busy check and the append can still interleave one line with
// the CLI's own appends; both are single O_APPEND writes.
func (l *live) flushTitle(ctx context.Context, id string) error {
	l.mu.Lock()
	o, p := l.owned[id], l.procs[id]
	if o == nil || !o.TitlePending || (p != nil && p.busy) {
		l.mu.Unlock()
		return nil
	}
	title := o.Title
	l.mu.Unlock()
	e, err := l.idx.get(ctx, id)
	if err != nil || e == nil {
		return err // no transcript yet: flushed after a later turn
	}
	line, _ := json.Marshal(map[string]string{"type": "custom-title", "customTitle": title, "sessionId": id})
	f, err := os.OpenFile(e.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		l.mu.Lock()
		err = l.updateOwnedLocked(func() {
			if o := l.owned[id]; o != nil && o.Title == title {
				o.TitlePending = false
			}
		})
		l.mu.Unlock()
	}
	return err
}

// turn starts a turn: BeginTurn synchronously, then the child is acquired
// (which may wait for a pool slot) and the input written.
func (l *live) turn(ctx context.Context, id, text string, images []platforms.ImageAttachment, model, effort string) error {
	if err := l.usable(); err != nil {
		return err
	}
	o, ok := l.ownedSession(id)
	if !ok {
		if e, err := l.idx.get(ctx, id); err != nil || e == nil {
			return platforms.ErrNotFound
		}
		return errReadOnly
	}
	if l.foreign(&o) {
		return l.foreignError(o)
	}
	input := uuid.NewString() // echoed by --replay-user-messages when the CLI starts it
	w := &waitTicket{input: input}
	done := make(chan struct{})
	var prev chan struct{}
	err := l.sup.BeginTurn(id, func() {
		l.mu.Lock()
		l.clearViewLocked(id)
		l.status[id] = db.Session{Status: db.StatusBusy}
		delete(l.deferred, id)
		if l.waits[id] == nil {
			l.waits[id] = map[*waitTicket]bool{}
		}
		l.waits[id][w] = true
		l.accepted[id]++
		prev, l.tails[id] = l.tails[id], done
		l.mu.Unlock()
	})
	if err != nil {
		return statusError{http.StatusConflict, err}
	}
	msg := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": userContent(text, images)},
		"parent_tool_use_id": nil, "session_id": "", "uuid": input}
	// "provider/model" from the composer; claude takes the model alone.
	model = model[strings.IndexByte(model, '/')+1:]
	go func() {
		// Inputs reach the child in send order, one turn after another.
		if prev != nil {
			<-prev
		}
		defer close(done)
		testHook("acquire", id)
		p, err := l.acquire(id, model, effort, input, w)
		if err == nil {
			err = l.initializeProc(p)
			if err == nil {
				err = l.changeModel(p, model)
			}
			if err == nil {
				// The cancellation check and the write share the stdin lock
				// with an interrupt, so an abort either cancels this input or
				// sees it written ahead of the interrupt.
				p.writeMu.Lock()
				l.mu.Lock()
				if w.cancelled {
					err = errAborted
				} else {
					w.written = true
				}
				l.mu.Unlock()
				if err == nil {
					err = l.writeLocked(p, msg)
				}
				p.writeMu.Unlock()
			}
			if err != nil {
				l.mu.Lock()
				if errors.Is(err, errAborted) && p.pending[input] {
					// Joined a running turn: the interrupt's result settles it.
					delete(p.pending, input)
					l.mu.Unlock()
					l.finishDeferredResult(p)
				} else {
					p.busy = false
					l.stopLocked(p)
					l.mu.Unlock()
				}
			} else {
				testHook("written", id)
			}
		}
		l.mu.Lock()
		delete(l.waits[id], w)
		running := l.procs[id] != nil && l.procs[id].busy
		last := len(l.waits[id]) == 0
		gen := l.accepted[id]
		settled, wasSettled := l.deferred[id]
		if last && !running {
			delete(l.deferred, id)
		}
		l.mu.Unlock()
		switch {
		case errors.Is(err, errAborted):
			if last && !running { // nothing else will end this turn
				l.settle(id, db.StatusDone, gen)
			}
		case err == nil:
			if last && !running && wasSettled { // the result arrived before this cleanup
				l.settle(id, settled, gen)
			}
		case err != nil:
			l.fail(id, err, gen)
		}
	}()
	return nil
}

// userContent builds stream-json content blocks; images arrive as data URLs.
func userContent(text string, images []platforms.ImageAttachment) []map[string]any {
	var blocks []map[string]any
	for _, img := range images {
		meta, data, ok := strings.Cut(strings.TrimPrefix(img.URL, "data:"), ",")
		if !ok || !strings.HasPrefix(img.URL, "data:") || !strings.HasSuffix(meta, ";base64") {
			continue
		}
		blocks = append(blocks, map[string]any{"type": "image",
			"source": map[string]any{"type": "base64", "media_type": strings.TrimSuffix(meta, ";base64"), "data": data}})
	}
	if text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	return blocks
}
