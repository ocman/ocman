package prworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/platforms"
)

type workflowBridge struct {
	owner   *fixture
	err     error
	lostAck bool
}

func (b *workflowBridge) PullRequestOperation(ctx context.Context, req platforms.PullRequestRequest) (platforms.PullRequestResponse, error) {
	if b.err != nil {
		return platforms.PullRequestResponse{}, b.err
	}
	response := b.owner.m.Operation(ctx, "opencode", req)
	if b.lostAck {
		return platforms.PullRequestResponse{}, errors.New("lost acknowledgement")
	}
	return response, nil
}

func ownerFixture(t *testing.T) *fixture {
	x := newFixture(t)
	x.m.d.Target = func(_ http.ResponseWriter, _ *http.Request, id string) (Target, bool) {
		return Target{"opencode", id, "/repo", "local"}, true
	}
	x.m.d.Enqueue = func(_ context.Context, platform string, req platforms.SendMessageRequest) error {
		if platform != "opencode" {
			t.Errorf("delivery escaped owner: %s", platform)
		}
		x.sent = append(x.sent, req.Message)
		return nil
	}
	return x
}

func bridgeFixtures(t *testing.T) (*fixture, *fixture, *workflowBridge) {
	hub, owner := newFixture(t), ownerFixture(t)
	bridge := &workflowBridge{owner: owner}
	hub.m.d.Remote = func(Target) (platforms.PullRequestWorkflow, error) { return bridge, nil }
	return hub, owner, bridge
}

func ownerLink(t *testing.T, owner *fixture) Link {
	t.Helper()
	links, err := owner.m.links(t.Context(), "opencode", "s1")
	if err != nil || len(links) != 1 {
		t.Fatalf("owner links=%+v err=%v", links, err)
	}
	return links[0]
}

