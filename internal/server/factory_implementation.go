package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type factoryImplementationLauncher struct{ server *Server }

type factoryHandoffHost interface {
	ValidateFactoryHandoff(context.Context, string, string) (string, error)
}

func (l factoryImplementationLauncher) ValidateImplementationHandoff(ctx context.Context, repoRoot, branch, prURL string, policy model.FactoryAttemptPolicy) error {
	owner := l.server.router().ForDir(repoRoot)
	host, ok := owner.(factoryHandoffHost)
	if !ok {
		return errors.New("factory handoff validation is unavailable")
	}
	head, err := host.ValidateFactoryHandoff(ctx, repoRoot, branch)
	if err != nil {
		return err
	}
	remote := forge.Remote{Type: forge.RemoteType(policy.DeliveryRemoteType), Host: policy.DeliveryRemoteHost, Repo: policy.DeliveryRemoteRepo}
	client, ok := l.server.resolveForge(remote)
	if !ok || remote.Repo == "" {
		return errors.New("factory delivery target is unavailable")
	}
	parsed, err := url.Parse(prURL)
	if err != nil {
		return err
	}
	number, err := strconv.Atoi(path.Base(parsed.Path))
	if err != nil {
		return errors.New("pull request URL has no numeric identifier")
	}
	pr, err := client.LookupPR(ctx, remote.Repo, number)
	if err != nil {
		return err
	}
	if pr.URL == prURL && pr.Branch == branch && pr.HeadSHA == head && !pr.CrossFork && (pr.Status == "open" || pr.Status == "draft") {
		return nil
	}
	return errors.New("pull request does not publish the shared Factory branch HEAD")
}

func (l factoryImplementationLauncher) LaunchImplementationSession(ctx context.Context, req factory.ImplementationSessionRequest) (factory.PlanningSession, error) {
	if req.Profile != "factory-implement/v1" {
		return factory.PlanningSession{}, errors.New("invalid Factory implementation profile")
	}
	owner := l.server.router().ForDir(req.Repository)
	if l.server.stateDB != nil {
		upstreams, err := owner.ProjectUpstreams(ctx, req.Repository)
		if err != nil {
			return factory.PlanningSession{}, fmt.Errorf("resolve Factory delivery target: %w", err)
		}
		var delivery forge.Remote
		for _, remote := range upstreams.Remotes {
			if _, ok := l.server.resolveForge(remote); ok && (delivery.Repo == "" || remote.Name == "origin") {
				delivery = remote
				if remote.Name == "origin" {
					break
				}
			}
		}
		if delivery.Repo == "" {
			return factory.PlanningSession{}, errors.New("no supported Factory delivery remote")
		}
		if err := l.server.stateDB.SetFactoryAttemptDeliveryTarget(ctx, req.AttemptID, string(delivery.Type), delivery.Host, delivery.Repo, time.Now()); err != nil {
			return factory.PlanningSession{}, err
		}
	}
	ensured, err := owner.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: req.Repository})
	if err != nil {
		return factory.PlanningSession{}, fmt.Errorf("ensure Factory runtime: %w", err)
	}
	if ensured == nil {
		return factory.PlanningSession{}, errors.New("ensure Factory runtime returned no instance")
	}
	if err := factoryPlanningLauncher(l).ensureMCP(ctx, ensured); err != nil {
		return factory.PlanningSession{}, err
	}
	rules := []platforms.PermissionRule{{Permission: "read", Pattern: "*", Action: "allow"}, {Permission: "glob", Pattern: "*", Action: "allow"}, {Permission: "grep", Pattern: "*", Action: "allow"}, {Permission: "list", Pattern: "*", Action: "allow"}, {Permission: "bash", Pattern: "*", Action: "allow"}, {Permission: "edit", Pattern: "*", Action: "allow"}, {Permission: "task", Pattern: "*", Action: "allow"}, {Permission: "external_directory", Pattern: "*", Action: "ask"}}
	if owner.RemoteID() == "local" {
		rules = append(rules, factorySkillDirectoryRules()...)
	}
	rules = append(rules, platforms.PermissionRule{Permission: "mcp_factory", Pattern: "factory", Action: "allow"})
	created, err := owner.CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{ProjectDir: req.Repository, Branch: req.Branch, Title: "implementation " + req.WorkID + " (@factory)", NewBranch: true, PermissionRules: rules})
	if err != nil {
		return factory.PlanningSession{}, fmt.Errorf("create Factory worktree: %w", err)
	}
	return factory.PlanningSession{Platform: "opencode", ID: created.SessionID}, nil
}

func (l factoryImplementationLauncher) PromptImplementationSession(ctx context.Context, session factory.PlanningSession, req factory.ImplementationSessionRequest) error {
	prompt := fmt.Sprintf(`Implement Factory Issue %s in Work Epic %s.

Title: %s

Task:
%s

Work only on this Issue in the assigned worktree. Inspect the existing code, make the smallest correct change, and run the relevant checks. All implementation Issues in this Work Epic use the shared branch %s and run sequentially.

Before completion, leave the shared worktree on a clean commit, push the branch, and create or reuse its single pull request. Then use the factory MCP action complete_attempt with attempt_id %s, attempt_token %s, pr_url set to that pull request, and a concise summary. If you cannot safely continue, use request_recovery with the same attempt ID and token instead of guessing.`, req.WorkID, req.EpicID, req.Title, req.Description, req.Branch, req.AttemptID, req.AgentToken)
	return l.server.sessions.SendMessage(ctx, session.Platform, platforms.SendMessageRequest{SessionID: session.ID, Message: prompt})
}

func (l factoryImplementationLauncher) StopImplementationSession(ctx context.Context, session factory.PlanningSession) error {
	return l.server.sessions.Dispose(ctx, session.Platform, platforms.DisposeSessionRequest{SessionID: session.ID})
}

func (l factoryImplementationLauncher) RespondImplementationPermission(ctx context.Context, session factory.PlanningSession, permissionID, reply string) error {
	return l.server.sessions.RespondPermission(ctx, session.Platform, platforms.RespondPermissionRequest{SessionID: session.ID, PermissionID: permissionID, Reply: reply})
}

func (l factoryImplementationLauncher) ImplementationPermissionPending(ctx context.Context, session factory.PlanningSession, permissionID string) (bool, error) {
	platform, ok := l.server.registry.Get(platforms.ID(session.Platform))
	if !ok {
		return false, errors.New("implementation platform is unavailable")
	}
	prompts, err := platform.ListPermissions(ctx, session.ID)
	if err != nil {
		return false, err
	}
	for _, prompt := range prompts {
		if id, _ := prompt["id"].(string); id == permissionID {
			return true, nil
		}
	}
	return false, nil
}

func (l factoryImplementationLauncher) ProbeImplementationSession(ctx context.Context, session factory.PlanningSession) (bool, error) {
	platform, ok := l.server.registry.Get(platforms.ID(session.Platform))
	if !ok {
		return false, errors.New("implementation platform is unavailable")
	}
	return platform.Owns(ctx, session.ID), nil
}
