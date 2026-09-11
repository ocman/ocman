package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

type factoryPlanningLauncher struct{ server *Server }

func (l factoryPlanningLauncher) LaunchPlanningSession(ctx context.Context, req factory.PlanningSessionRequest) (factory.PlanningSession, error) {
	return l.launchReadOnlySession(ctx, req, platforms.PermissionRule{Permission: "mcp_factory", Pattern: "factory", Action: "allow"})
}

func (l factoryPlanningLauncher) launchReadOnlySession(ctx context.Context, req factory.PlanningSessionRequest, mcpRule platforms.PermissionRule) (factory.PlanningSession, error) {
	var platformID string
	for _, candidate := range l.server.registry.Platforms() {
		remoteID, _ := remote.SplitPlatformID(string(candidate.ID()))
		if remoteID != "" || !candidate.Available(ctx) || !candidate.Capabilities().PermissionRules {
			continue
		}
		if platformID != "" {
			return factory.PlanningSession{}, errors.New("multiple local planning platforms are available")
		}
		platformID = string(candidate.ID())
	}
	if platformID == "" {
		return factory.PlanningSession{}, errors.New("no local planning platform with permission rules is available")
	}
	host := l.server.router().Local()
	ensureReq := hostsvc.EnsureProjectOpencodeRequest{ProjectDir: req.Repository}
	ensured, err := host.EnsureProjectOpencode(ctx, ensureReq)
	if err != nil {
		return factory.PlanningSession{}, fmt.Errorf("ensure planning runtime: %w", err)
	}
	if ensured == nil {
		return factory.PlanningSession{}, errors.New("ensure planning runtime returned no instance")
	}
	if err := l.ensureMCP(ctx, ensured); err != nil {
		return factory.PlanningSession{}, err
	}
	rules := []platforms.PermissionRule{
		{Permission: "read", Pattern: "*", Action: "allow"},
		{Permission: "glob", Pattern: "*", Action: "allow"},
		{Permission: "grep", Pattern: "*", Action: "allow"},
		{Permission: "list", Pattern: "*", Action: "allow"},
		{Permission: "external_directory", Pattern: "*", Action: "deny"},
	}
	rules = append(rules, factorySkillDirectoryRules()...)
	rules = append(rules,
		platforms.PermissionRule{Permission: "bash", Pattern: "*", Action: "deny"},
		platforms.PermissionRule{Permission: "edit", Pattern: "*", Action: "deny"},
		platforms.PermissionRule{Permission: "task", Pattern: "*", Action: "deny"},
		platforms.PermissionRule{Permission: "webfetch", Pattern: "*", Action: "deny"},
		mcpRule,
	)
	created, err := l.server.sessions.CreateConfigured(ctx, platformID, platforms.CreateSessionRequest{Directory: req.Repository, Title: req.Title, Port: ensured.Port()}, rules)
	if err != nil {
		var cleanup *sessionsvc.ConfiguredSessionCleanupError
		if errors.As(err, &cleanup) {
			return factory.PlanningSession{Platform: platformID, ID: cleanup.SessionID}, fmt.Errorf("create bounded Planning Session: %w", err)
		}
		return factory.PlanningSession{}, fmt.Errorf("create bounded Planning Session: %w", err)
	}
	return factory.PlanningSession{Platform: platformID, ID: created.ID}, nil
}

