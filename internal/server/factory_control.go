package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/sirupsen/logrus"
)

const factoryControlProtocol = `You are the control session for Factory Work Epic %[1]s: %[2]s. The user steers this Epic from here, and Factory posts every decision it needs into this conversation.

- Keep replies short: what happened, what the user can decide, and your recommendation.
- Read the Epic with the factory MCP tool (get, issues, issue_comments, proposals) and inspect the repositories without modifying them:
- %[3]s
- Never decide for the user and never add work the user did not ask for. Factory agents only implement and report; scope changes are the user's.
- To change the Epic, call the factory_control MCP tool with epic_id %[1]s, an action, its fields, and a one-line summary for the user. A call only proposes: Ocman shows the user a card with Confirm and Dismiss under it. Tell the user the card is waiting and stop; Factory tells you the outcome. Actions:
  - reopen with issue_id: run failed or cancelled work again.
  - mutate_graph with mutation_json: one batch (epicId, mutations, rationaleMarkdown) that edits, adds, or deletes Issues. It applies directly, without plan approval.
  - resolve_recovery with recovery_gate_id, decision (resume, retry, or cancel) and an optional response for the paused session.
  - deliver_as_is: stop verification, leave unfinished work out, and deliver what is done.
  - pause or resume the Epic.
- Plan approval stays on the Epic page's Plan approval card.

Reply now with a one-paragraph status of the Epic, including anything waiting for the user.`

type factoryControlStore interface {
	GetFactoryControlSession(context.Context, string) (state.FactoryControlSession, bool, error)
	SetFactoryControlSession(context.Context, state.FactoryControlSession, time.Time) error
	CreateFactoryControlAction(context.Context, state.FactoryControlAction, time.Time) error
	GetFactoryControlAction(context.Context, string, string) (state.FactoryControlAction, bool, error)
	TransitionFactoryControlAction(context.Context, string, string, string, string, string) (bool, error)
}

func (s *Server) factoryControlStore() (factoryControlStore, error) {
	if s.stateDB == nil {
		return nil, factory.ErrFactoryUnavailable
	}
	return s.stateDB, nil
}

type factoryControlEnsured struct {
	control state.FactoryControlSession
	created bool
}

// ensureFactoryControlSession returns the Epic's control session, creating it
// (or replacing one that no longer exists) on first use. Concurrent callers
// for one Epic share a single launch.
func (s *Server) ensureFactoryControlSession(ctx context.Context, epicID string) (state.FactoryControlSession, bool, error) {
	result, err, _ := s.factoryControlFlight.Do(epicID, func() (any, error) {
		control, created, err := s.ensureFactoryControlSessionOnce(context.WithoutCancel(ctx), epicID)
		return factoryControlEnsured{control, created}, err
	})
	ensured, _ := result.(factoryControlEnsured)
	return ensured.control, ensured.created, err
}

func (s *Server) ensureFactoryControlSessionOnce(ctx context.Context, epicID string) (state.FactoryControlSession, bool, error) {
	store, err := s.factoryControlStore()
	if err != nil {
		return state.FactoryControlSession{}, false, err
	}
	epic, err := s.factory.GetWorkEpic(ctx, epicID)
	if err != nil {
		return state.FactoryControlSession{}, false, err
	}
	if epic.Status == "closed" {
		return state.FactoryControlSession{}, false, fmt.Errorf("%w: the Epic is closed", factory.ErrInvalidRequest)
	}
	control, found, err := store.GetFactoryControlSession(ctx, epicID)
	if err != nil {
		return state.FactoryControlSession{}, false, err
	}
	if found {
		alive, err := s.factoryControlAlive(ctx, control)
		if err != nil || alive {
			return control, false, err
		}
	}
	projects := make([]string, 0, len(epic.Projects))
	for _, project := range epic.Projects {
		projects = append(projects, project.Path)
	}
	if len(projects) == 0 {
		projects = []string{epic.InitialProject}
	}
	launcher := factoryPlanningLauncher{server: s}
	session, err := launcher.launchReadOnlySession(ctx, factory.PlanningSessionRequest{Repository: epic.InitialProject, Projects: projects, Title: "Control: " + epic.Goal + " (@factory)"},
		platforms.PermissionRule{Permission: "mcp_factory", Pattern: "factory", Action: "allow"},
		platforms.PermissionRule{Permission: "mcp_factory_control", Pattern: "factory_control", Action: "allow"})
	if err != nil {
		return state.FactoryControlSession{}, false, err
	}
	prompt := factoryIssueProse + "\n\n" + fmt.Sprintf(factoryControlProtocol, epicID, epic.Goal, strings.Join(projects, "\n- "))
	if err := s.sessions.SendMessage(ctx, session.Platform, platforms.SendMessageRequest{SessionID: session.ID, Message: prompt, Model: s.factoryControlModel(ctx, session)}); err != nil {
		// A lost response may mean the prompt was accepted: stop the turn,
		// keep the history, and never adopt a session without its protocol.
		_ = launcher.StopPlanningSession(ctx, session)
		return state.FactoryControlSession{}, false, fmt.Errorf("start control session: %w", err)
	}
	control = state.FactoryControlSession{EpicID: epicID, Platform: session.Platform, SessionID: session.ID}
	return control, true, store.SetFactoryControlSession(ctx, control, time.Now())
}

