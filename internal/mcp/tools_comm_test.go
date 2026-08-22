package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type followupStore struct {
	child         state.ChildSession
	restoreHit    bool
	restoreErr    error
	restoreCtxErr error
	completeHit   bool
	completeErr   error
	restoreCalls  int
}

func (s *followupStore) GetChildSession(context.Context, string) (*state.ChildSession, error) {
	child := s.child
	return &child, nil
}

func (*followupStore) ClaimChildFollowup(context.Context, string, string, string) (bool, error) {
	return true, nil
}

func (s *followupStore) CompleteChildFollowup(context.Context, string, string, string) (bool, error) {
	return s.completeHit, s.completeErr
}

func (s *followupStore) RestoreChildFollowup(ctx context.Context, _ string, _ state.ChildSession, _ string) (bool, error) {
	s.restoreCalls++
	s.restoreCtxErr = ctx.Err()
	return s.restoreHit, s.restoreErr
}

func (*followupStore) CompareAndSetChildResultDelivery(context.Context, string, string, string) (bool, error) {
	return true, nil
}

type followupClient struct{ sendErr error }

func (*followupClient) CreateSession(context.Context, platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	return nil, nil
}

func (c *followupClient) SendMessage(context.Context, platforms.SendMessageRequest) error {
	return c.sendErr
}

func (*followupClient) SetPermissionRules(context.Context, platforms.SetPermissionRulesRequest) error {
	return nil
}

func (*followupClient) PermissionRules(context.Context, string) ([]platforms.PermissionRule, error) {
	return nil, nil
}

func followupRequest() mcplib.CallToolRequest {
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{
		"session_id":       "parent-1",
		"child_session_id": "child-1",
		"message":          "continue",
	}
	return req
}

func followupChild() state.ChildSession {
	return state.ChildSession{ID: "child-1", ParentSessionID: "parent-1", Status: "completed", ResultDelivery: "delivered"}
}

func TestSendMessageToChildSurfacesRestoreFailure(t *testing.T) {
	for _, tt := range []struct {
		name       string
		restoreHit bool
		restoreErr error
		want       string
	}{
		{name: "error", restoreErr: errors.New("database unavailable"), want: "restore failed: database unavailable"},
		{name: "miss", want: "restoring child follow-up: delivery ownership was lost"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &followupStore{child: followupChild(), restoreHit: tt.restoreHit, restoreErr: tt.restoreErr}
			tools := &commTools{stateDB: store, platform: &followupClient{sendErr: errors.New("offline")}}

			result, err := tools.handleSendMessageToChild(t.Context(), followupRequest())
			if err != nil || !result.IsError || !strings.Contains(result.Content[0].(mcplib.TextContent).Text, tt.want) {
				t.Fatalf("result = %+v, %v; want %q", result, err, tt.want)
			}
		})
	}
}

func TestSendMessageToChildRestoresWithCancelledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	store := &followupStore{child: followupChild(), restoreHit: true}
	client := &followupClient{sendErr: context.Canceled}
	tools := &commTools{stateDB: store, platform: client}
	cancel()

	result, err := tools.handleSendMessageToChild(ctx, followupRequest())
	if err != nil || !result.IsError || store.restoreCtxErr != nil {
		t.Fatalf("result = %+v, error = %v, restore context error = %v", result, err, store.restoreCtxErr)
	}
}

func TestSendMessageToChildDoesNotRestoreAfterSuccessfulSend(t *testing.T) {
	store := &followupStore{child: followupChild(), completeErr: errors.New("database unavailable")}
	tools := &commTools{stateDB: store, platform: &followupClient{}}

	result, err := tools.handleSendMessageToChild(t.Context(), followupRequest())
	if err != nil || !result.IsError || !strings.Contains(result.Content[0].(mcplib.TextContent).Text, "database unavailable") {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if store.restoreCalls != 0 {
		t.Fatalf("RestoreChildFollowup called %d times after successful send", store.restoreCalls)
	}
}

func TestSendMessageToChildRestartsCompletionTracking(t *testing.T) {
	store := &followupStore{child: followupChild(), completeHit: true}
	started := ""
	tools := &commTools{
		stateDB:  store,
		platform: &followupClient{},
		started:  func(id string) { started = id },
	}

	result, err := tools.handleSendMessageToChild(t.Context(), followupRequest())
	if err != nil || result.IsError {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if started != "child-1" {
		t.Fatalf("started callback = %q, want child-1", started)
	}
}
