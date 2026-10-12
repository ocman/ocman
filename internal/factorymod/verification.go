package factorymod

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type factoryCheckHost interface {
	RunFactoryChecks(ctx context.Context, repoRoot, branch, target string, commands []string) (bool, string, error)
}

// ImplementationRules is the frozen session profile. Validators get
// the same tools minus edit: they judge code they did not write and must
// not "fix" it into passing. Bash stays so they can run checks; the
// unchanged-HEAD rule in CompleteAttempt catches writes made through it.
func ImplementationRules(verification bool) []platforms.PermissionRule {
	edit := "allow"
	if verification {
		edit = "deny"
	}
	return []platforms.PermissionRule{{Permission: "read", Pattern: "*", Action: "allow"}, {Permission: "glob", Pattern: "*", Action: "allow"}, {Permission: "grep", Pattern: "*", Action: "allow"}, {Permission: "list", Pattern: "*", Action: "allow"}, {Permission: "bash", Pattern: "*", Action: "allow"}, {Permission: "edit", Pattern: "*", Action: edit}, {Permission: "task", Pattern: "*", Action: "allow"}, {Permission: "external_directory", Pattern: "*", Action: "ask"}}
}

// ScratchRules pre-allows the directories Factory sessions reached for
// in practice and users always approved: scratch space and sibling
// worktrees. Bash can already reach them; asking only interrupted the user.
func ScratchRules(repo string, local bool) []platforms.PermissionRule {
	dirs := []string{"/tmp", "/private/tmp", filepath.Join(filepath.Dir(filepath.Clean(repo)), ".worktrees")}
	if local {
		// TMPDIR is per host; a remote's differs from the hub's.
		dirs = append(dirs, os.TempDir())
		if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
			dirs = append(dirs, resolved)
		}
	}
	rules := make([]platforms.PermissionRule, 0, len(dirs))
	for _, dir := range dirs {
		if strings.ContainsAny(dir, "*?[") {
			continue
		}
		rules = append(rules, platforms.PermissionRule{Permission: "external_directory", Pattern: filepath.Join(dir, "**"), Action: "allow"})
	}
	return rules
}

func factoryVerificationPrompt(req factory.ImplementationSessionRequest, body string) string {
	return fmt.Sprintf(`Verify Factory step %s in Work Epic %s on the assigned branch %s (target %s).

%s

Approved Issues and their acceptance criteria:
%s

Factory protocol: You are a read-only validator. You did not write this code; judge it, do not fix it. Do not edit, commit, push, or create or merge a pull request: Factory rejects completion if the shared branch moved.

If you find missing work, use factory issues to find the implementation phase and mutate_graph to propose follow-up Issues. Submit the complete change in one call with action batch, epicId, mutations, and rationaleMarkdown summarizing what changed and why in Markdown. The rationale is shown beside graph approval. The amended graph requires human approval; never approve it yourself. Factory runs fresh verification after the added work. If your current scope cannot pass, request_recovery with its failing criteria.

1. Review the combined diff against %s. Check every acceptance criterion above against the code and by running the relevant checks.
2. Look for work that passes checks without doing the task: deleted or skipped tests, weakened or removed assertions, lowered thresholds, new lint or type suppressions, hard-coded expected values, and stubs.
3. If anything fails, call request_recovery with attempt_id %s and attempt_token %s, listing each failing criterion or check. Do not claim success.
4. Otherwise call complete_attempt with the same credentials, omit pr_url, and give a summary that lists each acceptance criterion as PASS or FAIL with evidence.

If this step declares Formula checks, complete_attempt answers checks_running: end your turn. Factory runs the commands itself and sends the results to this session; decide again with those results.`, req.WorkID, req.EpicID, req.Branch, req.TargetBranch, body, req.Criteria, req.TargetBranch, req.AttemptID, req.AgentToken)
}

// verificationModel keeps an explicitly frozen model and otherwise prefers
// the strong planning model, so the validator is not the implementer's twin.
func (l ImplementationLauncher) verificationModel(ctx context.Context, session factory.PlanningSession, frozen string) string {
	if frozen != "" {
		return frozen
	}
	adapter, ok := l.m.deps.Registry.Get(platforms.ID(session.Platform))
	if !ok {
		return ""
	}
	catalog, err := adapter.SessionModels(ctx, session.ID)
	if err != nil || catalog == nil {
		return ""
	}
	// ponytail: strong model, else the runtime default; exclude the
	// implementation model explicitly if the two collide in practice.
	return factoryStrongModel(catalog.Models)
}

func (l ImplementationLauncher) RunVerificationChecks(ctx context.Context, repo, branch, target string, commands []string) (bool, string, error) {
	host, ok := l.m.deps.Router().ForDir(repo).(factoryCheckHost)
	if !ok {
		return false, "", errors.New("factory verification checks are unavailable on this host")
	}
	return host.RunFactoryChecks(ctx, repo, branch, target, commands)
}

func (l ImplementationLauncher) NotifyImplementationSession(ctx context.Context, session factory.PlanningSession, message string) error {
	return l.m.deps.Sessions.SendMessage(ctx, session.Platform, platforms.SendMessageRequest{SessionID: session.ID, Message: message})
}

// ImplementationActivity reports when the session or a direct subagent last
// changed, from the cached session snapshot, and whether a permission or
// question prompt is waiting on a human.
func (l ImplementationLauncher) ImplementationActivity(ctx context.Context, session factory.PlanningSession) (time.Time, bool, error) {
	platform, ok := l.m.deps.Registry.Get(platforms.ID(session.Platform))
	if !ok {
		return time.Time{}, false, errors.New("implementation platform is unavailable")
	}
	sessions, err := platform.Sessions(ctx, "", 0)
	if err != nil {
		return time.Time{}, false, err
	}
	var latest int64
	found := false
	for _, candidate := range sessions {
		if candidate.ID == session.ID || candidate.ParentID == session.ID {
			found = found || candidate.ID == session.ID
			latest = max(latest, candidate.TimeUpdated)
		}
	}
	if !found {
		return time.Time{}, false, errors.New("implementation session is unavailable")
	}
	permissions := platform.ListPermissions
	if live, ok := platform.(platforms.PermissionRefresher); ok {
		// The observed-prompt cache is empty after a restart; ask the instance.
		permissions = live.RefreshPermissions
	}
	for _, list := range []func(context.Context, string) ([]platforms.LivePrompt, error){permissions, platform.ListQuestions} {
		prompts, err := list(ctx, session.ID)
		if err != nil {
			return time.Time{}, false, err
		}
		if len(prompts) > 0 {
			return time.UnixMilli(latest), true, nil
		}
	}
	return time.UnixMilli(latest), false, nil
}
