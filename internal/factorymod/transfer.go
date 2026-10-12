package factorymod

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/httpx"
)

// errFactoryTracker marks failures on the forge side of a transfer.
var errFactoryTracker = errors.New("issue tracker")

type factoryIssueTransferrer interface {
	TransferIssue(context.Context, string, string, func(context.Context, factory.Issue) (string, error)) (string, error)
	CancelIssueTransfer(context.Context, string, string) error
}

// TransferIssue moves an open Epic ticket to its project's GitHub or
// Forgejo tracker. Factory reserves the ticket first and keeps a receipt, so
// this only supplies the forge call.
func (m *Module) TransferIssue(ctx context.Context, epicID, issueID string) (string, error) {
	service, ok := m.svc.(factoryIssueTransferrer)
	if !ok {
		return "", factory.ErrFactoryUnavailable
	}
	epic, err := m.svc.GetWorkEpic(ctx, epicID)
	if err != nil {
		return "", err
	}
	return service.TransferIssue(ctx, epicID, issueID, func(ctx context.Context, issue factory.Issue) (string, error) {
		project := issue.Project
		if project == "" {
			project = epic.InitialProject
		}
		upstreams, err := m.deps.Router().ForDir(project).ProjectUpstreams(ctx, project)
		if err != nil {
			return "", fmt.Errorf("%w: resolve: %w", errFactoryTracker, err)
		}
		var creator forge.IssueCreator
		var repo string
		for _, remote := range upstreams.Remotes {
			client, ok := m.deps.ResolveForge(remote)
			if c, canCreate := client.(forge.IssueCreator); ok && canCreate && (creator == nil || remote.Name == "origin") {
				creator, repo = c, remote.Repo
			}
		}
		if creator == nil {
			return "", fmt.Errorf("%w: no supported issue tracker for %s", factory.ErrInvalidRequest, project)
		}
		body := fmt.Sprintf("%s\n\n---\nTransferred from ocman Factory Epic %s (%s), Issue %s.", issue.Description, epic.ID, epic.Goal, issue.ID)
		created, err := creator.CreateIssue(ctx, repo, issue.Title, body)
		if err != nil {
			// The POST may have landed before the error; keep the reservation.
			return "", fmt.Errorf("%w: create issue: %w (%w; check the tracker, then cancel the transfer to retry)", errFactoryTracker, err, factory.ErrTrackerUncertain)
		}
		return created.URL, nil
	})
}

func (m *Module) handleFactoryIssueTransfer(w http.ResponseWriter, r *http.Request, epicID, issueID string) {
	url, err := m.TransferIssue(r.Context(), epicID, issueID)
	switch {
	case err == nil:
		httpx.WriteJSONStatus(w, http.StatusCreated, map[string]string{"url": url})
	case url != "":
		// The tracker issue exists; a retry resumes from the receipt.
		http.Error(w, fmt.Sprintf("created %s but could not finish the transfer (retry to resume): %v", url, err), http.StatusBadGateway)
	case errors.Is(err, errFactoryTracker):
		http.Error(w, err.Error(), http.StatusBadGateway)
	default:
		WriteError(w, err)
	}
}

func (m *Module) handleFactoryIssueTransferCancel(w http.ResponseWriter, r *http.Request, epicID, issueID string) {
	service, ok := m.svc.(factoryIssueTransferrer)
	if !ok {
		WriteError(w, factory.ErrFactoryUnavailable)
		return
	}
	if err := service.CancelIssueTransfer(r.Context(), epicID, issueID); err != nil {
		WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
