package pluginsmod

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func (m *Module) sendSession(ctx context.Context, pluginID string, params plugins.SessionSendParams, operationID string) (id string, sendErr error) {
	if m.plugins == nil || m.deps.Adapter == nil || m.deps.Enqueue == nil {
		return "", plugins.ErrUnavailable
	}
	row, err := strconv.ParseInt(strings.TrimPrefix(params.After, sessionOutboxPrefix), 10, 64)
	if err != nil || row <= 0 || params.After != sessionOutboxOperation(row) {
		return "", &plugins.WireError{Category: plugins.ErrorInvalidArgument}
	}
	var platformID, sessionID, completedOperation string
	err = m.plugins.db.QueryRowContext(ctx, `SELECT platform_id,session_id,operation_id FROM plugin_session_outbox WHERE id=? AND plugin_id=?`, row, pluginID).Scan(&platformID, &sessionID, &completedOperation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &plugins.WireError{Category: plugins.ErrorPermissionDenied}
	}
	if err != nil {
		return "", plugins.ErrUnavailable
	}
	owner, linked, err := m.plugins.GetPluginSessionPlugin(ctx, platformID, sessionID)
	if err != nil || !linked || owner != pluginID {
		return "", &plugins.WireError{Category: plugins.ErrorPermissionDenied}
	}
	// One durable claim per completed turn also fences concurrent callers using
	// different operation IDs. The fingerprint rejects changed IDs or prompts.
	receipt := "followup:" + params.After
	stored, claimed, err := m.plugins.reserveSessionRun(ctx, pluginID, receipt, "send:"+operationID, plugins.SessionRunParams{Prompt: params.Prompt})
	if err != nil {
		return "", err
	}
	if !claimed {
		return m.pluginWireSession(ctx, platformID, stored)
	}
	defer func() {
		if err := m.plugins.finishSessionRun(ctx, pluginID, receipt, sessionID, sendErr); err != nil {
			id, sendErr = "", err
		}
	}()
	var newer bool
	if err := m.plugins.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM plugin_session_outbox WHERE plugin_id=? AND platform_id=? AND session_id=? AND id>?)`, pluginID, platformID, sessionID, row).Scan(&newer); err != nil {
		return "", plugins.ErrUnavailable
	}
	if newer {
		return "", &plugins.WireError{Category: plugins.ErrorConflict}
	}
	adapter, ok := m.deps.Adapter(ctx, platformID, sessionID)
	if !ok {
		return "", plugins.ErrUnavailable
	}
	detail, err := adapter.Session(ctx, sessionID, conversationFetchLimit, 0)
	if err != nil || detail == nil || detail.Session == nil {
		return "", plugins.ErrUnavailable
	}
	latest, text := db.LatestAssistantTextVerbatim(detail.Messages, detail.Parts)
	if !conversationReplyReady(detail.Session.Status) || text == "" || latest == "" || !strings.HasSuffix(completedOperation, ":"+latest) {
		return "", &plugins.WireError{Category: plugins.ErrorConflict}
	}
	if lifecycle, ok := adapter.(platforms.LifecycleReader); ok {
		state, err := lifecycle.SessionLifecycle(ctx, sessionID)
		if err != nil || state == nil {
			return "", plugins.ErrUnavailable
		}
		if !conversationReplyReady(state.Status) || state.LatestMessageID != latest || state.LatestMessageRole != "assistant" {
			return "", &plugins.WireError{Category: plugins.ErrorConflict}
		}
	}
	if err := m.plugins.PluginSessionBacklogFull(ctx, pluginID); err != nil {
		return "", plugins.ErrUnavailable
	}
	// Retain the session's existing creation identity and all pinned selections.
	// Never create a session, reset permissions or send through another owner.
	if err := m.deps.Enqueue(ctx, platformID, false, platforms.SendMessageRequest{SessionID: sessionID, Message: params.Prompt, PreserveSelections: true}); err != nil {
		return "", err
	}
	return m.pluginWireSession(ctx, platformID, sessionID)
}
