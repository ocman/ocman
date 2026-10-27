package conversationread

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestStoredContextUsesRequestUsageInsteadOfTurnTotals(t *testing.T) {
	for _, tt := range []struct {
		name    string
		context string
		output  int64
		want    int64
	}{
		{"explicit request context", `,"contextTokens":12000`, 500, 12000},
		{"empty context", `,"contextTokens":0`, 500, 0},
		{"context without output", `,"contextTokens":12000`, 0, 12000},
		{"legacy token fallback", "", 500, 110520},
		{"null context fallback", `,"contextTokens":null`, 500, 110520},
		{"negative context fallback", `,"contextTokens":-1`, 500, 110520},
		{"unfinished legacy reply", "", 0, 21},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, batch := readFixture(t)
			batch.ExpectedRevision = 1
			batch.Messages = append(batch.Messages, statev2.Message{ID: "latest", Role: "assistant", Position: 3,
				CreatedAt: 7, ConversationVisible: true, Extensions: json.RawMessage(fmt.Sprintf(
					`{"role":"assistant","tokens":{"input":100000,"output":%d,"reasoning":20,"cache":{"read":10000,"write":0}}%s,"finish":"stop","time":{"created":7,"completed":8}}`, tt.output, tt.context))})
			if _, err := store.Commit(t.Context(), batch); err != nil {
				t.Fatal(err)
			}
			for _, offset := range []int{0, 1} {
				page, err := store.ReadConversation(t.Context(), batch.Session.Key, 1, offset)
				if err != nil {
					t.Fatal(err)
				}
				detail, err := Convert(page, &platforms.ConversationHead{}, "codex")
				if err != nil {
					t.Fatal(err)
				}
				if detail.ContextTokenCount != tt.want {
					t.Errorf("offset %d: context = %d, want %d", offset, detail.ContextTokenCount, tt.want)
				}
				if detail.Session.TotalInputTokens != 100010 || detail.Session.TotalOutputTokens != tt.output+5 {
					t.Errorf("context override changed accounting totals: %+v", detail.Session)
				}
			}
		})
	}
}
