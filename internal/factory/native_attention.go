package factory

import (
	"context"
	"fmt"
	"net/url"

	"github.com/sirupsen/logrus"
)

// reconcileAttention runs after dispatch so automatically runnable work never
// produces an action notification. Inbox identities survive refresh/restart.
func (s *NativeService) reconcileAttention(ctx context.Context) {
	inbox, ok := s.store.(interface {
		SyncFactoryActionInbox(context.Context, string, string, map[string]string) error
	})
	if !ok {
		return
	}
	s.attentionMu.Lock()
	defer s.attentionMu.Unlock()
	// ponytail: scan all epics after dispatch; scope to changed epics if history makes this costly.
	epics, err := s.ListWorkEpics(ctx)
	if err != nil {
		logrus.WithError(err).Warn("Factory attention scan failed")
		return
	}
	for _, epic := range epics {
		issues, err := s.ListIssues(ctx, epic.ID)
		if err != nil {
			logrus.WithError(err).WithField("epic", epic.ID).Warn("Factory attention scan failed")
			continue
		}
		actions := factoryAttention(epic, issues)
		if err := inbox.SyncFactoryActionInbox(ctx, epic.ID, "Factory needs attention: "+epic.Goal, actions); err != nil {
			logrus.WithError(err).WithField("epic", epic.ID).Warn("Factory attention Inbox sync failed")
		}
	}
}

func factoryAttention(epic WorkEpic, issues []Issue) map[string]string {
	actions := map[string]string{}
	if epic.Status == "closed" {
		return actions
	}
	link := "\n\n[Open Factory actions](/factory/epics/" + url.PathEscape(epic.ID) + ")"
	add := func(key, text string) { actions[key] = text + link }
	if gate := epic.PlanGate; gate != nil && gate.Resolution == "open" {
		add(fmt.Sprintf("plan:%s:%d:%s", gate.IssueID, gate.ProposalRevision, gate.ProposalHash), "Plan approval required")
	}
	for _, issue := range issues {
		key := fmt.Sprintf("%s:%s:%d", issue.ID, issue.AttemptID, issue.PlanRevision)
		switch {
		case issue.Recovery != nil && issue.Recovery.Resolution != "resume" && issue.Recovery.Resolution != "retry" && issue.Recovery.Resolution != "cancel":
			add(key+":recovery", "Recovery required: "+issue.Title)
		case issue.Authority != nil && issue.Authority.Resolution != "approve" && issue.Authority.Resolution != "reject":
			add(key+":authority", "Permission decision required: "+issue.Title)
		case issue.ProjectRequest != nil && issue.ProjectRequest.Resolution != "approved" && issue.ProjectRequest.Resolution != "rejected":
			add(key+":project", "Project scope decision required: "+issue.Title)
		case epic.Status == "open" && issue.Status == "closed" && (issue.Outcome == "failed" || issue.Outcome == "cancelled") && (issue.Kind == "task" || issue.Kind == "implementation" || issue.Kind == "delivery" || issue.Kind == "approval"):
			add(key+":failed", "Work needs recovery: "+issue.Title)
		case epic.Status == "open" && issue.DispatchState == "terminally_blocked":
			add(key+":blocked", "Work is blocked: "+issue.Title)
		case epic.Status == "open" && issue.DispatchState == "ready" && (issue.Kind == "plan" || issue.Kind == "materialization" || issue.Kind == "approval"):
			add(key+":ready", "Action required: "+issue.Title)
		}
	}
	if epic.Progress.Stuck && len(actions) == 0 {
		add(fmt.Sprintf("stuck:%d", len(epic.Attempts)), "Epic is stuck. Review its work graph.")
	}
	return actions
}
