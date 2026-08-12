package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

type workflowStatusInferer struct{ s *Server }

// sessionDetail resolves a session's owning adapter and fetches its detail.
// A session's identity is (platform, sessionID): the named platform wins
// (AD-2b), because the same bare id can exist on several machines and a
// reverse lookup would answer from whichever adapter happens to own it
// first. The bare-id fallback survives only for a caller that genuinely has
// no platform — a hand-written workflow trigger that omitted it.
func (i *workflowStatusInferer) sessionDetail(ctx context.Context, platform, sessionID string) (*platforms.SessionDetail, bool) {
	var p platforms.Platform
	var found bool
	if platform != "" {
		p, found = i.s.registry.Get(platforms.ID(platform))
	} else {
		p, found = i.s.registry.PlatformForSession(ctx, sessionID)
	}
	if !found {
		return nil, false
	}
	detail, err := p.Session(ctx, sessionID, 1, 0)
	if err != nil || detail == nil || detail.Session == nil {
		return nil, false
	}
	return detail, true
}

// TurnRunning reports whether a turn is in flight. Since #488 the status it
// reads is settled against the agent's own turn signal rather than inferred
// from the last message's shape, so a true answer here means the agent says
// it is working — not that ocman guessed so from a missing `finish`.
func (i *workflowStatusInferer) TurnRunning(ctx context.Context, platform, sessionID string) (bool, bool) {
	detail, ok := i.sessionDetail(ctx, platform, sessionID)
	if !ok {
		return false, false
	}
	return detail.Session.Status == db.StatusBusy, true
}

func (i *workflowStatusInferer) LatestMessageState(ctx context.Context, platform, sessionID string) (string, int64, bool, bool, bool) {
	detail, ok := i.sessionDetail(ctx, platform, sessionID)
	if !ok {
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

// SessionUsage sums tokens and cost over the sessions a run's attempts
// recorded. Each reference is a full (platform, sessionID) identity: with a
// bare id, a session id shared by two machines billed the run for the wrong
// one. A reference whose platform is empty or unregistered contributes
// nothing — there is no safe way to guess its owner.
func (u *workflowUsage) SessionUsage(ctx context.Context, sessions []state.Key) (int64, float64, bool) {
	var tokens int64
	var cost float64
	var any bool
	for _, ref := range sessions {
		if ref.Platform == "" || ref.SessionID == "" {
			continue
		}
		p, found := u.s.registry.Get(platforms.ID(ref.Platform))
		if !found {
			continue
		}
		detail, err := p.Session(ctx, ref.SessionID, 1, 0)
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
