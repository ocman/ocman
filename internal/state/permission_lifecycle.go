package state

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type PermissionEvaluationMethod string

const (
	PermissionEvaluationJudge    PermissionEvaluationMethod = "judge"
	PermissionEvaluationCache    PermissionEvaluationMethod = "cache"
	PermissionEvaluationDenylist PermissionEvaluationMethod = "denylist"
)

type PermissionEvaluationResult string

const (
	PermissionEvaluationSafe       PermissionEvaluationResult = "safe"
	PermissionEvaluationUnsafe     PermissionEvaluationResult = "unsafe"
	PermissionEvaluationCacheSafe  PermissionEvaluationResult = "cache-safe"
	PermissionEvaluationDenylisted PermissionEvaluationResult = "denylisted"
	PermissionEvaluationError      PermissionEvaluationResult = "error"
)

type PermissionResolution string

const (
	PermissionResolutionAutoApproved PermissionResolution = "auto-approved"
	PermissionResolutionUserOnce     PermissionResolution = "user-once"
	PermissionResolutionUserAlways   PermissionResolution = "user-always"
	PermissionResolutionUserRejected PermissionResolution = "user-rejected"
	PermissionResolutionCancelled    PermissionResolution = "cancelled"
)

type PermissionLifecycle struct {
	Platform          string                     `json:"platform"`
	SessionID         string                     `json:"sessionId"`
	PermissionID      string                     `json:"permissionId"`
	Directory         string                     `json:"directory"`
	RequestedAt       int64                      `json:"requestedAt"`
	JudgeStartedAt    int64                      `json:"judgeStartedAt,omitempty"`
	JudgeCompletedAt  int64                      `json:"judgeCompletedAt,omitempty"`
	ResolvedAt        int64                      `json:"resolvedAt,omitempty"`
	EvaluationMethod  PermissionEvaluationMethod `json:"evaluationMethod,omitempty"`
	EvaluationResult  PermissionEvaluationResult `json:"evaluationResult,omitempty"`
	Resolution        PermissionResolution       `json:"resolution,omitempty"`
	ManuallyPreempted bool                       `json:"manuallyPreempted"`
}

type PermissionApprovalDaily struct {
	Date               string         `json:"date"`
	EvaluationResults  map[string]int `json:"evaluationResults"`
	ManualPreemptions  int            `json:"manualPreemptions"`
	Requests           int            `json:"requests"`
	UserDecisions      int            `json:"userDecisions"`
	ObservedUserWaitMs int64          `json:"observedUserWaitMs"`
}

type PermissionApprovalStats struct {
	EligibleRequests               int                       `json:"eligibleRequests"`
	AutoApprovedCount              int                       `json:"autoApprovedCount"`
	AutoApprovedRate               float64                   `json:"autoApprovedRate"`
	JudgmentRequests               int                       `json:"judgmentRequests"`
	ManualPreemptions              int                       `json:"manualPreemptions"`
	ManualPreemptionRate           float64                   `json:"manualPreemptionRate"`
	MedianJudgmentDurationMs       int64                     `json:"medianJudgmentDurationMs"`
	MedianManualResponseDurationMs int64                     `json:"medianManualResponseDurationMs"`
	UserDecisionCount              int                       `json:"userDecisionCount"`
	UserDecisionRate               float64                   `json:"userDecisionRate"`
	AffectedSessions               int                       `json:"affectedSessions"`
	UnresolvedEligibleRequests     int                       `json:"unresolvedEligibleRequests"`
	ObservedUserWaitMs             int64                     `json:"observedUserWaitMs"`
	P50UserWaitMs                  int64                     `json:"p50UserWaitMs"`
	P95UserWaitMs                  int64                     `json:"p95UserWaitMs"`
	Daily                          []PermissionApprovalDaily `json:"daily"`
}

