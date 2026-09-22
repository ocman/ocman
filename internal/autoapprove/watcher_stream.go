package autoapprove

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	log "github.com/sirupsen/logrus"
)

// streamOnce consumes one upstream connection until EOF or cancellation.
func (w *autoApproveWatcher) streamOnce(ctx context.Context, port string) error {
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/global/event", port)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("build /global/event request: %w", err)
	}
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect /global/event: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("/global/event upstream HTTP %d", resp.StatusCode)
	}

	adapter := w.svc.OpencodeAdapter()
	ocAdapter, _ := adapter.(*opencode.Adapter)
	portGeneration := uint64(0)
	statusGeneration := uint64(0)
	firstReconciliationFinished := make(chan struct{})
	if ocAdapter != nil {
		portGeneration = ocAdapter.PromptPortGeneration(port)
		// Seed upstream turn state before processing live events.
		statusGeneration = ocAdapter.StatusPortGeneration(port)
		if !ocAdapter.SeedSessionStatusFromInstance(streamCtx, port, statusGeneration) {
			log.WithField("port", port).Debug("autoapprove-watcher: /session/status snapshot unavailable, turn state stays unobserved")
		}
		directories := w.directoriesForPort(port)
		onPermission := func(prompt platforms.LivePrompt) {
			w.svc.ObservePermissionPrompt(opencode.PlatformID, "", prompt)
			if w.onPermission == nil {
				return
			}
			sessionID, _ := prompt["sessionID"].(string)
			permissionID, _ := prompt["id"].(string)
			permission, _ := prompt["permission"].(string)
			metadata, _ := prompt["metadata"].(map[string]any)
			patterns := promptStrings(prompt["patterns"])
			if sessionID != "" && permissionID != "" {
				w.onPermission(opencode.PlatformID, adapter, sessionID, permissionID, permission, patterns, metadata)
				if w.svc != nil && w.svc.deps.BroadcastSessionChanged != nil {
					w.svc.deps.BroadcastSessionChanged(sessionID)
				}
			}
		}
		first := ocAdapter.StartPromptReconciliation(ctx, port, directories, onPermission)
		go func() {
			ok := <-first
			close(firstReconciliationFinished)
			for !ok {
				select {
				case <-streamCtx.Done():
					return
				case <-time.After(w.reconnectDelay):
				}
				ok = <-ocAdapter.StartPromptReconciliation(streamCtx, port, directories, onPermission)
			}
		}()
	} else {
		close(firstReconciliationFinished)
	}

	commitParts := make(chan terminalPart, 32)
	commitDone := make(chan struct{})
	go func() {
		defer close(commitDone)
		for part := range commitParts {
			w.recordTerminalPart(streamCtx, part)
		}
	}()

	tee := &Tee{
		W: io.Discard,
		OnPermission: func(sessionID, permissionID, permission string, patterns []string, metadata map[string]any) {
			if w.onPermission != nil {
				w.onPermission(opencode.PlatformID, adapter, sessionID, permissionID, permission, patterns, metadata)
			}
		},
		OnPermissionReplied: func(sessionID, permissionID, reply string) {
			if w.onPermissionReplied != nil {
				w.onPermissionReplied(sessionID, permissionID, reply)
			}
		},
		OnPromptAsked: func(directory, kind string, prompt platforms.LivePrompt) {
			if kind == "permission" {
				w.svc.ObservePermissionPrompt(opencode.PlatformID, "", prompt)
			}
			if kind == "question" {
				// A question is never auto-answered, so asking it already
				// settles that the user has to.
				w.svc.ObserveQuestionPrompt(opencode.PlatformID, prompt)
			}
			if ocAdapter != nil {
				ocAdapter.ObservePromptAskedFromPort(port, portGeneration, directory, kind, prompt)
			}
			sessionID, _ := prompt["sessionID"].(string)
			if sessionID != "" && w.svc != nil && w.svc.deps.BroadcastSessionChanged != nil {
				w.svc.deps.BroadcastSessionChanged(sessionID)
			}
			if requestID, _ := prompt["id"].(string); kind == "permission" && w.svc != nil {
				w.svc.SurfacePermissionNotification(sessionID, requestID)
			}
		},
		OnPromptResolved: func(directory, kind, sessionID, requestID string) {
			if ocAdapter != nil {
				ocAdapter.ObservePromptResolved(directory, kind, sessionID, requestID)
			}
			if sessionID != "" && w.svc != nil && w.svc.deps.BroadcastSessionChanged != nil {
				w.svc.deps.BroadcastSessionChanged(sessionID)
			}
		},
		OnQuestionResolved: func(sessionID, requestID, reason string) {
			if w.svc != nil && w.svc.deps.BroadcastQuestionResolved != nil {
				w.svc.deps.BroadcastQuestionResolved(sessionID, requestID, reason)
			}
		},
		OnSessionStatus: func(sessionID, statusType string) {
			w.markSessionDirtyIfKnown(sessionID)
			if ocAdapter == nil {
				return
			}
			ocAdapter.ObserveSessionStatus(port, statusGeneration, sessionID, statusType)
			w.broadcastSessionStatus(ocAdapter, port, sessionID, statusType)
		},
		OnSessionIdle: func(sessionID string) {
			w.markSessionDirtyIfKnown(sessionID)
			if ocAdapter != nil {
				ocAdapter.ObserveSessionStatus(port, statusGeneration, sessionID, "idle")
				w.broadcastSessionStatus(ocAdapter, port, sessionID, "idle")
			}
			if w.svc != nil && w.svc.deps.BroadcastSessionIdle != nil {
				w.svc.deps.BroadcastSessionIdle(string(opencode.PlatformID), sessionID)
			}
		},
		OnSessionChanged: func(sessionID string) {
			w.handleSessionChanged(streamCtx, sessionID)
		},
		OnSessionDataChanged: w.handleSessionDataChanged,
		OnTerminalPart: func(part terminalPart) {
			select {
			case commitParts <- part:
			case <-streamCtx.Done():
			default:
				log.WithField("session_id", part.SessionID).Warn("session commit capture queue full; dropping live observation")
			}
		},
	}
	_, err = io.Copy(tee, resp.Body)
	close(commitParts)
	<-commitDone
	<-firstReconciliationFinished
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("read /global/event: %w", err)
	}
	return nil
}
