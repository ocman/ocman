package owned

import (
	"bytes"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestBoundSessionUsesIDMappingAfterProjectChangesOnEveryPlatform(t *testing.T) {
	for _, platform := range []platforms.ID{"opencode", "claudecode"} {
		t.Run(string(platform), func(t *testing.T) {
			original, native, catalogue := fixture(t)
			native.id = platform
			a, err := New(t.Context(), native, catalogue, original.history, "owner")
			if err != nil {
				t.Fatal(err)
			}
			publish(t, a, native.rows...)
			bound, err := a.canonical(t.Context(), "native")
			if err != nil {
				t.Fatal(err)
			}
			native.rows[0].Directory = "/different-project/renamed-checkout"
			publish(t, a, native.rows...)
			if err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: bound.ID, Message: "next"}); err != nil {
				t.Fatal(err)
			}
			if len(native.sent) != 1 || native.sent[0].SessionID != "native" {
				t.Fatalf("send lost native ID binding: %+v", native.sent)
			}
			native.events = "data: {\"type\":\"message.part.updated\",\"properties\":{\"part\":{\"sessionID\":\"native\",\"id\":\"part\"}}}\n\n"
			var out bytes.Buffer
			if err := a.ProxyEvents(t.Context(), bound.ID, &out, func() {}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), bound.ID) || strings.Contains(out.String(), `"sessionID":"native"`) {
				t.Fatalf("stream lost canonical ID binding: %s", out.String())
			}
			updated, err := a.canonical(t.Context(), "native")
			if err != nil || updated.ID != bound.ID || updated.Directory != native.rows[0].Directory {
				t.Fatalf("project change replaced session identity: %+v %v", updated, err)
			}
		})
	}
}