func TestOwnerWorkflowHasSameLocalAndRemoteActions(t *testing.T) {
	hub, owner, bridge := bridgeFixtures(t)
	hub.link(t)
	hub.watch(t)
	if l := ownerLink(t, owner); !l.Watching || l.RemoteID != "local" || l.Platform != "opencode" {
		t.Fatalf("owner link=%+v", l)
	}
	response := hub.request(t, "pull-requests", "", 200)
	var listed struct {
		PullRequests []Link `json:"pullRequests"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil || len(listed.PullRequests) != 1 || listed.PullRequests[0].Platform != "r-box:opencode" || listed.PullRequests[0].RemoteID != "box" {
		t.Fatalf("hub result=%s err=%v", response.Body, err)
	}
	hub.request(t, "pull-request-checks?number=42&host=github.com&repo=team/repo&sha=abcd", "", 200)
	hub.request(t, "pull-request-feedback", `{"action":"checks","number":42,"host":"github.com","repo":"team/repo"}`, 200)
	owner.f.checks = forge.CIStatus{State: forge.CIStateFailure, Checks: []forge.Check{{Name: "tests", State: forge.CIStateFailure}}}
	hub.m.sweep(t.Context())
	owner.m.sweep(t.Context())
	if len(hub.sent) != 0 || hub.f.reads != 0 || len(owner.sent) != 1 {
		t.Fatalf("hub reads=%d sends=%v owner sends=%v", hub.f.reads, hub.sent, owner.sent)
	}
	bridge.err = platforms.ErrRemoteOffline
	hub.request(t, "pull-requests", "", 503)
	bridge.err = nil
	hub.request(t, "pull-requests", `{"action":"unwatch","number":42,"host":"github.com","repo":"team/repo"}`, 200)
	if ownerLink(t, owner).Watching {
		t.Fatal("unwatch did not reach owner")
	}
	hub.request(t, "pull-requests", `{"action":"unlink","number":42,"host":"github.com","repo":"team/repo"}`, 204)
	links, err := owner.m.links(t.Context(), "opencode", "s1")
	if err != nil || len(links) != 0 {
		t.Fatalf("unlink: %+v %v", links, err)
	}
}

func TestLegacyWatchTransferPreservesProgressAndDoesNotPollOnHub(t *testing.T) {
	hub, owner := newFixture(t), ownerFixture(t)
	hub.link(t)
	hub.watch(t)
	l := hub.only(t)
	l.Pending, l.Failures, l.NextAt, l.CommentWakes = "pending update", 2, 123, 3
	if err := hub.m.put(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	reads := hub.f.reads
	bridge := &workflowBridge{owner: owner, lostAck: true}
	hub.m.d.Remote = func(Target) (platforms.PullRequestWorkflow, error) { return bridge, nil }
	hub.m.sweep(t.Context())
	transferred := ownerLink(t, owner)
	if !transferred.Watching || transferred.Pending != l.Pending || transferred.Failures != 2 || transferred.NextAt != 123 || transferred.CommentWakes != 3 || transferred.Snapshot == nil {
		t.Fatalf("lost transfer state: %+v", transferred)
	}
	if hub.f.reads != reads || len(hub.sent) != 0 {
		t.Fatal("hub polled or delivered during transfer")
	}
	// A lost ack must not resurrect a link unlinked on the owner before retry.
	if err := owner.m.remove(t.Context(), transferred); err != nil {
		t.Fatal(err)
	}
	bridge.lostAck = false
	hub.m.sweep(t.Context())
	for _, x := range []*fixture{hub, owner} {
		links, err := x.m.links(t.Context(), "", "")
		if err != nil || len(links) != 0 {
			t.Fatalf("transfer retry resurrected state: %+v %v", links, err)
		}
	}
}

func TestTransferFailureRetainsHubStateAndFailsClosed(t *testing.T) {
	hub := newFixture(t)
	hub.link(t)
	hub.watch(t)
	hub.m.d.Remote = func(Target) (platforms.PullRequestWorkflow, error) { return nil, platforms.ErrRemoteOffline }
	hub.m.sweep(t.Context())
	hub.request(t, "pull-requests", "", 503)
	if !hub.only(t).Watching {
		t.Fatal("disconnection discarded watch")
	}
	hub.m.d.Remote = func(Target) (platforms.PullRequestWorkflow, error) {
		return &workflowBridge{owner: ownerFixture(t), err: platforms.ErrRemoteOffline}, nil
	}
	hub.request(t, "pull-requests", "", 503)
	if !hub.only(t).Watching {
		t.Fatal("failed transfer discarded watch")
	}
}

func TestOwnerOperationValidationAndCounts(t *testing.T) {
	owner := ownerFixture(t)
	for _, req := range []platforms.PullRequestRequest{
		{Operation: "unknown"},
		{Operation: "created", Body: json.RawMessage(`{}`)},
		{Operation: "import", Body: json.RawMessage(`{}`)},
		{Operation: "change", Body: json.RawMessage(`{}`)},
		{Operation: "checks", Query: url.Values{"sha": {"not-a-sha"}}},
	} {
		if got := owner.m.Operation(t.Context(), "opencode", req); got.Status != 400 {
			t.Fatalf("%s returned %+v", req.Operation, got)
		}
	}
	if err := owner.m.LinkCreated(t.Context(), Target{"opencode", "s1", "/repo", "local"}, "origin", 42); err != nil {
		t.Fatal(err)
	}
	l := ownerLink(t, owner)
	l.Watching = true
	if err := owner.m.put(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	response := owner.m.Operation(t.Context(), "opencode", platforms.PullRequestRequest{Operation: "counts"})
	if response.Status != 200 || !strings.Contains(string(response.Body), `"s1":1`) {
		t.Fatalf("counts=%+v", response)
	}
	response = owner.m.Operation(t.Context(), "other", platforms.PullRequestRequest{Operation: "counts"})
	if strings.Contains(string(response.Body), "s1") {
		t.Fatalf("cross-platform counts=%s", response.Body)
	}
}

func TestRemoteCreatedLinksAndBodyValidation(t *testing.T) {
	hub, owner, bridge := bridgeFixtures(t)
	target := Target{"r-box:opencode", "s1", "/repo", "box"}
	if err := hub.m.LinkCreated(t.Context(), target, "origin", 42, &owner.f.pr); err != nil {
		t.Fatal(err)
	}
	if ownerLink(t, owner).Number != 42 {
		t.Fatal("created link did not reach owner")
	}
	bridge.err = platforms.ErrRemoteOffline
	if err := hub.m.LinkCreated(t.Context(), target, "origin", 42); err == nil {
		t.Fatal("offline creation succeeded")
	}
	bridge.err = nil
	hub.request(t, "pull-requests", `{"action":`, 400)
	hub.request(t, "pull-requests", strings.Repeat("x", 4097), 400)
}
