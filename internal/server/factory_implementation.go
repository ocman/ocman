package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
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

func (l factoryImplementationLauncher) ValidateImplementationHandoff(ctx context.Context, repoRoot, branch, previousPRURL, prURL string, policy model.FactoryAttemptPolicy) error {
	remote := forge.Remote{Type: forge.RemoteType(policy.DeliveryRemoteType), Host: policy.DeliveryRemoteHost, Repo: policy.DeliveryRemoteRepo}
	client, ok := l.server.resolveForge(remote)
	if !ok || remote.Repo == "" {
		return errors.New("factory delivery target is unavailable")
	}
	pr, err := lookupFactoryPR(ctx, client, remote.Repo, prURL)
	if err != nil {
		return err
	}
	validationBranch := branch
	if previousPRURL != "" {
		previous, err := lookupFactoryPR(ctx, client, remote.Repo, previousPRURL)
		if err != nil {
			return err
		}
		if previousPRURL == prURL {
			if previous.Status != "open" && previous.Status != "draft" {
				return errors.New("completed pull request requires a replacement")
			}
			if !validFactoryBranch(branch, pr.Branch) {
				return errors.New("pull request does not publish the shared Factory branch HEAD")
			}
		} else if previous.Status != "closed" && previous.Status != "merged" {
			return errors.New("factory epic already uses an open pull request")
		} else if expected, ok := nextFactoryBranch(branch, previous.Branch); !ok || (pr.Status != "merged" && pr.Branch != expected) {
			return errors.New("pull request does not publish the shared Factory branch HEAD")
		} else {
			validationBranch = expected
		}
	} else if pr.Status != "merged" && pr.Branch != branch {
		return errors.New("pull request does not publish the shared Factory branch HEAD")
	}
	owner := l.server.router().ForDir(repoRoot)
	host, ok := owner.(factoryHandoffHost)
	if !ok {
		return errors.New("factory handoff validation is unavailable")
	}
	head, err := host.ValidateFactoryHandoff(ctx, repoRoot, validationBranch)
	if err != nil {
		return err
	}
	if pr.HeadSHA == head && !pr.CrossFork && (pr.Status == "open" || pr.Status == "draft" || pr.Status == "merged") {
		return nil
	}
	return errors.New("pull request does not publish the shared Factory branch HEAD")
}

func (l factoryImplementationLauncher) ResolveImplementationBranch(ctx context.Context, repoRoot, branch, previousPRURL string, policy model.FactoryAttemptPolicy) (string, string, error) {
	if previousPRURL == "" {
		return branch, "", nil
	}
	remote := forge.Remote{Type: forge.RemoteType(policy.DeliveryRemoteType), Host: policy.DeliveryRemoteHost, Repo: policy.DeliveryRemoteRepo}
	client, ok := l.server.resolveForge(remote)
	if !ok || remote.Repo == "" {
		return "", "", errors.New("factory delivery target is unavailable")
	}
	previous, err := lookupFactoryPR(ctx, client, remote.Repo, previousPRURL)
	if err != nil {
		return "", "", err
	}
	switch previous.Status {
	case "open", "draft":
		if validFactoryBranch(branch, previous.Branch) {
			return previous.Branch, "", nil
		}
	case "closed", "merged":
		if next, ok := nextFactoryBranch(branch, previous.Branch); ok {
			if previous.HeadSHA == "" {
				return "", "", errors.New("factory pull request has no head commit")
			}
			branches, err := l.server.router().ForDir(repoRoot).GitBranches(ctx, repoRoot)
			if err != nil {
				return "", "", err
			}
			for _, existing := range branches {
				if existing == next {
					return "", "", errors.New("replacement Factory branch already exists")
				}
			}
			return next, previous.HeadSHA, nil
		}
	}
	return "", "", errors.New("factory pull request has an invalid branch or status")
}

func validFactoryBranch(base, branch string) bool {
	if branch == base {
		return true
	}
	prefix := base + "-"
	if !strings.HasPrefix(branch, prefix) {
		return false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(branch, prefix))
	return err == nil && n >= 2 && branch == prefix+strconv.Itoa(n)
}

func nextFactoryBranch(base, current string) (string, bool) {
	if current == base {
		return base + "-2", true
	}
	prefix := base + "-"
	if !strings.HasPrefix(current, prefix) {
		return "", false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(current, prefix))
	if err != nil || n < 2 || current != prefix+strconv.Itoa(n) {
		return "", false
	}
	return prefix + strconv.Itoa(n+1), true
}

func lookupFactoryPR(ctx context.Context, client forge.Forge, repo, prURL string) (forge.PR, error) {
	parsed, err := url.Parse(prURL)
	if err != nil {
		return forge.PR{}, err
	}
	number, err := strconv.Atoi(path.Base(parsed.Path))
	if err != nil {
		return forge.PR{}, errors.New("pull request URL has no numeric identifier")
	}
	pr, err := client.LookupPR(ctx, repo, number)
	if err != nil {
		return forge.PR{}, err
	}
	if pr.URL != prURL {
		return forge.PR{}, errors.New("pull request URL does not match the delivery target")
	}
	return pr, nil
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
	created, err := owner.CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{ProjectDir: req.Repository, Branch: req.Branch, BaseRef: req.BaseRef, MustCreateBranch: req.BaseRef != "", Title: "implementation " + req.WorkID + " (@factory)", NewBranch: true, PermissionRules: rules})
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

func (l factoryImplementationLauncher) ResumeImplementationSession(ctx context.Context, session factory.PlanningSession, gateID, response string) error {
	platform, ok := l.server.registry.Get(platforms.ID(session.Platform))
	if !ok {
		return errors.New("implementation platform is unavailable")
	}
	marker := "Factory recovery response for gate " + gateID + ":"
	detail, err := platform.Session(ctx, session.ID, 20, 0)
	if err != nil {
		return fmt.Errorf("check Factory recovery delivery: %w", err)
	}
	if detail == nil {
		return errors.New("check Factory recovery delivery: session is unavailable")
	}
	for _, part := range detail.Parts {
		if bytes.Contains(part.Data, []byte(marker)) {
			return nil
		}
	}
	prompt := fmt.Sprintf("%s\n\n%s\n\nContinue the existing Factory Issue using this response.", marker, response)
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
