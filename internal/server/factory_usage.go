package server

import (
	"context"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type factoryAttemptUsage struct {
	AttemptID string                `json:"attemptId"`
	WorkID    string                `json:"workId"`
	Stage     string                `json:"stage"`
	Session   model.PlanningSession `json:"session"`
	Usage     *platforms.Usage      `json:"usage"`
}

type factoryEpicUsage struct {
	Total      platforms.Usage            `json:"total"`
	Phases     map[string]platforms.Usage `json:"phases"`
	Attempts   []factoryAttemptUsage      `json:"attempts"`
	Incomplete bool                       `json:"incomplete"`
}

func (s *Server) handleFactoryUsage(w http.ResponseWriter, r *http.Request, epicID string) {
	if _, err := s.factory.GetWorkEpic(r.Context(), epicID); err != nil {
		writeFactoryError(w, err)
		return
	}
	if s.stateDB == nil {
		writeFactoryError(w, factory.ErrFactoryUnavailable)
		return
	}
	attempts, err := s.stateDB.ListFactoryAttempts(r.Context(), epicID)
	if err != nil {
		serverError(w, "reading Factory attempts", err)
		return
	}
	issues, err := s.stateDB.ListFactoryUsageIssues(r.Context(), epicID)
	if err != nil {
		serverError(w, "reading Factory issues", err)
		return
	}
	usage := s.factoryUsage(r.Context(), attempts, issues)
	if err := r.Context().Err(); err != nil {
		return
	}
	writeJSON(w, usage)
}

func (s *Server) factoryUsage(ctx context.Context, attempts []model.FactoryAttempt, issues []model.NativeIssue) factoryEpicUsage {
	result := factoryEpicUsage{Phases: map[string]platforms.Usage{"plan": {}, "implement": {}, "verify": {}, "deliver": {}}, Attempts: []factoryAttemptUsage{}}
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
		row := factoryAttemptUsage{AttemptID: attempt.ID, WorkID: attempt.WorkID, Stage: stage, Session: attempt.Session}
		if attempt.Session.ID == "" {
			row.Usage = &platforms.Usage{}
		} else if adapter, ok := s.registry.Get(platforms.ID(attempt.Session.Platform)); ok {
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
