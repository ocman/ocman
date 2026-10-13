package httpx

import (
	"errors"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
)

// WriteCreateError maps CreateWorktreeSession failures to HTTP.
func WriteCreateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, git.ErrNotARepo):
		http.Error(w, "projectDir is not a git repository", http.StatusNotFound)
	case errors.Is(err, git.ErrBranchCheckedOutElsewhere), errors.Is(err, git.ErrPathConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		log.WithError(err).Warn("worktree: create and launch")
		http.Error(w, "worktree create/launch failed: "+err.Error(), http.StatusBadGateway)
	}
}
