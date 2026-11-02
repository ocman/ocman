package codexprovider

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providersupervisor"
)

const rpcTimeout = 30 * time.Second

var reply = providersupervisor.Replies{}.Reply

// All execution routes belong to ACP. The native client only serves history,
// catalogs and metadata; it has no route that can load or prompt a writer.
func (b *backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if b.acp.serve(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
		defer cancel()
		b.serveRead(ctx, w, r)
		return
	}
	if kind, rest, ok := promptRoute(r.URL.Path); ok {
		reply(w, nil, b.servePrompt(r, kind, rest))
		return
	}
	reply(w, nil, platforms.ErrUnsupported)
}

func promptRoute(path string) (kind, rest string, ok bool) {
	for _, kind := range []string{"permissions", "questions"} {
		if rest, ok := strings.CutPrefix(path, "/"+kind+"/"); ok {
			return kind, rest, true
		}
	}
	return "", "", false
}

func (b *backend) servePrompt(r *http.Request, kind, rest string) error {
	id, action, _ := strings.Cut(rest, "/")
	switch {
	case kind == "permissions" && action == "reply":
		var req platforms.RespondPermissionRequest
		if err := providersupervisor.Decode(r, &req); err != nil {
			return err
		}
		return b.replyPermission(id, req.Reply)
	case kind == "questions" && action == "reply":
		var req platforms.RespondQuestionRequest
		if err := providersupervisor.Decode(r, &req); err != nil {
			return err
		}
		return b.replyQuestion(id, req.Answers)
	case kind == "questions" && action == "reject":
		return b.rejectQuestion(id)
	}
	return platforms.ErrUnsupported
}
