package autoapprove

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestTeeInternalHelpersDoNotPublishSessionActivity(t *testing.T) {
	for _, title := range []string{"(worktree-name subagent)", "(auto-approve subagent)", "Explore (@explore subagent)"} {
		t.Run(title, func(t *testing.T) {
			var forwarded bytes.Buffer
			callbacks := 0
			tee := &Tee{W: &forwarded,
				OnSessionChanged:     func(string) { callbacks++ },
				OnSessionTitle:       func(string, string) { callbacks++ },
				OnSessionDataChanged: func(string) { callbacks++ },
				OnUserPrompt:         func(string, int64) { callbacks++ },
			}
			input := fmt.Sprintf("data: {\"type\":\"session.created\",\"properties\":{\"info\":{\"id\":\"helper\",\"title\":%q}}}\n\n", title) +
				"data: {\"type\":\"message.updated\",\"properties\":{\"info\":{\"sessionID\":\"helper\",\"role\":\"user\",\"time\":{\"created\":1}}}}\n\n" +
				"data: {\"type\":\"session.deleted\",\"properties\":{\"info\":{\"id\":\"helper\"}}}\n\n"
			if _, err := tee.Write([]byte(input)); err != nil {
				t.Fatal(err)
			}
			if forwarded.String() != input {
				t.Fatal("helper filtering changed forwarded SSE bytes")
			}
			if title == "Explore (@explore subagent)" {
				if callbacks == 0 {
					t.Fatal("ordinary agent child was filtered as an internal helper")
				}
			} else if callbacks != 0 {
				t.Fatalf("internal helper triggered %d user-facing refresh callbacks", callbacks)
			}
		})
	}
}

func TestTeeHelperFilteringDoesNotHidePermissionPrompts(t *testing.T) {
	var prompt platforms.LivePrompt
	tee := &Tee{W: &bytes.Buffer{}, OnPromptAsked: func(_, _ string, got platforms.LivePrompt) { prompt = got }}
	tee.dispatchEvent("", `{"type":"session.created","properties":{"info":{"id":"helper","title":"(auto-approve subagent)"}}}`)
	tee.dispatchEvent("", `{"type":"permission.v2.asked","properties":{"id":"permission","sessionID":"helper","action":"bash","resources":["git commit *"]}}`)
	if prompt["sessionID"] != "helper" {
		t.Fatalf("helper filter swallowed a permission request: %+v", prompt)
	}
}

func TestTeeHelperTrackingIsBoundedAndReleasesDeletedIDs(t *testing.T) {
	tee := &Tee{}
	for i := range 4100 {
		data := fmt.Sprintf(`{"properties":{"info":{"id":"helper-%d","title":"(worktree-name subagent)"}}}`, i)
		tee.internalHelperEvent("session.created", data)
	}
	if len(tee.internalHelpers) != 4096 {
		t.Fatalf("unbounded helper metadata: %d", len(tee.internalHelpers))
	}
	if !tee.internalHelperEvent("session.deleted", `{"properties":{"sessionID":"helper-0"}}`) || len(tee.internalHelpers) != 4095 {
		t.Fatal("helper deletion did not release its tracking entry")
	}
	if tee.internalHelperEvent("session.created", `{"properties":{"info":{"id":"child","parentID":"parent","title":"(worktree-name subagent)"}}}`) {
		t.Fatal("a native child was mistaken for an internal parentless helper")
	}
}
