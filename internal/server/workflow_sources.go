package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

type workflowStatusInferer struct{ s *Server }

// TurnRunning reports whether a turn is in flight. Since #488 the status it
// reads is settled against the agent's own turn signal rather than inferred
// from the last message's shape, so a true answer here means the agent says
// it is working — not that ocman guessed so from a missing `finish`.
func (i *workflowStatusInferer) TurnRunning(ctx context.Context, _ string, sessionID string) (bool, bool) {
	p, found := i.s.registry.PlatformForSession(ctx, sessionID)
	if !found {
		return false, false
	}
	detail, err := p.Session(ctx, sessionID, 1, 0)
	if err != nil || detail == nil || detail.Session == nil {
		return false, false
	}
	return detail.Session.Status == db.StatusBusy, true
}

func (i *workflowStatusInferer) LatestMessageState(ctx context.Context, _ string, sessionID string) (string, int64, bool, bool, bool) {
	p, found := i.s.registry.PlatformForSession(ctx, sessionID)
	if !found {
		return "", 0, false, false, false
	}
	detail, err := p.Session(ctx, sessionID, 1, 0)
	if err != nil || detail == nil || detail.Session == nil {
		return "", 0, false, false, false
	}
	if len(detail.Messages) == 0 {
		return "", 0, false, true, true
	}
	message := detail.Messages[0]
	var data struct {
		Role string `json:"role"`
	}
	if json.Unmarshal(message.Data, &data) != nil {
		return "", 0, false, false, false
	}
	running := detail.Session.Status == db.StatusBusy
	completed := data.Role == "assistant" && detail.Session.Status != db.StatusBusy
	return message.ID, message.TimeCreated, running, completed, true
}

type workflowUsage struct{ s *Server }

func (u *workflowUsage) SessionUsage(ctx context.Context, sessionIDs []string) (int64, float64, bool) {
	var tokens int64
	var cost float64
	var any bool
	for _, id := range sessionIDs {
		p, found := u.s.registry.PlatformForSession(ctx, id)
		if !found {
			continue
		}
		detail, err := p.Session(ctx, id, 1, 0)
		if err != nil || detail == nil || detail.Session == nil {
			continue
		}
		tokens += detail.Session.TotalInputTokens + detail.Session.TotalOutputTokens
		cost += detail.Session.TotalCost
		any = true
	}
	return tokens, cost, any
}

type workflowForge struct{ s *Server }

func (f *workflowForge) PollPR(ctx context.Context, directory string, prNumber int) (workflows.PRState, error) {
	_, remotes, err := f.s.detectUpstreams(ctx, directory)
	if err != nil {
		return workflows.PRState{}, err
	}
	if len(remotes) == 0 {
		return workflows.PRState{}, fmt.Errorf("no upstream forge detected for %s", directory)
	}
	forge, ok := f.s.resolveForge(remotes[0])
	if !ok {
		return workflows.PRState{}, fmt.Errorf("could not resolve forge for %s", remotes[0].Host)
	}
	pr, err := f.s.fetchSinglePR(ctx, forge, remotes[0].Repo, prNumber)
	if err != nil {
		return workflows.PRState{}, err
	}
	return workflows.PRState{HeadSHA: pr.HeadSHA, Merged: pr.Status == "merged"}, nil
}
