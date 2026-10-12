package factorymod

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type ImplementationLauncher struct{ m *Module }

type factoryHandoffHost interface {
	ValidateFactoryHandoff(context.Context, string, string) (string, error)
}

type factoryWorkspaceHost interface {
	PrepareFactoryWorkspace(context.Context, string, string, string, string) (string, string, error)
	ValidateFactoryCheckpoint(context.Context, string, string, string) (string, error)
}

// ResolveImplementationWorkspace recovers the actual branch of a session started
// before Factory persisted workspaces. It never creates or edits a pull request.
func (l ImplementationLauncher) ResolveImplementationWorkspace(ctx context.Context, repo, branch string, session factory.PlanningSession) (string, string, error) {
	platform, ok := l.m.deps.Registry.Get(platforms.ID(session.Platform))
	if !ok {
		return "", "", errors.New("implementation platform is unavailable")
	}
	detail, err := platform.Session(ctx, session.ID, 1, 0)
	if err != nil {
		return "", "", err
	}
	if detail == nil || detail.Session == nil || detail.Session.Directory == "" {
		return "", "", errors.New("implementation session directory is unavailable")
	}
	owner := l.m.deps.Router().ForDir(repo)
	worktrees, err := owner.ListWorktrees(ctx, repo)
	if err != nil {
		return "", "", err
	}
	for _, worktree := range worktrees {
		if filepath.Clean(worktree.Path) == filepath.Clean(detail.Session.Directory) && validFactoryBranch(branch, worktree.Branch) {
			target, _, err := l.PrepareImplementationWorkspace(ctx, repo, worktree.Branch, "", "")
			if err != nil {
				return "", "", err
			}
			return worktree.Branch, target, nil
		}
	}
	return "", "", errors.New("factory shared branch worktree was not found")
}

func (l ImplementationLauncher) PrepareImplementationWorkspace(ctx context.Context, repo, branch, checkpoint, target string) (string, string, error) {
	host, ok := l.m.deps.Router().ForDir(repo).(factoryWorkspaceHost)
	if !ok {
		return "", "", errors.New("factory workspace validation is unavailable")
	}
	return host.PrepareFactoryWorkspace(ctx, repo, branch, checkpoint, target)
}

func (l ImplementationLauncher) ValidateImplementationCheckpoint(ctx context.Context, repo, branch, checkpoint string) (string, error) {
	host, ok := l.m.deps.Router().ForDir(repo).(factoryWorkspaceHost)
	if !ok {
		return "", errors.New("factory workspace validation is unavailable")
	}
	return host.ValidateFactoryCheckpoint(ctx, repo, branch, checkpoint)
}

