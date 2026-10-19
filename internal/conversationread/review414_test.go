package conversationread

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestResumptionOutranksFreshnessFailure(t *testing.T) {
	for _, stage := range []string{"stale wait", "new busy head", "second wait"} {
		t.Run(stage, func(t *testing.T) {
			s, p, _, _ := readFixture(t)
			if stage == "stale wait" {
				p.head.Version = "v2"
			} else {
				p.onHead = func(n int, h *platforms.ConversationHead) {
					if n == 2 {
						h.Version = "v2"
						if stage == "new busy head" {
							row := *h.Session
							row.Status = db.StatusBusy
							h.Session = &row
						}
					}
				}
			}
			waits := 0
			s.Await = func(context.Context, string, string, string) bool {
				waits++
				p.live = &platforms.LiveState{Status: "busy"}
				return false
			}
			_, handled, err := s.ReadConversation(t.Context(), p, "s", 30, 0)
			if handled || err != nil || (stage == "new busy head" && waits != 0) {
				t.Fatalf("live resumption blocked by history: handled=%v err=%v waits=%d", handled, err, waits)
			}
		})
	}
}
