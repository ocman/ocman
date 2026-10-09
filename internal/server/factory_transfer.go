package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/forge"
)

// errFactoryTracker marks failures on the forge side of a transfer.
var errFactoryTracker = errors.New("issue tracker")

// transferFactoryIssue moves an open Epic ticket to the project's issue
// tracker: it opens a forge issue with the ticket's text, links it in a
// comment, and soft-deletes the ticket so the Epic no longer waits on it.
func (s *Server) transferFactoryIssue(ctx context.Context, epicID, issueID string) (forge.Issue, error) {
	epic, err := s.factory.GetWorkEpic(ctx, epicID)
	if err != nil {
		return forge.Issue{}, err
	}
	issues, err := s.factory.ListIssues(ctx, epicID)
	if err != nil {
		return forge.Issue{}, err
	}
	var issue *factory.Issue
	for i := range issues {
		if issues[i].ID == issueID {
			issue = &issues[i]
		}
	}
	if issue == nil || issue.Status != "open" || (issue.Kind != "task" && issue.Kind != "implementation") {
		return forge.Issue{}, fmt.Errorf("%w: only open task Issues can be transferred", factory.ErrInvalidRequest)
	}
	mutator, ok := s.factory.(interface {
		MutateGraph(context.Context, factory.GraphMutation) error
	})
	if !ok {
		return forge.Issue{}, factory.ErrFactoryUnavailable
	}
	project := issue.Project
	if project == "" {
		project = epic.InitialProject
	}
	upstreams, err := s.router().ForDir(project).ProjectUpstreams(ctx, project)
	if err != nil {
		return forge.Issue{}, fmt.Errorf("%w: resolve: %w", errFactoryTracker, err)
	}
	var creator forge.IssueCreator
	var repo string
	for _, remote := range upstreams.Remotes {
		client, ok := s.resolveForge(remote)
		if c, canCreate := client.(forge.IssueCreator); ok && canCreate && (creator == nil || remote.Name == "origin") {
			creator, repo = c, remote.Repo
		}
	}
	if creator == nil {
		return forge.Issue{}, fmt.Errorf("%w: no supported issue tracker for %s", factory.ErrInvalidRequest, project)
	}
	body := fmt.Sprintf("%s\n\n---\nTransferred from ocman Factory Epic %s (%s), Issue %s.", issue.Description, epic.ID, epic.Goal, issue.ID)
	created, err := creator.CreateIssue(ctx, repo, issue.Title, body)
	if err != nil {
		return forge.Issue{}, fmt.Errorf("%w: create issue: %w", errFactoryTracker, err)
	}
	// Comment first: the removed ticket keeps its link to the new home.
	if _, err := s.factory.AddIssueComment(ctx, epicID, issueID, "user", "Transferred to "+created.URL); err != nil {
		return created, fmt.Errorf("%w: created %s but could not link it: %w", errFactoryTracker, created.URL, err)
	}
	if err := mutator.MutateGraph(ctx, factory.GraphMutation{Action: "delete", EpicID: epicID, IssueID: issueID, Actor: "user"}); err != nil {
		return created, fmt.Errorf("%w: created %s but could not remove the ticket: %w", errFactoryTracker, created.URL, err)
	}
	return created, nil
}

func (s *Server) handleFactoryIssueTransfer(w http.ResponseWriter, r *http.Request, epicID, issueID string) {
	created, err := s.transferFactoryIssue(r.Context(), epicID, issueID)
	if err != nil {
		if errors.Is(err, errFactoryTracker) {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeFactoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, created)
}