// UpsertPermissionLifecycle records facts as they arrive. Existing facts are
// never replaced, while manual preemption can only advance from false to true.
func (d *DB) UpsertPermissionLifecycle(ctx context.Context, lifecycle PermissionLifecycle) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO permission_lifecycle (
			platform, session_id, permission_id, directory, project_root, requested_at,
			judge_started_at, judge_completed_at, resolved_at, evaluation_method,
			evaluation_result, resolution, manually_preempted
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(platform, session_id, permission_id) DO UPDATE SET
			directory = CASE WHEN permission_lifecycle.directory = '' THEN excluded.directory ELSE permission_lifecycle.directory END,
			project_root = CASE WHEN permission_lifecycle.project_root = '' THEN excluded.project_root ELSE permission_lifecycle.project_root END,
			requested_at = CASE WHEN permission_lifecycle.requested_at = 0 THEN excluded.requested_at ELSE permission_lifecycle.requested_at END,
			judge_started_at = CASE WHEN permission_lifecycle.judge_started_at = 0 THEN excluded.judge_started_at ELSE permission_lifecycle.judge_started_at END,
			judge_completed_at = CASE WHEN permission_lifecycle.judge_completed_at = 0 THEN excluded.judge_completed_at ELSE permission_lifecycle.judge_completed_at END,
			resolved_at = CASE WHEN permission_lifecycle.resolved_at = 0 THEN excluded.resolved_at ELSE permission_lifecycle.resolved_at END,
			evaluation_method = CASE WHEN permission_lifecycle.evaluation_method = '' THEN excluded.evaluation_method ELSE permission_lifecycle.evaluation_method END,
			evaluation_result = CASE WHEN permission_lifecycle.evaluation_result = '' THEN excluded.evaluation_result ELSE permission_lifecycle.evaluation_result END,
			resolution = CASE WHEN permission_lifecycle.resolution = '' THEN excluded.resolution ELSE permission_lifecycle.resolution END,
			manually_preempted = MAX(permission_lifecycle.manually_preempted, excluded.manually_preempted)
	`, lifecycle.Platform, lifecycle.SessionID, lifecycle.PermissionID, lifecycle.Directory, ProjectRootForDirectory(lifecycle.Directory), lifecycle.RequestedAt,
		lifecycle.JudgeStartedAt, lifecycle.JudgeCompletedAt, lifecycle.ResolvedAt, lifecycle.EvaluationMethod,
		lifecycle.EvaluationResult, lifecycle.Resolution, lifecycle.ManuallyPreempted)
	if err != nil {
		return fmt.Errorf("upserting permission lifecycle: %w", err)
	}
	return nil
}

// PermissionApprovalStats aggregates permission requests observed at or after
// since in the exact directory. Dates are UTC and durations are milliseconds.
func (d *DB) PermissionApprovalStats(ctx context.Context, since int64, directory string) (PermissionApprovalStats, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT session_id, requested_at, judge_started_at, judge_completed_at, resolved_at,
		       evaluation_method, evaluation_result, resolution, manually_preempted
		FROM permission_lifecycle
		WHERE requested_at >= ? AND (? = '' OR project_root = ?)
		ORDER BY requested_at
	`, since, directory, directory)
	if err != nil {
		return PermissionApprovalStats{}, fmt.Errorf("querying permission approval stats: %w", err)
	}
	defer rows.Close()

	stats := PermissionApprovalStats{Daily: []PermissionApprovalDaily{}}
	var judgmentDurations, manualDurations, userWaits []int64
	affectedSessions := make(map[string]struct{})
	for rows.Next() {
		var requestedAt, judgeStartedAt, judgeCompletedAt, resolvedAt int64
		var sessionID, method, result, resolution string
		var manuallyPreempted bool
		if err := rows.Scan(&sessionID, &requestedAt, &judgeStartedAt, &judgeCompletedAt, &resolvedAt, &method, &result, &resolution, &manuallyPreempted); err != nil {
			return PermissionApprovalStats{}, fmt.Errorf("scanning permission approval stats: %w", err)
		}
		stats.EligibleRequests++
		if resolution == string(PermissionResolutionAutoApproved) {
			stats.AutoApprovedCount++
		}
		if method == string(PermissionEvaluationJudge) {
			stats.JudgmentRequests++
		}
		if manuallyPreempted {
			stats.ManualPreemptions++
		}
		if judgeStartedAt > 0 && judgeCompletedAt >= judgeStartedAt {
			judgmentDurations = append(judgmentDurations, judgeCompletedAt-judgeStartedAt)
		}
		if manuallyPreempted && resolvedAt >= requestedAt {
			manualDurations = append(manualDurations, resolvedAt-requestedAt)
		}
		userDecision := resolution == string(PermissionResolutionUserOnce) ||
			resolution == string(PermissionResolutionUserAlways) ||
			resolution == string(PermissionResolutionUserRejected)
		var userWait int64
		if userDecision {
			stats.UserDecisionCount++
			affectedSessions[sessionID] = struct{}{}
			if resolvedAt >= requestedAt {
				userWait = resolvedAt - requestedAt
				stats.ObservedUserWaitMs += userWait
				userWaits = append(userWaits, userWait)
			}
		} else if resolution == "" {
			stats.UnresolvedEligibleRequests++
		}

		date := time.UnixMilli(requestedAt).UTC().Format(time.DateOnly)
		if len(stats.Daily) == 0 || stats.Daily[len(stats.Daily)-1].Date != date {
			stats.Daily = append(stats.Daily, PermissionApprovalDaily{Date: date, EvaluationResults: map[string]int{}})
		}
		day := &stats.Daily[len(stats.Daily)-1]
		day.Requests++
		if result != "" {
			day.EvaluationResults[result]++
		}
		if manuallyPreempted {
			day.ManualPreemptions++
		}
		if userDecision {
			day.UserDecisions++
			day.ObservedUserWaitMs += userWait
		}
	}
	if err := rows.Err(); err != nil {
		return PermissionApprovalStats{}, fmt.Errorf("reading permission approval stats: %w", err)
	}
	if stats.EligibleRequests > 0 {
		stats.AutoApprovedRate = float64(stats.AutoApprovedCount) / float64(stats.EligibleRequests)
		stats.UserDecisionRate = float64(stats.UserDecisionCount) / float64(stats.EligibleRequests)
	}
	if stats.JudgmentRequests > 0 {
		stats.ManualPreemptionRate = float64(stats.ManualPreemptions) / float64(stats.JudgmentRequests)
	}
	stats.MedianJudgmentDurationMs = medianMilliseconds(judgmentDurations)
	stats.MedianManualResponseDurationMs = medianMilliseconds(manualDurations)
	stats.AffectedSessions = len(affectedSessions)
	stats.P50UserWaitMs = percentileMilliseconds(userWaits, 50)
	stats.P95UserWaitMs = percentileMilliseconds(userWaits, 95)
	return stats, nil
}

func percentileMilliseconds(values []int64, percentile int) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[(len(values)*percentile+99)/100-1]
}

func medianMilliseconds(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return (values[middle-1] + values[middle]) / 2
}
