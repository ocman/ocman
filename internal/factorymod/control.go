package factorymod

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/sirupsen/logrus"
)

const ControlProtocolVersion = 3

const factoryControlProtocol = `You are the control session for Factory Work Epic %[1]s: %[2]s. The user steers this Epic from here, and Factory posts every decision it needs into this conversation.

- Keep replies short: what happened, what the user can decide, and your recommendation.
- Own resolution of this Epic. Investigate failures and repeated recovery/review cycles, propose a concrete change, and verify its outcome. Never repeat an unsuccessful retry without new evidence or a changed approach. When a loop repeats, offer pause, deliver_as_is, or stop, with the unfinished work named. If the user asks to end it, propose stop immediately rather than requiring another recovery or delivery attempt.
- Read the Epic with the factory MCP tool (get, issues, issue_comments, proposals) and inspect the repositories without modifying them:
- %[3]s
- Never decide for the user and never add work the user did not ask for. Factory agents only implement and report; scope changes are the user's.
- To change the Epic, call the factory_control MCP tool with epic_id %[1]s, an action, its fields, and a one-line summary for the user. A call only proposes: Ocman shows the user a card with Confirm and Dismiss under it. Tell the user the card is waiting and stop; Factory tells you the outcome. Actions:
  - reopen with issue_id: run failed or cancelled work again.
  - claim_plan with issue_id: start the ready planning session. materialize with issue_id: apply the approved plan and start ready work.
  - mutate_graph with mutation_json: one batch (epicId, mutations, rationaleMarkdown) that edits, adds, or deletes Issues. It applies directly, without plan approval.
  - resolve_recovery with recovery_gate_id, decision (resume, retry, or cancel) and an optional response for the paused session.
  - deliver_as_is: stop verification, leave unfinished work out, and deliver what is done.
  - stop: stop all Epic workers and close the Epic without delivery. Preserve files and history; unfinished work is cancelled, never reported as completed. This works even when recovery or delivery cannot proceed.
  - pause or resume: suspend new work or start dispatch again.
  - decide_plan with decision (approve, revise, reject), expected_revision and expected_hash from the current plan gate, optional response and implementation_model.
  - resolve_authority with authority_gate_id and decision (approve or reject).
  - resolve_project with project_gate_id, decision (approve or reject), optional response. Ocman's card displays the requested project and requires the user to explicitly allow local command execution. You cannot supply that acknowledgement.
- Recovery worker sessions are optional evidence, not another conversation the user must manage. Resolve Epic decisions here. Before acting on an old notification, read the current Epic and issues. After a confirmed change, re-read them and explain what remains relevant; never ask for decisions on closed gates or cancelled work.

Reply now with a one-paragraph status of the Epic, including anything waiting for the user.`

type factoryControlStore interface {
	GetFactoryControlSession(context.Context, string) (state.FactoryControlSession, bool, error)
	SetFactoryControlSession(context.Context, state.FactoryControlSession, time.Time) (bool, error)
	CreateFactoryControlAction(context.Context, state.FactoryControlAction, time.Time) error
	GetFactoryControlAction(context.Context, string, string) (state.FactoryControlAction, bool, error)
	TransitionFactoryControlAction(context.Context, string, string, string, string, string) (bool, error)
	DismissFactoryControlAlternatives(context.Context, string, string) error
}

func (m *Module) factoryControlStore() (factoryControlStore, error) {
	if m.deps.Store == nil {
		return nil, factory.ErrFactoryUnavailable
	}
	return m.deps.Store, nil
}

type factoryControlEnsured struct {
	control state.FactoryControlSession
	created bool
}

// EnsureControlSession returns the Epic's control session, creating it
// (or replacing one that no longer exists) on first use. Concurrent callers
// for one Epic share a single launch.
func (m *Module) EnsureControlSession(ctx context.Context, epicID string) (state.FactoryControlSession, bool, error) {
	result, err, _ := m.controlFlight.Do(epicID, func() (any, error) {
		control, created, err := m.ensureFactoryControlSessionOnce(context.WithoutCancel(ctx), epicID)
		return factoryControlEnsured{control, created}, err
	})
	ensured, _ := result.(factoryControlEnsured)
	return ensured.control, ensured.created, err
}

