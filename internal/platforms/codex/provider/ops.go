package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// rpcTimeout bounds one app-server request made for a protocol operation.
const rpcTimeout = 30 * time.Second

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
	case errors.Is(err, platforms.ErrBusy):
		return http.StatusConflict
	case errors.Is(err, platforms.ErrUnsupported):
		return http.StatusNotImplemented
	}
	return http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func reply(w http.ResponseWriter, v any, err error) {
	switch {
	case errors.Is(err, platforms.ErrCreationRejected):
		writeJSON(w, errStatus(err), providerprotocol.Error{Code: providerprotocol.CodeCreationRejected, Message: err.Error()})
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

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<20)).Decode(v); err != nil {
		return statusError{http.StatusBadRequest, fmt.Errorf("invalid request: %w", err)}
	}
	return nil
}

// ServeHTTP handles every operation the supervisor does not answer itself.
func (b *backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if r.Method == http.MethodGet {
		b.serveRead(ctx, w, r)
		return
	}
	if r.URL.Path == "/sessions" {
		var req platforms.CreateSessionRequest
		var v any
		err := decode(r, &req)
		if err == nil {
			v, err = b.create(ctx, req)
		}
		reply(w, v, err)
		return
	}
	if kind, rest, ok := promptRoute(r.URL.Path); ok {
		reply(w, nil, b.servePrompt(r, kind, rest))
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/sessions/")
	i := strings.LastIndexByte(rest, '/')
	if !ok || i <= 0 {
		reply(w, nil, platforms.ErrUnsupported)
		return
	}
	id, op := rest[:i], rest[i+1:]
	var err error
	switch op {
	case "send":
		var req platforms.SendMessageRequest
		if err = decode(r, &req); err == nil {
			err = b.send(ctx, id, req)
		}
	case "abort":
		err = b.abort(ctx, id)
	case "rename":
		var req platforms.RenameSessionRequest
		if err = decode(r, &req); err == nil {
			err = b.rename(ctx, id, req.Title)
		}
	case "compact":
		err = b.compact(ctx, id)
	case "shell":
		var req platforms.RunShellRequest
		if err = decode(r, &req); err == nil {
			err = b.shell(ctx, id, req.Command)
		}
	case "command":
		var req platforms.ExecuteCommandRequest
		if err = decode(r, &req); err == nil {
			err = b.command(ctx, id, req)
		}
	case "fork":
		var req platforms.ForkSessionRequest
		var v any
		if err = decode(r, &req); err == nil {
			v, err = b.fork(ctx, id, req.MessageID)
		}
		reply(w, v, err)
		return
	default:
		err = platforms.ErrUnsupported
	}
	reply(w, nil, err)
}

func promptRoute(path string) (kind, rest string, ok bool) {
	for _, k := range []string{"permissions", "questions"} {
		if rest, ok := strings.CutPrefix(path, "/"+k+"/"); ok {
			return k, rest, true
		}
	}
	return "", "", false
}

func (b *backend) servePrompt(r *http.Request, kind, rest string) error {
	id, action, _ := strings.Cut(rest, "/")
	switch {
	case kind == "permissions" && action == "reply":
		var req platforms.RespondPermissionRequest
		if err := decode(r, &req); err != nil {
			return err
		}
		return b.replyPermission(id, req.Reply)
	case kind == "questions" && action == "reply":
		var req platforms.RespondQuestionRequest
		if err := decode(r, &req); err != nil {
			return err
		}
		return b.replyQuestion(id, req.Answers)
	case kind == "questions" && action == "reject":
		return b.rejectQuestion(id)
	}
	return platforms.ErrUnsupported
}

// load makes a stored thread live on connection gen, resuming it once.
func (b *backend) load(ctx context.Context, c *codexappserver.Client, gen uint64, id string) error {
	b.mu.Lock()
	loaded := b.gen == gen && b.client == c && b.loaded[id]
	b.mu.Unlock()
	if loaded {
		return nil
	}
	resp, err := c.ResumeThread(ctx, codexappserver.ThreadResumeParams{ThreadID: id, ExcludeTurns: true})
	if err != nil {
		return notFound(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.gen != gen || b.client != c || b.loaded == nil {
		return errors.New("codex app-server exited while resuming thread")
	}
	b.loaded[id] = true
	s := b.sessionLocked(id)
	s.dir, s.provider, s.model = resp.Cwd, resp.ModelProvider, resp.Model
	return nil
}

func (b *backend) create(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	if !filepath.IsAbs(req.Directory) {
		return nil, errors.Join(platforms.ErrCreationRejected, statusError{http.StatusBadRequest, errors.New("directory must be absolute")})
	}
	c, gen, err := b.conn(ctx)
	if err != nil {
		return nil, errors.Join(platforms.ErrCreationRejected, err)
	}
	resp, err := c.StartThread(ctx, codexappserver.ThreadStartParams{Cwd: req.Directory})
	if err != nil {
		return nil, err // the thread may exist: allocation is uncertain
	}
	id := resp.Thread.ID
	b.mu.Lock()
	if b.gen != gen || b.client != c || b.loaded == nil {
		b.mu.Unlock()
		return nil, errors.New("codex app-server exited after allocating thread " + id)
	}
	b.loaded[id] = true
	s := b.sessionLocked(id)
	s.dir, s.title = req.Directory, req.Title
	s.provider, s.model = resp.ModelProvider, resp.Model
	b.mu.Unlock()
	if req.Title != "" {
		if err := c.SetThreadName(ctx, id, req.Title); err != nil {
			return nil, err
		}
	}
	return &platforms.CreateSessionResponse{ID: id}, nil
}

// send starts a turn: BeginTurn synchronously, then turn/start. A turn that
// Codex refuses ends at once with its error. A send while a turn runs steers
// that turn instead (turn/steer); it is busy until Codex names the turn.
func (b *backend) send(ctx context.Context, id string, req platforms.SendMessageRequest) error {
	input := userInputs(req.Message, req.Images)
	if len(input) == 0 {
		return statusError{http.StatusBadRequest, errors.New("message is empty")}
	}
	b.mu.Lock()
	s, c := b.sessions[id], b.client
	busy, active := s != nil && s.running, ""
	if busy {
		active = s.turn
	}
	b.mu.Unlock()
	if busy {
		if active == "" || c == nil {
			return platforms.ErrBusy
		}
		if _, err := c.SteerTurn(ctx, codexappserver.TurnSteerParams{ThreadID: id, Input: input, ExpectedTurnID: active}); err != nil {
			return fmt.Errorf("%w: %w", platforms.ErrBusy, err)
		}
		return nil
	}
	return b.startTurn(ctx, id, input, req.Model, req.Reasoning)
}

// startTurn starts a model turn; model is "provider/model" from the composer.
func (b *backend) startTurn(ctx context.Context, id string, input []codexappserver.UserInput, model, effort string) error {
	modelID := model[strings.IndexByte(model, '/')+1:]
	return b.runTurn(ctx, id, model, func(c *codexappserver.Client) (string, error) {
		turn, err := c.StartTurn(ctx, codexappserver.TurnStartParams{ThreadID: id, Input: input, Model: modelID, Effort: effort})
		if err != nil {
			return "", err
		}
		return turn.ID, nil
	})
}

// runTurn admits a turn on a loaded thread (BeginTurn) and starts it with
// start, which returns Codex's turn id when its response names one: that id
// wins over turn/started, which may announce an inner turn (a review's).
// model is the composer's "provider/model", empty for the session's own. A
// start Codex rejects ends the admitted turn at once with its error.
func (b *backend) runTurn(ctx context.Context, id, model string, start func(*codexappserver.Client) (string, error)) error {
	b.mu.Lock()
	s := b.sessionLocked(id)
	b.mu.Unlock()
	if !s.sendMu.TryLock() {
		return platforms.ErrBusy
	}
	defer s.sendMu.Unlock()
	b.mu.Lock()
	busy := s.running
	b.mu.Unlock()
	if busy {
		return platforms.ErrBusy
	}
	c, gen, err := b.conn(ctx)
	if err != nil {
		return err
	}
	if err := b.load(ctx, c, gen, id); err != nil {
		return err
	}
	var accepted uint64
	// Serialize the ownership check and admission with connection retirement.
	b.startMu.Lock()
	b.mu.Lock()
	current := b.client == c && b.gen == gen && b.loaded != nil
	b.mu.Unlock()
	if !current {
		b.startMu.Unlock()
		return errors.New("codex app-server exited before turn admission")
	}
	err = b.sup.BeginTurn(id, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		s := b.sessionLocked(id)
		s.running, s.turn, s.view, s.base, s.used = true, "", nil, nil, nil
		s.status = db.StatusBusy
		provider, modelID := s.provider, s.model
		if model != "" {
			if p, m, ok := strings.Cut(model, "/"); ok {
				provider, modelID = p, m
			} else {
				modelID = model
			}
		}
		s.conv = &Converter{SessionID: id, ProviderID: provider, ModelID: modelID}
		s.accepted++
		accepted = s.accepted
	})
	b.startMu.Unlock()
	if err != nil {
		return statusError{http.StatusConflict, err}
	}
	turn, err := start(c)
	if err != nil {
		var rejection *codexappserver.RPCError
		if errors.As(err, &rejection) || errors.Is(err, codexappserver.ErrNotSent) {
			b.errorMessage(id, err.Error())
			b.settle(id, db.StatusError, accepted)
		}
		// After writer handoff, cancellation does not cancel Codex. Keep that
		// admission until completion or connection retirement. An unsent request
		// cannot produce a completion, so release only that definite failure.
		return err
	}
	b.mu.Lock()
	if s := b.sessions[id]; s.accepted == accepted && s.running && turn != "" {
		s.turn = turn
	}
	b.mu.Unlock()
	return nil
}

// userInputs builds turn input; images arrive as data URLs.
func userInputs(text string, images []platforms.ImageAttachment) []codexappserver.UserInput {
	var out []codexappserver.UserInput
	for _, img := range images {
		if strings.HasPrefix(img.URL, "data:") {
			out = append(out, codexappserver.UserInput{Type: "image", URL: img.URL})
		}
	}
	if strings.TrimSpace(text) != "" {
		out = append(out, codexappserver.TextInput(text))
	}
	return out
}

// abort interrupts the running turn; its turn/completed settles it.
func (b *backend) abort(ctx context.Context, id string) error {
	b.mu.Lock()
	s := b.sessions[id]
	running, turn, c := s != nil && s.running, "", b.client
	if running {
		turn = s.turn
	}
	b.mu.Unlock()
	switch {
	case !running:
		return nil
	case turn == "" || c == nil:
		return statusError{http.StatusConflict, errors.New("the turn is still starting; try again")}
	}
	return c.InterruptTurn(ctx, id, turn)
}

func (b *backend) rename(ctx context.Context, id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return statusError{http.StatusBadRequest, errors.New("title is required")}
	}
	c, gen, err := b.conn(ctx)
	if err != nil {
		return err
	}
	if err := b.load(ctx, c, gen, id); err != nil {
		return err
	}
	if err := c.SetThreadName(ctx, id, title); err != nil {
		return err
	}
	b.mu.Lock()
	b.sessionLocked(id).title = title
	b.mu.Unlock()
	return nil
}
