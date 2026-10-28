package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/git"
)

func TestWriteCreateError(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"not a repository", fmt.Errorf("wrapped: %w", git.ErrNotARepo), http.StatusNotFound, "projectDir is not a git repository\n"},
		{"branch conflict", fmt.Errorf("wrapped: %w", git.ErrBranchCheckedOutElsewhere), http.StatusConflict, "wrapped: " + git.ErrBranchCheckedOutElsewhere.Error() + "\n"},
		{"path conflict", git.ErrPathConflict, http.StatusConflict, git.ErrPathConflict.Error() + "\n"},
		{"launch failure", errors.New("launch failed"), http.StatusBadGateway, "worktree create/launch failed: launch failed\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteCreateError(w, tt.err)
			if w.Code != tt.status || w.Body.String() != tt.body {
				t.Fatalf("got %d %q, want %d %q", w.Code, w.Body.String(), tt.status, tt.body)
			}
		})
	}
}