// factoryControlAlive replaces a session only when it is known to be gone; a
// transient read error must not orphan the user's conversation.
func (s *Server) factoryControlAlive(ctx context.Context, control state.FactoryControlSession) (bool, error) {
	platform, ok := s.registry.Get(platforms.ID(control.Platform))
	if !ok {
		return false, nil
	}
	if platform.Owns(ctx, control.SessionID) {
		return true, nil
	}
	reader, ok := platform.(platforms.LifecycleReader)
	if !ok {
		return false, nil
	}
	if _, err := reader.SessionLifecycle(ctx, control.SessionID); err != nil && !errors.Is(err, platforms.ErrNotFound) {
		return false, err
	}
	return false, nil
}

func (s *Server) factoryControlModel(ctx context.Context, session factory.PlanningSession) string {
	if adapter, ok := s.registry.Get(platforms.ID(session.Platform)); ok {
		if catalog, err := adapter.SessionModels(ctx, session.ID); err == nil && catalog != nil {
			return factoryStrongModel(catalog.Models)
		}
	}
	return ""
}

// postFactoryControl hands Factory news to the Epic's control session. It is
// queued, so it never interleaves into a turn there, including the first.
func (s *Server) postFactoryControl(ctx context.Context, epicID, message string) (state.FactoryControlSession, error) {
	control, created, err := s.ensureFactoryControlSession(ctx, epicID)
	if err != nil {
		return control, err
	}
	session := factory.PlanningSession{Platform: control.Platform, ID: control.SessionID}
	return control, s.queueSvc().Enqueue(ctx, control.Platform, created, platforms.SendMessageRequest{SessionID: control.SessionID, Message: message, Model: s.factoryControlModel(ctx, session)})
}

// factoryAttentionToControl posts the decisions Factory newly needs into the
// Epic's control session, off the dispatch path.
// ponytail: a failed post is only logged; the Inbox keeps the decision and a
// new control session starts by reporting everything waiting for the user.
func (s *Server) factoryAttentionToControl(ctx context.Context, epicID string, actions []string) {
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		message := "Factory needs your decision:\n- " + strings.Join(actions, "\n- ") + "\n\nExplain each briefly and recommend what to do."
		if _, err := s.postFactoryControl(ctx, epicID, message); err != nil {
			logrus.WithError(err).WithField("epic", epicID).Warn("factory: control session update failed")
		}
	}()
}

// discussFactoryIssue asks the control session to look into one Issue.
func (s *Server) discussFactoryIssue(ctx context.Context, epicID, issueID string) (state.FactoryControlSession, error) {
	issues, err := s.factory.ListIssues(ctx, epicID)
	if err != nil {
		return state.FactoryControlSession{}, err
	}
	for _, issue := range issues {
		if issue.ID != issueID {
			continue
		}
		evidence, err := json.MarshalIndent(issue, "", "  ")
		if err != nil {
			return state.FactoryControlSession{}, err
		}
		return s.postFactoryControl(ctx, epicID, fmt.Sprintf("Look into Issue %s (%s). Its current state:\n\n```json\n%s\n```\n\nExplain what went wrong and propose the smallest safe fix.", issue.Title, issue.ID, evidence))
	}
	return state.FactoryControlSession{}, factory.ErrInvalidRequest
}

// factoryControlActions lists what a control proposal may do and the fields
// each requires.
var factoryControlActions = map[string][]string{
	"reopen":           {"issue_id"},
	"mutate_graph":     {"mutation_json"},
	"resolve_recovery": {"recovery_gate_id", "decision"},
	"deliver_as_is":    nil,
	"pause":            nil,
	"resume":           nil,
}

// proposeFactoryControl records an agent's proposal; it does nothing until
// the user confirms its card.
func (s *Server) proposeFactoryControl(ctx context.Context, epicID, action string, args map[string]string, summary string) (string, error) {
	required, ok := factoryControlActions[action]
	if !ok {
		return "", fmt.Errorf("%w: unsupported factory control action", factory.ErrInvalidRequest)
	}
	for _, field := range required {
		if strings.TrimSpace(args[field]) == "" {
			return "", fmt.Errorf("%w: %s is required", factory.ErrInvalidRequest, field)
		}
	}
	if strings.TrimSpace(summary) == "" {
		return "", fmt.Errorf("%w: summary is required", factory.ErrInvalidRequest)
	}
	if action == "mutate_graph" {
		if _, err := decodeFactoryControlMutation(args["mutation_json"]); err != nil {
			return "", err
		}
	}
	if decision := args["decision"]; action == "resolve_recovery" && decision != "resume" && decision != "retry" && decision != "cancel" {
		return "", fmt.Errorf("%w: decision must be resume, retry, or cancel", factory.ErrInvalidRequest)
	}
	epic, err := s.factory.GetWorkEpic(ctx, epicID)
	if err != nil {
		return "", err
	}
	if epic.Status == "closed" {
		return "", fmt.Errorf("%w: the Epic is closed", factory.ErrInvalidRequest)
	}
	store, err := s.factoryControlStore()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	id := "fca_" + hex.EncodeToString(idBytes)
	return id, store.CreateFactoryControlAction(ctx, state.FactoryControlAction{ID: id, EpicID: epicID, Action: action, Args: raw, Summary: strings.TrimSpace(summary)}, time.Now())
}
