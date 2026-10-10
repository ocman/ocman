package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/sirupsen/logrus"
)

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
func (s *Server) handleFactoryControl(w http.ResponseWriter, r *http.Request, epicID string) {
	switch r.Method {
	case http.MethodGet:
		store, err := s.factoryControlStore()
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		control, found, err := store.GetFactoryControlSession(r.Context(), epicID)
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		if !found {
			http.Error(w, "no control session", http.StatusNotFound)
			return
		}
		writeJSON(w, control)
	case http.MethodPost:
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			control, _, err := s.ensureFactoryControlSession(r.Context(), epicID)
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			writeJSON(w, control)
		})(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleFactoryControlAction serves a proposal's card: GET reads it, POST
// confirm runs it, POST dismiss drops it. Only a human click reaches here.
func (s *Server) handleFactoryControlAction(w http.ResponseWriter, r *http.Request, epicID, actionID, verb string) {
	store, err := s.factoryControlStore()
	if err != nil {
		writeFactoryError(w, err)
		return
	}
	if verb == "" && r.Method == http.MethodGet {
		action, found, err := store.GetFactoryControlAction(r.Context(), epicID, actionID)
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		if !found {
			http.Error(w, "control action not found", http.StatusNotFound)
			return
		}
		writeJSON(w, action)
		return
	}
	if r.Method != http.MethodPost || (verb != "confirm" && verb != "dismiss") {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
		action, err := s.decideFactoryControlAction(r.Context(), store, epicID, actionID, verb)
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		writeJSON(w, action)
	})(w, r)
}

func (s *Server) decideFactoryControlAction(ctx context.Context, store factoryControlStore, epicID, actionID, verb string) (state.FactoryControlAction, error) {
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
	outcome := "The user dismissed: " + action.Summary
	if verb == "confirm" {
		result, runErr := s.runFactoryControlAction(ctx, action)
		action.Status, action.Result = "done", result
		if runErr != nil {
			action.Status, action.Result = "failed", runErr.Error()
		}
		if _, err := store.TransitionFactoryControlAction(ctx, epicID, actionID, "confirmed", action.Status, action.Result); err != nil {
			return action, err
		}
		outcome = fmt.Sprintf("The user confirmed: %s\nOutcome: %s %s", action.Summary, action.Status, action.Result)
	}
	// Tell the control session, if there is one, so it can carry on.
	if _, found, _ := store.GetFactoryControlSession(ctx, epicID); found {
		if _, err := s.postFactoryControl(ctx, epicID, outcome); err != nil {
			logrus.WithError(err).WithField("epic", epicID).Warn("factory: control outcome not delivered")
		}
	}
	return action, nil
}

// runFactoryControlAction performs a confirmed proposal with the user's
// authority, so it bypasses the agent-facing refusals.
func (s *Server) runFactoryControlAction(ctx context.Context, action state.FactoryControlAction) (string, error) {
	var args map[string]string
	if err := json.Unmarshal(action.Args, &args); err != nil {
		return "", err
	}
	epicID := action.EpicID
	switch action.Action {
	case "reopen":
		reopener, ok := s.factory.(interface {
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
		return "", s.factory.MutateGraph(ctx, mutation)
	case "resolve_recovery":
		issues, err := s.factory.ListIssues(ctx, epicID)
		if err != nil {
			return "", err
		}
		for _, issue := range issues {
			if issue.ID == args["recovery_gate_id"] || (issue.Recovery != nil && issue.Recovery.IssueID == args["recovery_gate_id"]) {
				_, err := s.factory.ResolveRecoveryGate(ctx, args["recovery_gate_id"], args["decision"], args["response"])
				return "", err
			}
		}
		return "", fmt.Errorf("%w: recovery gate %s is not part of Epic %s", factory.ErrInvalidRequest, args["recovery_gate_id"], epicID)
	case "deliver_as_is":
		deliverer, ok := s.factory.(interface {
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
		pauser, ok := s.factory.(interface {
			SetEpicPaused(context.Context, string, bool) error
		})
		if !ok {
			return "", factory.ErrFactoryUnavailable
		}
		return "", pauser.SetEpicPaused(ctx, epicID, action.Action == "pause")
	}
	return "", fmt.Errorf("%w: unsupported factory control action", factory.ErrInvalidRequest)
}
