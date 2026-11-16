package sessionsvc

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

func TestExactModelNeverFallsBack(t *testing.T) {
	for _, model := range []string{"anthropic/opus", "opencode:anthropic/opus", "claudecode:anthropic/opus", ""} {
		t.Run(model, func(t *testing.T) {
			svc, p := newProjectModelService(map[string][]string{"/repo": {"openai/sol"}})
			svc.RecordCooldown(t.Context(), "anthropic", time.Minute)
			err := svc.SendMessage(t.Context(), "opencode", platforms.SendMessageRequest{
				SessionID: "s", Message: "investigate", Model: model, ExactModel: true,
			})
			if model == "" || model == "claudecode:anthropic/opus" {
				if err == nil || len(p.sent) != 0 {
					t.Fatalf("invalid exact selection sent: %v, %+v", err, p.sent)
				}
				return
			}
			if err != nil || len(p.sent) != 1 || p.sent[0].Model != "anthropic/opus" || !p.sent[0].ExactModel {
				t.Fatalf("exact selection replaced: %v, %+v", err, p.sent)
			}
		})
	}
}
