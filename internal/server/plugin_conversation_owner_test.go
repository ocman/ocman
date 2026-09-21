package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/plugins"
)

// otherOwnerHost stands in for a second machine. Touching it at all is the
// failure this file is about: a connector's session belongs to the machine the
// connector is installed on, whichever machine that is.
type otherOwnerHost struct {
	hostsvc.Host
	touched atomic.Bool
}

func (*otherOwnerHost) RemoteID() string { return "machine" }

func (h *otherOwnerHost) EnsureProjectOpencode(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	h.touched.Store(true)
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:9999", RepoRoot: req.ProjectDir}, nil
}

// conversationEnable approves the current declaration and enables the plugin,
// the way Settings does.
func (f *conversationFixture) enable(t *testing.T, grants []string) {
	t.Helper()
	id := conversationPluginDescription().ID
	p, err := f.s.stateDB.GetPlugin(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(pluginManagementInput{Approval: p.Approval, Grants: &grants})
	if err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/"+id+"/enable", string(body), 200)
}

func (f *conversationFixture) awaitReplies(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for strings.Count(f.replies(), "reply\t") < want {
		if time.Now().After(deadline) {
			t.Fatalf("expected %d replies, saw %q", want, f.replies())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestConversationRunsOnItsOwnerNotAnInferredOne is the local/remote parity
// statement for the conversation flow: whether the connector's owner is the hub
// or a remote, the whole round trip runs on that owner. Here the project
// inventory claims the approved directory belongs to another machine — which is
// what an owner-inferring route would believe — and the conversation still
// starts, prompts and replies locally.
//
// Two failure modes are covered at once. An inventory that resolves to a
// registered owner would run the session on a machine that approved neither the
// plugin nor the project; an inventory naming an owner that is not connected
// would degrade to the hub, which is the silent fallback the flow must not
// have. Both are inferred ownership, and neither is consulted.
func TestConversationRunsOnItsOwnerNotAnInferredOne(t *testing.T) {
	f := newConversationFixture(t)
	elsewhere := &otherOwnerHost{}
	f.s.router().RegisterRemote("machine", elsewhere)
	claimed := "machine"
	f.s.router().SetDirResolver(func(string) string { return claimed })

	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	if prompts := f.awaitPrompts(t, 1); prompts[0] != "ship it" {
		t.Fatalf("prompt %v", prompts)
	}
	if elsewhere.touched.Load() {
		t.Fatal("the conversation launched opencode on a machine that never approved it")
	}

	key := conversationOutboxKey(conversationTestThread)
	mapped, ok, err := f.s.stateDB.GetPluginConversation(t.Context(), key)
	if err != nil || !ok {
		t.Fatalf("thread not mapped: %v %v", ok, err)
	}
	// The mapping is owner-qualified, so a session recorded against the wrong
	// owner would also route every later reply to the wrong machine.
	if mapped.PlatformID != "opencode" {
		t.Fatalf("session mapped to %q, want the owner's own platform", mapped.PlatformID)
	}

	// The same with a disconnected owner: still no fallback, still local.
	claimed = "gone"
	second := conversationMessage("and again")
	second.ThreadID = "slackC7:1700000000.000700"
	if err := f.deliver(t, second); err != nil {
		t.Fatal(err)
	}
	if created := f.createdSessions(); created != 2 {
		t.Fatalf("sessions created: %d, want 2", created)
	}

	// Parity is the whole round trip, not just the start: the completed reply
	// comes back through the owner's own outbox.
	f.s.onSessionIdle("opencode", "ses-chat")
	f.awaitReplies(t, 1)
	if elsewhere.touched.Load() {
		t.Fatal("the conversation reached another machine")
	}
}

// TestConversationDeliveryPausesWhileUnauthorized covers the two ways an owner
// withdraws a connector's authority while a reply is already owed: revoking the
// grant and disabling the installation. Neither may deliver. Neither may spend
// the reply's retry budget either — an operator who disables a connector for an
// hour must not come back to a thread full of dead letters. The mapping and the
// queued reply survive both, and restoring authority resumes exactly the work
// that was owed.
func TestConversationDeliveryPausesWhileUnauthorized(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)
	id := conversationPluginDescription().ID

	// A completed turn the owner no longer authorizes anyone to post.
	if err := f.s.stateDB.SetPluginGrants(t.Context(), id, nil); err != nil {
		t.Fatal(err)
	}
	f.appendReply(t, conversationTestThread, "ses-chat:m2", "owed but unauthorized")
	f.pump(t, f.s)
	assertConversationHeld(t, f, "a revoked grant delivered a reply")

	// Disabling is the same denial reached a different way, and it also stops
	// the connector's process.
	f.call(t, "POST", "/"+id+"/disable", `{}`, 200)
	f.pump(t, f.s)
	assertConversationHeld(t, f, "a disabled connector delivered a reply")

	// Private data outlives the denial: revoking authority is not deletion.
	if _, ok, err := f.s.stateDB.GetPluginConversation(t.Context(), conversationOutboxKey(conversationTestThread)); err != nil || !ok {
		t.Fatalf("the thread mapping was lost while unauthorized: %v %v", ok, err)
	}

	// Restoring authority resumes the owed reply — and only it. Nothing that
	// was denied in between is replayed, because nothing was ever attempted.
	f.enable(t, []string{plugins.ConversationSessionGrant})
	deadline := time.Now().Add(20 * time.Second)
	for strings.Count(f.replies(), "reply\t") == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("re-enabling did not resume the owed reply: %q", f.replies())
		}
		f.pump(t, f.s)
		time.Sleep(10 * time.Millisecond)
	}
	if got := strings.Count(f.replies(), "\n"); got != 1 {
		t.Fatalf("re-enabling posted %d deliveries, want the one that was owed: %q", got, f.replies())
	}
	if status := f.backlog(t); status.Pending != 0 || status.Dead != 0 {
		t.Fatalf("the resumed reply stayed in the backlog: %+v", status)
	}
}

// assertConversationHeld states what a denial must leave behind: nothing posted,
// the reply still pending, and its retry budget untouched so it is not one
// disable away from becoming a dead letter.
func assertConversationHeld(t *testing.T, f *conversationFixture, reason string) {
	t.Helper()
	if f.replies() != "" {
		t.Fatalf("%s: %q", reason, f.replies())
	}
	status := f.backlog(t)
	if status.Pending != 1 || status.Dead != 0 || status.Retrying != 0 {
		t.Fatalf("%s: backlog %+v, want one untouched pending reply", reason, status)
	}
}
