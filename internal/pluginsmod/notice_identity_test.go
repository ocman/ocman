package pluginsmod

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestConversationNoticeUsesOwnedSessionLinkAndNeverNativeFallback(t *testing.T) {
	fail := false
	m := NewModule(Deps{PublicURL: func(path string) string { return "https://ocman.example" + path }, SessionURL: func(_ context.Context, platform, id string) (string, error) {
		if platform != "test" || id != "native" {
			t.Fatalf("wrong owner-native notice: %s %s", platform, id)
		}
		if fail {
			return "", errors.New("identity unavailable")
		}
		return "https://ocman.example/session/ocm_owned?platform=test", nil
	}})
	text := m.conversationNoticeFor(t.Context(), "permission", "test", "native")
	if !strings.Contains(text, "/session/ocm_owned") || strings.Contains(text, "/session/native") {
		t.Fatal(text)
	}
	fail = true
	text = m.conversationNoticeFor(t.Context(), "permission", "test", "native")
	if strings.Contains(text, "/session/") || !strings.Contains(text, "https://ocman.example/") {
		t.Fatalf("missing identity guessed a native destination: %s", text)
	}
}
