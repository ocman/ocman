package prworkflow

import "testing"

func TestChangesPublishAfterPersistence(t *testing.T) {
	x := newFixture(t)
	var watching []bool
	x.m.d.Changed = func(platform, id string) {
		if platform != "r-box:opencode" || id != "s1" {
			t.Fatalf("wrong identity: %s/%s", platform, id)
		}
		links, err := x.m.links(t.Context(), platform, id)
		if err != nil {
			t.Fatal(err)
		}
		watching = append(watching, len(links) != 0 && links[0].Watching)
	}
	x.link(t)
	x.watch(t)
	// Idempotent starts do not write or publish again.
	x.watch(t)
	if len(watching) != 2 {
		t.Fatalf("idempotent watch published again: %v", watching)
	}
	x.f.pr.Status = "merged"
	x.m.sweep(t.Context())
	if len(watching) < 3 || watching[0] || !watching[1] || watching[len(watching)-1] {
		t.Fatalf("expected linked, watching, automatically stopped; got %v", watching)
	}
	x.request(t, "pull-requests", `{"action":"unlink","host":"github.com","repo":"team/repo","number":42}`, 204)
	before := len(watching)
	x.request(t, "pull-requests", `{"action":"watch","host":"github.com","repo":"team/repo","number":42}`, 404)
	if len(watching) != before {
		t.Fatal("failed mutation published a change")
	}
	// Closed storage must not publish either insertion or deletion failures.
	l := Link{Platform: "r-box:opencode", SessionID: "s1"}
	if err := x.m.d.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := x.m.put(t.Context(), l); err == nil {
		t.Fatal("write to closed storage succeeded")
	}
	if err := x.m.remove(t.Context(), l); err == nil {
		t.Fatal("delete from closed storage succeeded")
	}
	if len(watching) != before {
		t.Fatal("storage failure published a change")
	}
}

func TestForwardedChangesPublishCompoundIdentity(t *testing.T) {
	hub, _, bridge := bridgeFixtures(t)
	changes := 0
	hub.m.d.Changed = func(platform, id string) {
		if platform != "r-box:opencode" || id != "s1" {
			t.Fatalf("wrong forwarded identity: %s/%s", platform, id)
		}
		changes++
	}
	hub.link(t)
	hub.watch(t)
	hub.request(t, "pull-requests", "", 200)
	hub.request(t, "pull-requests", `{"action":"unwatch","host":"github.com","repo":"team/repo","number":42}`, 200)
	hub.request(t, "pull-requests", `{"action":"unlink","host":"github.com","repo":"team/repo","number":42}`, 204)
	if changes != 4 {
		t.Fatalf("expected four mutations, got %d", changes)
	}
	hub.request(t, "pull-requests", `{"action":"unwatch","host":"github.com","repo":"team/repo","number":42}`, 404)
	bridge.lostAck = true
	hub.request(t, "pull-requests", `{"action":"link","remote":"origin","number":42}`, 503)
	if changes != 4 {
		t.Fatal("failed remote operation published a change")
	}
}
