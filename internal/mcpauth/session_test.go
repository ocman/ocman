package mcpauth

import "testing"

func TestSessionHeaders(t *testing.T) {
	for _, id := range []string{"", "caller-one", "caller-two"} {
		h := SessionHeaders("token", "codex", id)
		if h["Authorization"] != Header("token") || h[CallerSessionHeader] != id {
			t.Fatalf("headers = %+v", h)
		}
		if id == "" && len(h) != 1 || id != "" && (len(h) != 3 || h[CallerPlatformHeader] != "codex") {
			t.Fatalf("unexpected caller scope: %+v", h)
		}
	}
}