func (l ImplementationLauncher) ValidateImplementationHandoff(ctx context.Context, repoRoot, branch, previousPRURL, prURL string, policy model.FactoryAttemptPolicy) error {
	remote := forge.Remote{Type: forge.RemoteType(policy.DeliveryRemoteType), Host: policy.DeliveryRemoteHost, Repo: policy.DeliveryRemoteRepo}
	client, ok := l.m.deps.ResolveForge(remote)
	if !ok || remote.Repo == "" {
		return errors.New("factory delivery target is unavailable")
	}
	pr, err := lookupFactoryPR(ctx, client, remote.Repo, prURL)
	if err != nil {
		return err
	}
	if pr.Status == "draft" {
		return errors.New("pull request must be ready for review")
	}
	if policy.Delivery {
		valid := pr.Status == "open" && pr.Branch == branch && pr.BaseBranch == policy.TargetBranch
		if policy.ForceComplete {
			valid = pr.Status == "merged" && pr.BaseBranch == policy.TargetBranch
		}
		if !valid {
			return errors.New("delivery pull request must be open and target the recorded branch")
		}
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
	owner := l.m.deps.Router().ForDir(repoRoot)
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

func (l ImplementationLauncher) ObserveImplementationDelivery(ctx context.Context, prURL string, policy model.FactoryAttemptPolicy) (string, string, error) {
	remote := forge.Remote{Type: forge.RemoteType(policy.DeliveryRemoteType), Host: policy.DeliveryRemoteHost, Repo: policy.DeliveryRemoteRepo}
	client, ok := l.m.deps.ResolveForge(remote)
	if !ok || remote.Repo == "" {
		return "", "", errors.New("factory delivery target is unavailable")
	}
	pr, err := lookupFactoryPR(ctx, client, remote.Repo, prURL)
	return pr.Status, pr.HeadSHA, err
}

func (l ImplementationLauncher) ResolveImplementationBranch(ctx context.Context, repoRoot, branch, previousPRURL string, policy model.FactoryAttemptPolicy) (string, string, error) {
	if previousPRURL == "" {
		return branch, "", nil
	}
	remote := forge.Remote{Type: forge.RemoteType(policy.DeliveryRemoteType), Host: policy.DeliveryRemoteHost, Repo: policy.DeliveryRemoteRepo}
	client, ok := l.m.deps.ResolveForge(remote)
	if !ok || remote.Repo == "" {
		return "", "", errors.New("factory delivery target is unavailable")
	}
	previous, err := lookupFactoryPR(ctx, client, remote.Repo, previousPRURL)
	if err != nil {
		return "", "", err
	}
	if previous.Status == "open" && validFactoryBranch(branch, previous.Branch) {
		if err := client.ConvertPRToDraft(ctx, remote.Repo, previous.Number); err != nil {
			return "", "", fmt.Errorf("convert Factory pull request to draft: %w", err)
		}
		previous.Status = "draft"
	}
	switch previous.Status {
	case "open", "draft":
		if validFactoryBranch(branch, previous.Branch) {
			return previous.Branch, "", nil
		}
	case "closed", "merged":
		if next, ok := nextFactoryBranch(branch, previous.Branch); ok {
			if previous.Status == "closed" && previous.HeadSHA == "" {
				return "", "", errors.New("factory pull request has no head commit")
			}
			branches, err := l.m.deps.Router().ForDir(repoRoot).GitBranches(ctx, repoRoot)
			if err != nil {
				return "", "", err
			}
			for _, existing := range branches {
				if existing == next {
					return "", "", errors.New("replacement Factory branch already exists")
				}
			}
			if previous.Status == "merged" {
				return next, "", nil
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

func (l ImplementationLauncher) LaunchImplementationSession(ctx context.Context, req factory.ImplementationSessionRequest) (factory.PlanningSession, error) {
	if req.Profile != "factory-implement/v1" {
		return factory.PlanningSession{}, errors.New("invalid Factory implementation profile")
	}
	owner := l.m.deps.Router().ForDir(req.Repository)
	if l.m.deps.Store != nil {
		upstreams, err := owner.ProjectUpstreams(ctx, req.Repository)
		if err != nil {
			return factory.PlanningSession{}, fmt.Errorf("resolve Factory delivery target: %w", err)
		}
		var delivery forge.Remote
		for _, remote := range upstreams.Remotes {
			if _, ok := l.m.deps.ResolveForge(remote); ok && (delivery.Repo == "" || remote.Name == "origin") {
				delivery = remote
				if remote.Name == "origin" {
					break
				}
			}
		}
		if delivery.Repo == "" {
			return factory.PlanningSession{}, errors.New("no supported Factory delivery remote")
		}
		if err := l.m.deps.Store.SetFactoryAttemptDeliveryTarget(ctx, req.AttemptID, string(delivery.Type), delivery.Host, delivery.Repo, time.Now()); err != nil {
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
	if err := PlanningLauncher(l).ensureMCP(ctx, ensured); err != nil {
		return factory.PlanningSession{}, err
	}
	rules := ImplementationRules(req.Verification)
	for _, project := range req.Projects {
		if project != req.Repository {
			pattern := filepath.Join(project, "**")
			rules = append(rules, platforms.PermissionRule{Permission: "external_directory", Pattern: pattern, Action: "allow"}, platforms.PermissionRule{Permission: "edit", Pattern: pattern, Action: "deny"})
		}
	}
	local := owner.RemoteID() == "local"
	if local {
		// Keep exceptions after the catch-all: OpenCode uses the last match.
		rules = append(rules, ExternalDirectoryRules()...)
	}
	rules = append(rules, ScratchRules(req.Repository, local)...)
	rules = append(rules, platforms.PermissionRule{Permission: "mcp_factory", Pattern: "factory", Action: "allow"})
	// Append Epic-level user rules last so they override the profile defaults.
	for _, r := range req.PermissionRules {
		rules = append(rules, platforms.PermissionRule{Permission: r.Permission, Pattern: r.Pattern, Action: r.Action})
	}
	created, err := owner.CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{ProjectDir: req.Repository, Branch: req.Branch, BaseRef: req.BaseRef, MustCreateBranch: req.BaseRef != "", Title: "IMPL " + req.WorkID + " (@factory)", NewBranch: true, PermissionRules: rules})
	if err != nil {
		return factory.PlanningSession{}, fmt.Errorf("create Factory worktree: %w", err)
	}
	return factory.PlanningSession{Platform: "opencode", ID: created.SessionID}, nil
}

func (l ImplementationLauncher) PromptImplementationSession(ctx context.Context, session factory.PlanningSession, req factory.ImplementationSessionRequest) error {
	body := req.Prompt
	if body == "" {
		stage := "implementation"
		if req.Delivery {
			stage = "delivery"
		}
		body = factory.DefaultFormulaPrompts()[stage]
	}
	prompt := fmt.Sprintf(`Implement Factory Issue %s in Work Epic %s.

Title: %s

Task:
%s

%s

Factory protocol: Work only on this Issue in the assigned worktree. All implementation Issues in this Work Epic use the shared branch %s and run sequentially. Keep this assigned branch. Do not create a pull request during implementation; Factory schedules a separate final delivery session.

Before completion, leave the shared worktree on a clean commit, push the branch with an upstream, then use the factory MCP action complete_attempt with attempt_id %s, attempt_token %s, and a concise summary. Omit pr_url. Factory verifies and records the commit checkpoint. If you cannot safely continue, use request_recovery with the same attempt ID and token instead of guessing.`, req.WorkID, req.EpicID, req.Title, req.Description, body, req.Branch, req.AttemptID, req.AgentToken)
	if req.Delivery {
		prompt = fmt.Sprintf(`Deliver Factory Work Epic %s from the assigned shared branch %s into %s.

All required implementation Issues have completed.

%s

Factory protocol: Search the forge for an existing open pull request from this exact branch into the target branch before creating one; reuse it on retries. Create the final pull request if none exists and mark it ready for review. Never merge it or reuse a merged/closed pull request. If the branch was merged or changed unexpectedly, request recovery instead of guessing.

Leave the worktree clean and pushed, then call factory complete_attempt with attempt_id %s, attempt_token %s, summary describing the delivery checks, and pr_url set to the final pull request URL. Factory validates the branch, target, and HEAD. If delivery is blocked, use request_recovery with the same attempt credentials.`, req.EpicID, req.Branch, req.TargetBranch, body, req.AttemptID, req.AgentToken)
		if req.LeftOut != "" {
			prompt = strings.Replace(prompt, "All required implementation Issues have completed.", "The user chose to deliver as is: verification was waived and this work was left out. Add a \"Not done\" section to the pull request description listing each of these with one line on what is missing (read them with factory issues):\n"+req.LeftOut, 1)
		}
	}
	model := req.Model
	if req.Verification {
		prompt = factoryVerificationPrompt(req, body)
		model = l.verificationModel(ctx, session, model)
	}
	if !req.Delivery && !req.Verification {
		prompt += "\n\nIf you discover missing work, use factory issues to find the implementation parent and mutate_graph to propose tickets or dependency changes. Submit the complete change in one call with action batch, epicId, mutations, and rationaleMarkdown summarizing what changed and why in Markdown. The rationale is shown beside graph approval. The revised graph needs human approval before new work runs. Finish only your assigned Issue; use request_recovery if the gap prevents you from continuing. Never approve the graph yourself."
	}
	prompt += "\n\n" + factoryIssueProse
	return l.m.deps.Sessions.SendMessage(ctx, session.Platform, platforms.SendMessageRequest{SessionID: session.ID, Message: prompt, Model: model})
}

func (l ImplementationLauncher) StopImplementationSession(ctx context.Context, session factory.PlanningSession) error {
	return l.m.deps.Sessions.Abort(ctx, session.Platform, platforms.AbortRequest{SessionID: session.ID})
}

func (l ImplementationLauncher) RespondImplementationPermission(ctx context.Context, session factory.PlanningSession, permissionID, reply string) error {
	return l.m.deps.Sessions.RespondPermission(ctx, session.Platform, platforms.RespondPermissionRequest{SessionID: session.ID, PermissionID: permissionID, Reply: reply})
}

func (l ImplementationLauncher) ImplementationPermissionPending(ctx context.Context, session factory.PlanningSession, permissionID string) (bool, error) {
	platform, ok := l.m.deps.Registry.Get(platforms.ID(session.Platform))
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

func (l ImplementationLauncher) ProbeImplementationSession(ctx context.Context, session factory.PlanningSession) (bool, error) {
	platform, ok := l.m.deps.Registry.Get(platforms.ID(session.Platform))
	if !ok {
		return false, errors.New("implementation platform is unavailable")
	}
	return platform.Owns(ctx, session.ID), nil
}