func (m *Module) ensureFactoryControlSessionOnce(ctx context.Context, epicID string) (state.FactoryControlSession, bool, error) {
	store, err := m.factoryControlStore()
	if err != nil {
		return state.FactoryControlSession{}, false, err
	}
	epic, err := m.svc.GetWorkEpic(ctx, epicID)
	if err != nil {
		return state.FactoryControlSession{}, false, err
	}
	if epic.Status == "closed" {
		return state.FactoryControlSession{}, false, fmt.Errorf("%w: the Epic is closed", factory.ErrInvalidRequest)
	}
	projects := make([]string, 0, len(epic.Projects))
	for _, project := range epic.Projects {
		projects = append(projects, project.Path)
	}
	if len(projects) == 0 {
		projects = []string{epic.InitialProject}
	}
	prompt := factoryIssueProse + "\n\n" + fmt.Sprintf(factoryControlProtocol, epicID, epic.Goal, strings.Join(projects, "\n- "))
	control, found, err := store.GetFactoryControlSession(ctx, epicID)
	if err != nil {
		return state.FactoryControlSession{}, false, err
	}
	if found {
		alive, err := m.factoryControlAlive(ctx, control)
		if err != nil || alive {
			if err == nil && control.ProtocolVersion < ControlProtocolVersion {
				err = m.deps.Enqueue(ctx, control.Platform, false, platforms.SendMessageRequest{SessionID: control.SessionID, Message: "Updated Factory control instructions. These replace the previous control protocol.\n\n" + prompt, Model: epic.Models.Plan})
				if err == nil {
					control.ProtocolVersion = ControlProtocolVersion
					_, err = store.SetFactoryControlSession(ctx, control, time.Now())
				}
			}
			return control, false, err
		}
	}
	launcher := PlanningLauncher{m}
	session, err := launcher.launchReadOnlySession(ctx, factory.PlanningSessionRequest{EpicID: epicID, Platform: epic.Platform, Repository: epic.InitialProject, Projects: projects, Title: "Control: " + epic.Goal + " (@factory)"},
		platforms.PermissionRule{Permission: "mcp_factory", Pattern: "factory", Action: "allow"},
		platforms.PermissionRule{Permission: "mcp_factory_control", Pattern: "factory_control", Action: "allow"})
	if err != nil {
		return state.FactoryControlSession{}, false, err
	}
	if err := m.deps.Sessions.SendMessage(ctx, session.Platform, platforms.SendMessageRequest{SessionID: session.ID, Message: prompt, Model: epic.Models.Plan}); err != nil {
		// A lost response may mean the prompt was accepted: stop the turn,
		// keep the history, and never adopt a session without its protocol.
		_ = launcher.StopPlanningSession(ctx, session)
		return state.FactoryControlSession{}, false, fmt.Errorf("start control session: %w", err)
	}
	control = state.FactoryControlSession{EpicID: epicID, Platform: session.Platform, SessionID: session.ID, ProtocolVersion: ControlProtocolVersion}
	adopted, err := store.SetFactoryControlSession(ctx, control, time.Now())
	if err == nil && !adopted {
		// The Epic closed meanwhile: stop the session and keep it out of view.
		_ = launcher.StopPlanningSession(ctx, session)
		_ = m.deps.Store.ArchiveSession(ctx, session.Platform, session.ID, math.MaxInt64)
		err = fmt.Errorf("%w: the Epic is closed", factory.ErrInvalidRequest)
	}
	return control, adopted, err
}

