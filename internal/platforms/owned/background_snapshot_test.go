package owned

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/convevents/v1map"
)

func TestBackgroundSnapshotReachesCanonicalConversation(t *testing.T) {
	for _, framing := range []string{"ordinary", "named", "global"} {
		t.Run(framing, func(t *testing.T) {
			a, p, _ := fixture(t)
			row, err := a.canonical(t.Context(), "native")
			if err != nil {
				t.Fatal(err)
			}
			payload := `{"backgroundTasks":{"native":1},"backgroundTaskDetails":{"native":[{"id":"shell","description":"pnpm exec vite preview","sessionID":"opaque"}]}}`
			frame := `{"type":"server.connected","properties":` + payload + `}`
			prefix := ""
			switch framing {
			case "global":
				frame = `{"directory":"/repo","payload":` + frame + `}`
			case "named":
				prefix, frame = "event: server.connected\n", payload
			}
			p.events = prefix + "data: " + frame + "\n\n"
			var out bytes.Buffer
			if err := a.ProxyEvents(t.Context(), row.ID, &out, nil); err != nil {
				t.Fatal(err)
			}
			mapped := v1map.New(v1map.Options{SessionID: row.ID}).Map(v1map.ParseFrame(bytes.TrimSpace(out.Bytes())))
			if len(mapped.Events) != 1 {
				t.Fatalf("background snapshot disappeared: %s", out.String())
			}
			bg, ok := mapped.Events[0].(convevents.BackgroundChanged)
			if !ok || bg.Count != 1 || len(bg.Tasks) != 1 || bg.Tasks[0].ID != "shell" || bg.Tasks[0].Description != "pnpm exec vite preview" {
				t.Fatalf("background process missing from canonical conversation: %+v, frame: %s", bg, out.String())
			}
			if !strings.Contains(out.String(), fmt.Sprintf(`"%s":1`, row.ID)) || !strings.Contains(out.String(), `"sessionID":"opaque"`) {
				t.Fatalf("snapshot keys or opaque task data changed incorrectly: %s", out.String())
			}
		})
	}
}
