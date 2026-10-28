package claudeprovider

import (
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
)

// servePrompt handles POST /permissions/{id}/reply and /questions/{id}/reply|reject.
func (l *live) servePrompt(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 3 {
		reply(w, nil, platforms.ErrUnsupported)
		return
	}
	var err error
	switch parts[0] + "/" + parts[2] {
	case "permissions/reply":
		var req platforms.RespondPermissionRequest
		if err = decode(r, &req); err == nil {
			err = l.replyPermission(parts[1], req)
		}
	case "questions/reply":
		var req platforms.RespondQuestionRequest
		if err = decode(r, &req); err == nil {
			err = l.replyQuestion(parts[1], req.Answers)
		}
	case "questions/reject":
		err = l.rejectQuestion(parts[1])
	default:
		err = platforms.ErrUnsupported
	}
	reply(w, nil, err)
}