// factoryControlAlive replaces a session only when it is known to be gone; a
// transient read error must not orphan the user's conversation.
func (m *Module) factoryControlAlive(ctx context.Context, control state.FactoryControlSession) (bool, error) {
	platform, ok := m.deps.Registry.Get(platforms.ID(control.Platform))
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
	_, err := reader.SessionLifecycle(ctx, control.SessionID)
	if errors.Is(err, platforms.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// PostControl hands Factory news to the Epic's control session. It is
// queued, so it never interleaves into a turn there, including the first.
func (m *Module) PostControl(ctx context.Context, epicID, message string) (state.FactoryControlSession, error) {
	epic, err := m.svc.GetWorkEpic(ctx, epicID)
	if err != nil {
		return state.FactoryControlSession{}, err
	}
	control, created, err := m.EnsureControlSession(ctx, epicID)
	if err != nil {
		return control, err
	}
	return control, m.deps.Enqueue(ctx, control.Platform, created, platforms.SendMessageRequest{SessionID: control.SessionID, Message: message, Model: epic.Models.Plan})
}

// AttentionToControl posts the decisions Factory newly needs into the
// Epic's control session, off the dispatch path.
// ponytail: a failed post is only logged; the Inbox keeps the decision and a
// new control session starts by reporting everything waiting for the user.
func (m *Module) AttentionToControl(ctx context.Context, epicID string, actions []string) {
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		message := "Factory needs your decision:\n- " + strings.Join(actions, "\n- ") + "\n\nExplain each briefly and recommend what to do."
		if _, err := m.PostControl(ctx, epicID, message); err != nil {
			logrus.WithError(err).WithField("epic", epicID).Warn("factory: control session update failed")
		}
	}()
}

// discussFactoryIssue asks the control session to look into one Issue.
func (m *Module) discussFactoryIssue(ctx context.Context, epicID, issueID string) (state.FactoryControlSession, error) {
	issues, err := m.svc.ListIssues(ctx, epicID)
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
		return m.PostControl(ctx, epicID, fmt.Sprintf("Look into Issue %s (%s). Its current state:\n\n```json\n%s\n```\n\nExplain what went wrong and propose the smallest safe fix.", issue.Title, issue.ID, evidence))
	}
	return state.FactoryControlSession{}, factory.ErrInvalidRequest
}

// factoryControlActions lists what a control proposal may do and the fields
// each requires.
var factoryControlActions = map[string][]string{
	"reopen":            {"issue_id"},
	"claim_plan":        {"issue_id"},
	"materialize":       {"issue_id"},
	"mutate_graph":      {"mutation_json"},
	"resolve_recovery":  {"recovery_gate_id", "decision"},
	"deliver_as_is":     nil,
	"pause":             nil,
	"resume":            nil,
	"stop":              nil,
	"decide_plan":       {"decision", "expected_revision", "expected_hash"},
	"resolve_authority": {"authority_gate_id", "decision"},
	"resolve_project":   {"project_gate_id", "decision"},
}

// ProposeControl records an agent's proposal; it does nothing until
// the user confirms its card.
func (m *Module) ProposeControl(ctx context.Context, epicID, action string, args map[string]string, summary string) (string, error) {
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
	if err := validateFactoryControlDecision(action, args); err != nil {
		return "", err
	}
	epic, err := m.svc.GetWorkEpic(ctx, epicID)
	if err != nil {
		return "", err
	}
	requestedProject := ""
	if action == "resolve_project" && args["decision"] == "approve" {
		issues, err := m.svc.ListIssues(ctx, epicID)
		if err != nil {
			return "", err
		}
		for _, issue := range issues {
			if issue.ProjectRequest != nil && issue.ProjectRequest.IssueID == args["project_gate_id"] {
				requestedProject = issue.ProjectRequest.RequestedProject
				break
			}
		}
		if requestedProject == "" {
			return "", fmt.Errorf("%w: project gate is not part of this Epic", factory.ErrInvalidRequest)
		}
	}
	if epic.Status == "closed" {
		return "", fmt.Errorf("%w: the Epic is closed", factory.ErrInvalidRequest)
	}
	store, err := m.factoryControlStore()
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
	return id, store.CreateFactoryControlAction(ctx, state.FactoryControlAction{ID: id, EpicID: epicID, Action: action, Args: raw, Summary: strings.TrimSpace(summary), RequestedProject: requestedProject}, time.Now())
}
