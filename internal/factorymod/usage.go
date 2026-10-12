package factorymod

import (
	"context"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type AttemptUsage struct {
	AttemptID string                `json:"attemptId"`
	WorkID    string                `json:"workId"`
	Stage     string                `json:"stage"`
	Session   model.PlanningSession `json:"session"`
	Usage     *platforms.Usage      `json:"usage"`
}

type EpicUsage struct {
	Total      platforms.Usage            `json:"total"`
	Phases     map[string]platforms.Usage `json:"phases"`
	Attempts   []AttemptUsage             `json:"attempts"`
	Incomplete bool                       `json:"incomplete"`
}

func (m *Module) handleFactoryUsage(w http.ResponseWriter, r *http.Request, epicID string) {
	if _, err := m.svc.GetWorkEpic(r.Context(), epicID); err != nil {
		WriteError(w, err)
		return
	}
	if m.deps.Store == nil {
		WriteError(w, factory.ErrFactoryUnavailable)
		return
	}
	attempts, err := m.deps.Store.ListFactoryAttempts(r.Context(), epicID)
	if err != nil {
		httpx.ServerError(w, "reading Factory attempts", err)
		return
	}
	issues, err := m.deps.Store.ListFactoryUsageIssues(r.Context(), epicID)
	if err != nil {
		httpx.ServerError(w, "reading Factory issues", err)
		return
	}
	usage := m.Usage(r.Context(), attempts, issues)
	if err := r.Context().Err(); err != nil {
		return
	}
	httpx.WriteJSON(w, usage)
}

func (m *Module) Usage(ctx context.Context, attempts []model.FactoryAttempt, issues []model.NativeIssue) EpicUsage {
	result := EpicUsage{Phases: map[string]platforms.Usage{"plan": {}, "implement": {}, "verify": {}, "deliver": {}}, Attempts: []AttemptUsage{}}
	work := make(map[string]model.NativeIssue, len(issues))
	for _, issue := range issues {
		work[issue.ID] = issue
	}
	seen := map[model.PlanningSession]bool{}
	for _, attempt := range attempts {
		stage := "implement"
		issue := work[attempt.WorkID]
		switch {
		case attempt.FrozenPolicy.Profile == "factory-plan/v1":
			stage = "plan"
		case attempt.FrozenPolicy.Delivery || issue.Kind == "delivery":
			stage = "deliver"
		case issue.Workflow != nil && issue.Workflow.Kind == "verification":
			stage = "verify"
		}
		row := AttemptUsage{AttemptID: attempt.ID, WorkID: attempt.WorkID, Stage: stage, Session: attempt.Session}
		if attempt.Session.ID == "" {
			row.Usage = &platforms.Usage{}
		} else if adapter, ok := m.deps.Registry.Get(platforms.ID(attempt.Session.Platform)); ok {
			if reader, ok := adapter.(platforms.UsageReader); ok {
				if sessions, err := reader.SessionUsage(ctx, attempt.Session.ID); err == nil {
					row.Usage = &platforms.Usage{}
					for id, usage := range sessions {
						row.Usage.Add(usage)
						key := model.PlanningSession{Platform: attempt.Session.Platform, ID: id}
						if !seen[key] {
							seen[key] = true
							result.Total.Add(usage)
							phase := result.Phases[stage]
							phase.Add(usage)
							result.Phases[stage] = phase
						}
					}
				}
			}
		}
		result.Incomplete = result.Incomplete || row.Usage == nil
		result.Attempts = append(result.Attempts, row)
	}
	return result
}
