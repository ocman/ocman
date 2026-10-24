package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

type permissionNotificationPlatform struct {
	fakePlatform
	prompts []platforms.LivePrompt
	err     error
}

func (p *permissionNotificationPlatform) ListPermissions(context.Context, string) ([]platforms.LivePrompt, error) {
	return p.prompts, p.err
}

func TestPermissionNotificationDecisionCrossesOwnerRPC(t *testing.T) {
	p := &permissionNotificationPlatform{prompts: []platforms.LivePrompt{{"id": "judge", "sessionID": "child"}, {"id": "manual", "sessionID": "other-child"}}}
	reg := platforms.NewRegistry()
	reg.Register(p)
	srv := NewServer(reg, nil, "owner", "test").UsePermissionNotificationDeferrer(func(session, request string) bool { return session == "child" && request == "judge" })
	client := pb.NewOcmanClient(startTestServer(t, "token", srv))
	resp, err := client.Call(t.Context(), &pb.CallReq{Method: "ListPermissions", Platform: string(p.ID()), SessionId: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	var prompts []platforms.LivePrompt
	if err := unmarshalJSON(resp.Payload, &prompts); err != nil {
		t.Fatal(err)
	}
	if prompts[0][platforms.NotificationDeferredField] != true || prompts[1][platforms.NotificationDeferredField] != false {
		t.Fatalf("owner decisions lost: %+v", prompts)
	}
	if _, exists := p.prompts[0][platforms.NotificationDeferredField]; exists {
		t.Fatal("mutated source prompt cache")
	}
	p.err = errors.New("owner read failed")
	if _, err := srv.ListPermissions(t.Context(), &pb.CallReq{Platform: string(p.ID()), SessionId: "parent"}); err == nil {
		t.Fatal("owner read failure hidden")
	}
}
