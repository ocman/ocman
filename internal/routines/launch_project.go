package routines

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ocman/ocman/internal/state"
)

// RunNowInProject supplies a project only for a routine whose project is unset.
// The routine's machine remains fixed, including for disconnected remotes.
func (s *Service) RunNowInProject(ctx context.Context, id, project string) (state.RoutineRun, error) {
	if project == "" {
		return s.RunNow(ctx, id)
	}
	routine, err := s.store.GetRoutine(ctx, id)
	if err != nil {
		return state.RoutineRun{}, err
	}
	directory, err := s.launchProject(ctx, routine, project)
	if err != nil {
		return state.RoutineRun{}, err
	}
	occurrence := s.now().UnixMilli()
	if routine.NextDueAt > 0 && routine.NextDueAt <= occurrence {
		occurrence = routine.NextDueAt
	}
	return s.claimAndDispatchPrompt(ctx, routine, occurrence, "manual", routine.Prompt, "", directory)
}

func (s *Service) RunWebhookInProject(ctx context.Context, id, payload, label string, occurrence int64, project string) (state.RoutineRun, error) {
	routine, err := s.store.GetRoutine(ctx, id)
	if err != nil {
		return state.RoutineRun{}, err
	}
	if routine.Deleted || !routine.Enabled {
		return state.RoutineRun{}, fmt.Errorf("routine is disabled")
	}
	if routine.RemoteID != "" && routine.RemoteID != state.LocalRemoteID {
		return state.RoutineRun{}, fmt.Errorf("routine runs on another machine")
	}
	directory, err := s.launchProject(ctx, routine, project)
	if err != nil {
		return state.RoutineRun{}, err
	}
	return s.claimAndDispatchPrompt(ctx, routine, occurrence, "webhook", routine.Prompt+"\n\n"+payload, label, directory)
}

func (s *Service) launchProject(ctx context.Context, routine state.Routine, project string) (string, error) {
	project = strings.TrimSpace(project)
	if routine.Directory != "" {
		if project != "" && project != routine.Directory {
			return "", state.ErrRoutineProject
		}
		return routine.Directory, nil
	}
	if project == "" {
		return "", state.ErrRoutineProject
	}
	host, ok := s.router.LookupRemote(routine.RemoteID)
	if !ok {
		return "", fmt.Errorf("routine target %q is unavailable", routine.RemoteID)
	}
	canonicalGitKey := strings.HasPrefix(project, "git:") && !strings.HasPrefix(project, "git://")
	if !filepath.IsAbs(project) && (strings.HasPrefix(project, "dir:") || canonicalGitKey || s.normalizeProjectUpstream(project) == "") {
		return s.launchProjectKey(ctx, routine.RemoteID, project)
	}
	projects, err := host.Projects(ctx)
	if err != nil {
		return "", fmt.Errorf("listing routine projects: %w", err)
	}
	archived, err := s.store.ArchivedProjects(ctx)
	if err != nil {
		return "", err
	}
	key := ""
	if s.normalizeUpstream != nil {
		key = s.normalizeUpstream(project)
	}
	matches := map[string]bool{}
	for _, candidate := range projects {
		archivedAt, isArchived := archived[state.ProjectKey{RemoteID: state.NormalizeRemoteID(routine.RemoteID), Root: candidate.Directory}]
		if candidate.Archived || (isArchived && candidate.LastUsed <= archivedAt) || !filepath.IsAbs(candidate.Directory) {
			continue
		}
		match := project == candidate.Directory
		for _, upstream := range candidate.UpstreamKeys {
			match = match || (key != "" && upstream == key)
		}
		if key != "" && (candidate.UpstreamOrigin == key || s.normalizeUpstream(candidate.UpstreamOrigin) == key) {
			match = true
		}
		if match {
			matches[candidate.Directory] = true
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("project must match exactly one known project on the routine's machine: %w", ErrValidation)
	}
	for directory := range matches {
		return directory, nil
	}
	return "", state.ErrRoutineProject
}

func (s *Service) normalizeProjectUpstream(project string) string {
	if s.normalizeUpstream == nil {
		return ""
	}
	return s.normalizeUpstream(project)
}

func (s *Service) launchProjectKey(ctx context.Context, owner, key string) (string, error) {
	if s.projectCatalog == nil {
		return "", fmt.Errorf("project catalog unavailable: %w", ErrValidation)
	}
	projects, err := s.projectCatalog(ctx)
	if err != nil {
		return "", err
	}
	matches := map[string]bool{}
	for _, project := range projects {
		if project.ProjectKey == key && !project.Archived && filepath.IsAbs(project.Directory) && state.NormalizeRemoteID(project.RemoteID) == state.NormalizeRemoteID(owner) {
			matches[project.Directory] = true
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("project key must match exactly one known project on the routine's machine: %w", ErrValidation)
	}
	for directory := range matches {
		return directory, nil
	}
	return "", state.ErrRoutineProject
}
