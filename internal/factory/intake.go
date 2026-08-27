package factory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrProjectNotLocalGit      = errors.New("project is not a local Git repository")
	ErrPreparationStale        = errors.New("factory preparation is stale")
	ErrAcknowledgementRequired = errors.New("local execution acknowledgement is required")
)

type PrepareWorkRequest struct {
	Goal        string `json:"goal"`
	Brief       string `json:"brief"`
	ProjectPath string `json:"projectPath"`
}

type PreparedWork struct {
	PreparationKey          string  `json:"preparationKey"`
	Goal                    string  `json:"goal"`
	Brief                   string  `json:"brief"`
	ProjectPath             string  `json:"projectPath"`
	Formula                 Formula `json:"formula"`
	AcknowledgementRequired bool    `json:"acknowledgementRequired"`
}

func (s *Service) PrepareWork(ctx context.Context, req PrepareWorkRequest) (PreparedWork, error) {
	if strings.TrimSpace(req.Goal) == "" {
		return PreparedWork{}, errors.New("goal is required")
	}
	if strings.TrimSpace(req.Brief) == "" {
		return PreparedWork{}, errors.New("brief is required")
	}
	project, err := s.canonicalGitProject(ctx, req.ProjectPath)
	if err != nil {
		return PreparedWork{}, err
	}
	if !s.ownsMutations() {
		return PreparedWork{}, fmt.Errorf("%w: this process does not own Factory mutations", ErrFactoryUnavailable)
	}
	if s.acks == nil {
		return PreparedWork{}, fmt.Errorf("%w: acknowledgement store is unavailable", ErrFactoryUnavailable)
	}
	acknowledged, err := s.acks.HasFactoryLocalExecutionAck(ctx, localHostID, project, planningProfileID, planningProfileVersion)
	if err != nil {
		return PreparedWork{}, fmt.Errorf("%w: check local execution acknowledgement: %w", ErrFactoryUnavailable, err)
	}
	formula := DefaultFormula()
	return PreparedWork{
		PreparationKey:          preparationKey(req.Goal, req.Brief, project, formula),
		Goal:                    req.Goal,
		Brief:                   req.Brief,
		ProjectPath:             project,
		Formula:                 formula,
		AcknowledgementRequired: !acknowledged,
	}, nil
}

func (s *Service) AcknowledgeLocalExecution(ctx context.Context, projectPath string) error {
	project, err := s.canonicalGitProject(ctx, projectPath)
	if err != nil {
		return err
	}
	if !s.ownsMutations() {
		return fmt.Errorf("%w: this process does not own Factory mutations", ErrFactoryUnavailable)
	}
	if s.acks == nil {
		return fmt.Errorf("%w: acknowledgement store is unavailable", ErrFactoryUnavailable)
	}
	if err := s.acks.UpsertFactoryLocalExecutionAck(ctx, localHostID, project, planningProfileID, planningProfileVersion, operatorActor, time.Now()); err != nil {
		return fmt.Errorf("%w: record local execution acknowledgement: %w", ErrFactoryUnavailable, err)
	}
	return nil
}

func (s *Service) CreatePreparedWorkEpic(ctx context.Context, prepared PreparedWork) (WorkEpic, error) {
	if strings.TrimSpace(prepared.Goal) == "" || strings.TrimSpace(prepared.Brief) == "" {
		return WorkEpic{}, ErrPreparationStale
	}
	project, err := s.canonicalGitProject(ctx, prepared.ProjectPath)
	if err != nil {
		return WorkEpic{}, err
	}
	formula := DefaultFormula()
	if project != prepared.ProjectPath || prepared.Formula.ID != formula.ID || prepared.Formula.Version != formula.Version ||
		prepared.PreparationKey != preparationKey(prepared.Goal, prepared.Brief, project, formula) {
		return WorkEpic{}, ErrPreparationStale
	}
	if !s.ownsMutations() {
		return WorkEpic{}, fmt.Errorf("%w: this process does not own Factory mutations", ErrFactoryUnavailable)
	}
	if s.acks == nil {
		return WorkEpic{}, fmt.Errorf("%w: acknowledgement store is unavailable", ErrFactoryUnavailable)
	}
	acknowledged, err := s.acks.HasFactoryLocalExecutionAck(ctx, localHostID, project, planningProfileID, planningProfileVersion)
	if err != nil {
		return WorkEpic{}, fmt.Errorf("%w: check local execution acknowledgement: %w", ErrFactoryUnavailable, err)
	}
	if !acknowledged {
		return WorkEpic{}, ErrAcknowledgementRequired
	}
	return s.createWorkEpic(ctx, CreateWorkEpicRequest{
		InstantiationID: prepared.PreparationKey,
		Goal:            prepared.Goal,
		Brief:           prepared.Brief,
		InitialProject:  project,
	})
}

func (s *Service) ownsMutations() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.owned
}

func (s *Service) canonicalGitProject(ctx context.Context, path string) (string, error) {
	if !filepath.IsAbs(path) || s.projects == nil {
		return "", ErrProjectNotLocalGit
	}
	root, err := s.projects.ResolveLocalProject(ctx, path)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrProjectNotLocalGit, err)
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return "", ErrProjectNotLocalGit
	}
	return root, nil
}

func preparationKey(goal, brief, project string, formula Formula) string {
	payload, _ := json.Marshal([]any{project, formula.ID, formula.Version, goal, brief})
	sum := sha256.Sum256(payload)
	return "factory-intake-" + hex.EncodeToString(sum[:])
}