func (s *Server) launchFactoryUnblockSession(ctx context.Context, epicID, issueID string) (factory.PlanningSession, error) {
	epic, err := s.factory.GetWorkEpic(ctx, epicID)
	if err != nil {
		return factory.PlanningSession{}, err
	}
	issues, err := s.factory.ListIssues(ctx, epicID)
	if err != nil {
		return factory.PlanningSession{}, err
	}
	var issue *factory.Issue
	for i := range issues {
		if issues[i].ID == issueID {
			issue = &issues[i]
			break
		}
	}
	if issue == nil {
		return factory.PlanningSession{}, factory.ErrInvalidRequest
	}
	actionable := issue.DispatchState == string(factory.DispatchBlocked) || (issue.Status == "closed" && (issue.Outcome == "failed" || issue.Outcome == "cancelled"))
	if !actionable {
		return factory.PlanningSession{}, factory.ErrInvalidRequest
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return factory.PlanningSession{}, fmt.Errorf("create unblock token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	launcher := factoryPlanningLauncher{server: s}
	session, err := launcher.launchReadOnlySession(ctx, factory.PlanningSessionRequest{Repository: epic.InitialProject, Title: "Unblock: " + issue.Title}, platforms.PermissionRule{Permission: "mcp_factory_unblock", Pattern: "factory_unblock", Action: "ask"})
	if err != nil {
		return session, err
	}
	s.factoryUnblockTokens.Store(token, epic.ID)
	evidence, _ := json.Marshal(issue)
	prompt := fmt.Sprintf("Investigate why Factory Issue %s in Work Epic %s is blocked. The current evidence is:\n\n```json\n%s\n```\n\nInspect the repository and Factory state without modifying files. Propose the smallest safe fix, explain it in the conversation, then invoke the factory_unblock MCP tool with that exact action and unblock_token %s. Its permission prompt supplies the user-facing Allow and Reject buttons, and the action cannot run before approval. Supported actions are reopen with epic_id and issue_id, or mutate_graph with epic_id and a strict GraphMutation JSON payload. After execution, explain what changed.", issue.ID, epic.ID, evidence, token)
	if err := s.sessions.SendMessage(ctx, session.Platform, platforms.SendMessageRequest{SessionID: session.ID, Message: prompt}); err != nil {
		s.factoryUnblockTokens.Delete(token)
		_ = s.sessions.Dispose(ctx, session.Platform, platforms.DisposeSessionRequest{SessionID: session.ID})
		return session, fmt.Errorf("prompt unblock session: %w", err)
	}
	return session, nil
}

func (s *Server) consumeFactoryUnblock(token, epicID string) bool {
	return s.factoryUnblockTokens.CompareAndDelete(token, epicID)
}

func factorySkillDirectoryRules() []platforms.PermissionRule {
	home, _ := os.UserHomeDir()
	if !filepath.IsAbs(home) {
		return nil
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(configHome) {
		configHome = filepath.Join(home, ".config")
	}
	directories := []string{
		filepath.Join(configHome, "opencode", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".agents", "skills"),
	}
	allowed := make(map[string]struct{})
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			skillDirectory := filepath.Join(directory, entry.Name())
			resolved, err := filepath.EvalSymlinks(skillDirectory)
			if err != nil {
				continue
			}
			info, err := os.Stat(filepath.Join(resolved, "SKILL.md"))
			if err != nil || info.IsDir() || filepath.Dir(resolved) == resolved || strings.ContainsAny(resolved, "*?[") {
				continue
			}
			allowed[resolved] = struct{}{}
		}
	}
	rules := make([]platforms.PermissionRule, 0, len(allowed))
	for directory := range allowed {
		rules = append(rules, platforms.PermissionRule{Permission: "external_directory", Pattern: filepath.Join(directory, "**"), Action: "allow"})
	}
	slices.SortFunc(rules, func(a, b platforms.PermissionRule) int { return strings.Compare(a.Pattern, b.Pattern) })
	return rules
}

func (l factoryPlanningLauncher) ensureMCP(ctx context.Context, ensured *hostsvc.EnsureProjectOpencodeResult) error {
	client := &http.Client{Timeout: 5 * time.Second, Transport: l.server.openCodeAuth.Transport(http.DefaultTransport)}
	loadedURL, err := planningMCPURL(ctx, client, ensured.Endpoint)
	if err != nil {
		return fmt.Errorf("check planning MCP config: %w", err)
	}
	if loadedURL != l.server.mcpServerURL() {
		return fmt.Errorf("planning MCP is configured for %q; restart OpenCode after configuring %s", loadedURL, l.server.mcpServerURL())
	}
	status, err := planningMCPStatus(ctx, client, ensured.Endpoint)
	if err != nil {
		return fmt.Errorf("check planning MCP: %w", err)
	}
	if status == "connected" {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ensured.Endpoint, "/")+"/mcp/ocman/connect", nil)
	if err == nil {
		var response *http.Response
		response, err = client.Do(request)
		if response != nil {
			_ = response.Body.Close()
			if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
				err = fmt.Errorf("status %d", response.StatusCode)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("connect planning MCP: %w", err)
	}
	status, err = planningMCPStatus(ctx, client, ensured.Endpoint)
	if err != nil || status != "connected" {
		return fmt.Errorf("planning MCP is unavailable (status %q): restart OpenCode after configuring %s", status, l.server.mcpServerURL())
	}
	return nil
}

func planningMCPStatus(ctx context.Context, client *http.Client, endpoint string) (string, error) {
	var statuses map[string]struct {
		Status string `json:"status"`
	}
	if err := planningGetJSON(ctx, client, strings.TrimRight(endpoint, "/")+"/mcp", &statuses); err != nil {
		return "", err
	}
	return statuses["ocman"].Status, nil
}

func planningMCPURL(ctx context.Context, client *http.Client, endpoint string) (string, error) {
	var config struct {
		MCP map[string]struct {
			URL string `json:"url"`
		} `json:"mcp"`
	}
	if err := planningGetJSON(ctx, client, strings.TrimRight(endpoint, "/")+"/config", &config); err != nil {
		return "", err
	}
	return config.MCP["ocman"].URL, nil
}

func planningGetJSON(ctx context.Context, client *http.Client, url string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("status %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func (l factoryPlanningLauncher) PromptPlanningSession(ctx context.Context, session factory.PlanningSession, req factory.PlanningSessionRequest) error {
	prompt := fmt.Sprintf("Plan Factory Work Epic %s (planning work %s). Inspect the repository without modifying it. First grill the user: load the grilling skill if available, and either way ask one sharp question at a time until the goal, scope, and non-goals are unambiguous. Do not propose an issue graph until the user tells you to proceed. Then, if the to-tickets skill is available, load it; either way, split the plan into tracer-bullet vertical slices with explicit blocking edges. Factory's proposal approval replaces the skill's tracker publication step. Submit the resulting issue graph with the factory MCP action submit_proposal using epic_id %s, attempt_id %s, and attempt_token %s. Split the work into multiple focused implementation Issues by default. Give every manifest node a stable key, concise title, actionable description, and add explicit edges from each dependent node to its blocker where ordering matters. After submitting, recap the proposed Issues and include a Mermaid flowchart of their dependencies. Tell the user that Approve and start implementation materializes the Plan and begins implementation. End your final response with [Review and approve the plan](/factory/epics/%s)", req.EpicID, req.WorkID, req.EpicID, req.AttemptID, req.AgentToken, req.EpicID)
	return l.server.sessions.SendMessage(ctx, session.Platform, platforms.SendMessageRequest{SessionID: session.ID, Message: prompt})
}

func (l factoryPlanningLauncher) StopPlanningSession(ctx context.Context, session factory.PlanningSession) error {
	return l.server.sessions.Dispose(ctx, session.Platform, platforms.DisposeSessionRequest{SessionID: session.ID})
}

func (l factoryPlanningLauncher) ProbePlanningSession(ctx context.Context, session factory.PlanningSession) (bool, error) {
	platform, ok := l.server.registry.Get(platforms.ID(session.Platform))
	if !ok {
		return false, fmt.Errorf("planning platform %q is unavailable", session.Platform)
	}
	return platform.Owns(ctx, session.ID), nil
}
