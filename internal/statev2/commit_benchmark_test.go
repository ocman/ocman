package statev2

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// BenchmarkPartialCommit appends one message to a retained session, as a
// journaled history read does after each source event. Messages are 20 s
// apart, so an hour bucket holds about 180 of them.
func BenchmarkPartialCommit(b *testing.B) {
	message := func(i int) Message {
		at := int64(i) * 20_000
		return Message{ID: fmt.Sprintf("m%06d", i), Position: int64(i), Role: "assistant", CreatedAt: at, CompletedAt: at + 5000, ConversationVisible: true,
			Extensions: json.RawMessage(fmt.Sprintf(`{"role":"assistant","finish":"stop","modelID":"model","providerID":"provider","time":{"created":%d,"completed":%d},"tokens":{"input":12,"output":345,"cache":{"read":164532,"write":210}},"system":[%q]}`,
				at, at+5000, strings.Repeat("s", 300)))}
	}
	parts := func(i int) []Part {
		id := fmt.Sprintf("m%06d", i)
		return []Part{
			{ID: id + "-text", MessageID: id, Kind: "text", Text: strings.Repeat("t", 400), TextPresent: true},
			{ID: id + "-tool", MessageID: id, Position: 1, Kind: "tool",
				Extensions: json.RawMessage(fmt.Sprintf(`{"tool":"bash","state":{"status":"completed","output":%q}}`, strings.Repeat("o", 2000)))},
		}
	}
	for _, retained := range []int{200, 1000, 4000} {
		b.Run(fmt.Sprint(retained), func(b *testing.B) {
			d, err := Open(b.TempDir() + "/conversation.db")
			if err != nil {
				b.Fatal(err)
			}
			defer d.Close()
			if err := d.RegisterInstance(b.Context(), Instance{ID: "first", OwnerID: "owner", Platform: "claude", SourceKey: "fixture"}); err != nil {
				b.Fatal(err)
			}
			s := Session{Key: Key{InstanceID: "first", SessionID: "history"}, Title: "history", Directory: "/repo", Extensions: json.RawMessage(`{}`)}
			base := Batch{Session: s, Complete: true}
			for i := range retained {
				base.Messages = append(base.Messages, message(i))
				base.Parts = append(base.Parts, parts(i)...)
			}
			result, err := d.Commit(b.Context(), base)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := range b.N {
				next := retained + i
				batch := Batch{Session: s, ExpectedRevision: result.Revision, Messages: []Message{message(next)}, Parts: parts(next)}
				if result, err = d.Commit(b.Context(), batch); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
