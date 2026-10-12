package factorymod

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/sirupsen/logrus"
)

// ControlRunTimeout bounds running a confirmed proposal; a variable so
// tests can make it expire.
var ControlRunTimeout = 5 * time.Minute

// ponytail: serialize confirmed controls; use per-Epic locks if contention matters.
var factoryControlDecisionMu sync.Mutex

func decodeFactoryControlMutation(raw string) (factory.GraphMutation, error) {
	var mutation factory.GraphMutation
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&mutation) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return mutation, fmt.Errorf("%w: mutation_json is invalid", factory.ErrInvalidRequest)
	}
	return mutation, nil
}

// handleFactoryControl: GET returns the Epic's control session (404 before
// it exists), POST opens it.
func (m *Module) handleFactoryControl(w http.ResponseWriter, r *http.Request, epicID string) {
	switch r.Method {
	case http.MethodGet:
		store, err := m.factoryControlStore()
		if err != nil {
			WriteError(w, err)
			return
		}
		control, found, err := store.GetFactoryControlSession(r.Context(), epicID)
		if err != nil {
			WriteError(w, err)
			return
		}
		if !found {
			http.Error(w, "no control session", http.StatusNotFound)
			return
		}
		httpx.WriteJSON(w, control)
	case http.MethodPost:
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			control, _, err := m.EnsureControlSession(r.Context(), epicID)
			if err != nil {
				WriteError(w, err)
				return
			}
			httpx.WriteJSON(w, control)
		})(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleFactoryControlAction serves a proposal's card: GET reads it, POST
// confirm runs it, POST dismiss drops it. Only a human click reaches here.
func (m *Module) handleFactoryControlAction(w http.ResponseWriter, r *http.Request, epicID, actionID, verb string) {
	store, err := m.factoryControlStore()
	if err != nil {
		WriteError(w, err)
		return
	}
	if verb == "" && r.Method == http.MethodGet {
		action, found, err := store.GetFactoryControlAction(r.Context(), epicID, actionID)
		if err != nil {
			WriteError(w, err)
			return
		}
		if !found {
			http.Error(w, "control action not found", http.StatusNotFound)
			return
		}
		httpx.WriteJSON(w, action)
		return
	}
	if r.Method != http.MethodPost || (verb != "confirm" && verb != "dismiss") {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			AcknowledgeLocalExecution bool `json:"acknowledgeLocalExecution"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); (err != nil && err != io.EOF) || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid confirmation", http.StatusBadRequest)
			return
		}
		action, err := m.DecideControlAction(r.Context(), epicID, actionID, verb, request.AcknowledgeLocalExecution)
		if err != nil {
			WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, action)
	})(w, r)
}

// DecideControlAction confirms or dismisses a pending proposal. Only a
// human click reaches it; confirming runs the action with the user's
// authority and reports the outcome to the control session.
func (m *Module) DecideControlAction(ctx context.Context, epicID, actionID, verb string, acknowledged ...bool) (state.FactoryControlAction, error) {
	store, err := m.factoryControlStore()
	if err != nil {
		return state.FactoryControlAction{}, err
	}
	factoryControlDecisionMu.Lock()
	defer factoryControlDecisionMu.Unlock()
	action, found, err := store.GetFactoryControlAction(ctx, epicID, actionID)
	if err != nil {
		return action, err
	}
	if !found {
		return action, fmt.Errorf("%w: control action not found", factory.ErrInvalidRequest)
	}
	next := map[string]string{"confirm": "confirmed", "dismiss": "dismissed"}[verb]
	won, err := store.TransitionFactoryControlAction(ctx, epicID, actionID, "pending", next, "")
	if err != nil {
		return action, err
	}
	if !won {
		return action, fmt.Errorf("%w: this proposal was already %s", factory.ErrInvalidRequest, action.Status)
	}
	action.Status = next
	// The claim is durable: run and finish under contexts the client cannot
	// cancel, and record the outcome under its own deadline, so a confirmed
	// proposal reaches a terminal state even when running it times out.
	detached := context.WithoutCancel(ctx)
	outcome := "The user dismissed: " + action.Summary
	if verb == "confirm" {
		runCtx, cancelRun := context.WithTimeout(detached, ControlRunTimeout)
		result, runErr := m.runFactoryControlAction(runCtx, action, acknowledged...)
		cancelRun()
		action.Status, action.Result = "done", result
		if runErr != nil {
			action.Status, action.Result = "failed", runErr.Error()
		}
		finishCtx, cancelFinish := context.WithTimeout(detached, 10*time.Second)
		if runErr == nil {
			if err := store.DismissFactoryControlAlternatives(finishCtx, epicID, actionID); err != nil {
				logrus.WithError(err).WithField("epic", epicID).Warn("factory: stale control proposals could not be retired")
			}
		}
		_, err := store.TransitionFactoryControlAction(finishCtx, epicID, actionID, "confirmed", action.Status, action.Result)
		cancelFinish()
		if err != nil {
			return action, err
		}
		outcome = fmt.Sprintf("The user confirmed: %s\nOutcome: %s %s", action.Summary, action.Status, action.Result)
		if reconciler, ok := m.svc.(interface{ ReconcileAttention(context.Context) }); ok {
			reconciler.ReconcileAttention(detached)
		}
	}
	// Tell the control session, if there is one, so it can carry on.
	postCtx, cancelPost := context.WithTimeout(detached, 2*time.Minute)
	defer cancelPost()
	if control, found, _ := store.GetFactoryControlSession(postCtx, epicID); found {
		// Deliver the final outcome without creating another control session.
		epic, err := m.svc.GetWorkEpic(postCtx, epicID)
		if err == nil && epic.Status == "closed" {
			outcome += "\nThe Epic is closed. Report the final state and unfinished work. Do not propose more work or recovery."
			err = m.deps.Enqueue(postCtx, control.Platform, false, platforms.SendMessageRequest{SessionID: control.SessionID, Message: outcome, Model: epic.Models.Plan})
		} else if err == nil {
			_, err = m.PostControl(postCtx, epicID, outcome+"\nRe-read the Epic and issues before recommending another action. Older notifications and proposals may have been superseded.")
		}
		if err != nil {
			logrus.WithError(err).WithField("epic", epicID).Warn("factory: control outcome not delivered")
		}
	}
	return action, nil
}

// runFactoryControlAction performs a confirmed proposal with the user's
// authority, so it bypasses the agent-facing refusals.
func (m *Module) runFactoryControlAction(ctx context.Context, action state.FactoryControlAction, acknowledged ...bool) (string, error) {
	var args map[string]string
	if err := json.Unmarshal(action.Args, &args); err != nil {
		return "", err
	}
	epicID := action.EpicID
	epic, err := m.svc.GetWorkEpic(ctx, epicID)
	if err != nil {
		return "", err
	}
	if epic.Status == "closed" {
		return "", fmt.Errorf("%w: the Epic is closed", factory.ErrInvalidRequest)
	}
	switch action.Action {
	case "claim_plan":
		_, err := m.svc.ClaimPlan(ctx, epicID, args["issue_id"])
		return "", err
	case "materialize":
		_, err := m.svc.Materialize(ctx, epicID, args["issue_id"])
		return "", err
	case "reopen":
		reopener, ok := m.svc.(interface {
			ReopenIssue(context.Context, string, string) error
		})
		if !ok {
			return "", factory.ErrFactoryUnavailable
		}
		return "", reopener.ReopenIssue(ctx, epicID, args["issue_id"])
	case "mutate_graph":
		mutation, err := decodeFactoryControlMutation(args["mutation_json"])
		if err != nil {
			return "", err
		}
		mutation.EpicID, mutation.Actor = epicID, "user"
		return "", m.svc.MutateGraph(ctx, mutation)
	case "resolve_recovery":
		issues, err := m.svc.ListIssues(ctx, epicID)
		if err != nil {
			return "", err
		}
		for _, issue := range issues {
			if issue.ID == args["recovery_gate_id"] || (issue.Recovery != nil && issue.Recovery.IssueID == args["recovery_gate_id"]) {
				_, err := m.svc.ResolveRecoveryGate(ctx, args["recovery_gate_id"], args["decision"], args["response"])
				return "", err
			}
		}
		return "", fmt.Errorf("%w: recovery gate %s is not part of Epic %s", factory.ErrInvalidRequest, args["recovery_gate_id"], epicID)
	case "deliver_as_is":
		deliverer, ok := m.svc.(interface {
			DeliverAsIs(context.Context, string) ([]string, error)
		})
		if !ok {
			return "", factory.ErrFactoryUnavailable
		}
		leftOut, err := deliverer.DeliverAsIs(ctx, epicID)
		if len(leftOut) == 0 {
			return "", err
		}
		return "left out " + strings.Join(leftOut, ", "), err
	case "pause", "resume":
		pauser, ok := m.svc.(interface {
			SetEpicPaused(context.Context, string, bool) error
		})
		if !ok {
			return "", factory.ErrFactoryUnavailable
		}
		return "", pauser.SetEpicPaused(ctx, epicID, action.Action == "pause")
	case "stop":
		stopper, ok := m.svc.(interface {
			StopEpic(context.Context, string) error
		})
		if !ok {
			return "", factory.ErrFactoryUnavailable
		}
		return "Epic stopped without delivery; unfinished work cancelled, files and history preserved.", stopper.StopEpic(ctx, epicID)
	case "decide_plan", "resolve_authority", "resolve_project":
		return "", m.runFactoryControlDecision(ctx, epicID, action.Action, args, acknowledged...)
	}
	return "", fmt.Errorf("%w: unsupported factory control action", factory.ErrInvalidRequest)
}
