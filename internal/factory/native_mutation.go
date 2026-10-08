package factory

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func (s *NativeService) canonicalMutationProjects(ctx context.Context, epic model.NativeEpic, mutation *GraphMutation) error {
	changes := []*GraphMutation{mutation}
	if mutation.Action == "batch" {
		changes = nil
		for i := range mutation.Mutations {
			changes = append(changes, &mutation.Mutations[i])
		}
	}
	for _, change := range changes {
		if change.Action == "create" || change.Project != "" {
			project, err := s.canonicalIssueProject(ctx, epic, change.Project)
			if err != nil {
				return err
			}
			change.Project = project
		}
	}
	return nil
}
