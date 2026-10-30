package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/platforms"
)

// serve intercepts execution only. History, catalog, account and spool routes
// retain the existing provider implementation and never resume a native writer.
func (a *acpExecution) serve(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		if strings.HasSuffix(r.URL.Path, "/ownership") {
			reply(w, nil, platforms.ErrUnsupported)
			return true
		}
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()
	if r.URL.Path == "/sessions" {
		var req platforms.CreateSessionRequest
		var response *platforms.CreateSessionResponse
		err := decode(r, &req)
		if err == nil {
			response, err = a.create(ctx, req)
		}
		reply(w, response, err)
		return true
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/sessions/")
	i := strings.LastIndexByte(rest, '/')
	if !ok || i <= 0 {
		return false // existing permission/question replies
	}
	id, op := rest[:i], rest[i+1:]
	var err error
	switch op {
	case "send":
		var req platforms.SendMessageRequest
		if err = decode(r, &req); err == nil {
			err = a.send(ctx, id, req)
		}
	case "abort":
		err = a.abort(ctx, id)
	case "compact":
		err = a.send(ctx, id, platforms.SendMessageRequest{Message: "/compact"})
	case "command":
		var req platforms.ExecuteCommandRequest
		if err = decode(r, &req); err == nil {
			message := "/" + strings.TrimPrefix(strings.TrimSpace(req.Command), "/")
			if req.Arguments != "" {
				message += " " + req.Arguments
			}
			err = a.send(ctx, id, platforms.SendMessageRequest{Message: message, Model: req.Model, Reasoning: req.Reasoning})
		}
	case "fork":
		var req platforms.ForkSessionRequest
		var response *platforms.CreateSessionResponse
		if err = decode(r, &req); err == nil {
			response, err = a.fork(ctx, id, req.MessageID)
		}
		reply(w, response, err)
		return true
	case "rename":
		var req platforms.RenameSessionRequest
		if err = decode(r, &req); err == nil {
			err = a.rename(ctx, id, req.Title)
		}
	default:
		err = platforms.ErrUnsupported // in particular: never send raw shell as a prompt
	}
	reply(w, nil, err)
	return true
}

func (a *acpExecution) fork(ctx context.Context, id, messageID string) (*platforms.CreateSessionResponse, error) {
	if messageID != "" {
		return nil, errors.Join(platforms.ErrCreationRejected, platforms.ErrUnsupported)
	}
	c, err := a.conn(ctx)
	if err != nil {
		return nil, errors.Join(platforms.ErrCreationRejected, err)
	}
	if err := a.resume(ctx, c, id); err != nil {
		return nil, errors.Join(platforms.ErrCreationRejected, err)
	}
	a.b.mu.Lock()
	dir := a.b.sessionLocked(id).dir
	a.b.mu.Unlock()
	var servers []acp.UnstableMcpServer
	raw, err := json.Marshal(a.servers())
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, err
	}
	response, err := c.ForkSession(ctx, acp.UnstableForkSessionRequest{SessionId: acp.SessionId(id), Cwd: dir, McpServers: servers})
	if err != nil {
		return nil, err
	}
	forkID := string(response.SessionId)
	if forkID == "" || forkID == id {
		return nil, errors.New("codex ACP fork returned an invalid session ID")
	}
	a.mu.Lock()
	a.loaded[forkID] = true
	a.mu.Unlock()
	a.b.mu.Lock()
	a.b.sessionLocked(forkID).dir = dir
	a.b.mu.Unlock()
	return &platforms.CreateSessionResponse{ID: forkID}, nil
}

func (a *acpExecution) rename(ctx context.Context, id, title string) error {
	if strings.TrimSpace(title) == "" {
		return statusError{http.StatusBadRequest, errors.New("title is required")}
	}
	c, _, err := a.b.conn(ctx)
	if err != nil {
		return err
	}
	// Setting metadata does not resume/load a writer. Keep native session titles
	// visible to CLI users and history ingestion rather than renaming only ACP UI.
	if err := c.SetThreadName(ctx, id, title); err != nil {
		return err
	}
	a.b.mu.Lock()
	a.b.sessionLocked(id).title = title
	a.b.mu.Unlock()
	return nil
}
