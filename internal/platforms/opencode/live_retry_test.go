package opencode

import (
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestSessionRetryNoticeFromSnapshot(t *testing.T) {
	ResetCachesForTests()
	t.Cleanup(ResetCachesForTests)
	const sid, dir = "retry-session", "/tmp/retry-project"
	fake := newOpencodeFake(t)
	fake.turnStatusJSON = json.RawMessage(`{"retry-session":{"type":"retry","attempt":1,"message":"This request would exceed your account's rate limit. Please try again later.","next":1791456599677}}`)
	fake.SetSession(sid, []byte(`{"id":"retry-session","directory":"/tmp/retry-project","time":{"created":1000,"updated":1500}}`))
	fake.AddMessage(sid, []byte(`{"info":{"id":"m1","sessionID":"retry-session","role":"assistant","time":{"created":1100}},"parts":[]}`))
	withTestPort(t, dir, fake.Port())
	a := New(newTestDBWithSession(t, sid, dir), nil)
	if !a.SeedSessionStatusFromInstance(t.Context(), fake.Port(), 0, nil) {
		t.Fatal("status snapshot failed")
	}
	detail, err := a.Session(t.Context(), sid, 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Session.Status != db.StatusBusy {
		t.Fatalf("status = %s, want busy", detail.Session.Status)
	}
	notice := detail.Session.Notice
	if notice == nil || notice.Kind != "rate_limit" || notice.Attempt != 1 || notice.RetryAt != 1791456599677 {
		t.Fatalf("rate-limit notice missing from conversation/composer session: %+v", notice)
	}
	for _, live := range []bool{true, false} {
		if !live {
			fake.SetSession(sid, nil)
			sessionCache.invalidate(fake.Port(), "/session/"+sid)
		}
		detail, err = a.Session(t.Context(), sid, 30, 0)
		if err != nil || detail.Session.Notice == nil || *detail.Session.Notice != *notice {
			t.Fatalf("live=%v detail notice lost: %+v, %v", live, detail, err)
		}
	}
	rows, err := a.Sessions(t.Context(), dir, 0)
	if err != nil || len(rows) != 1 || rows[0].Notice == nil || *rows[0].Notice != *notice {
		t.Fatalf("list notice lost: %+v, %v", rows, err)
	}
	summary, err := a.SessionSummary(t.Context(), sid)
	if err != nil || summary.Notice == nil || *summary.Notice != *notice {
		t.Fatalf("summary notice lost: %+v, %v", summary, err)
	}
}

func TestLiveRetryNoticeLifecycle(t *testing.T) {
	a := newTestAdapter()
	port := "7777"
	generation := a.StatusPortGeneration(port)
	notice := RetryNotice("rate limited", 42, 1)
	a.ObserveSessionStatus(port, generation, "s1", "busy")
	if !a.ObserveSessionStatus(port, generation, "s1", "retry", notice) {
		t.Fatal("busy to retry must broadcast even though both are running")
	}
	if a.ObserveSessionStatus(port, generation, "s1", "retry", RetryNotice("rate limited", 42, 1)) {
		t.Fatal("identical retry should not broadcast")
	}
	if !a.ObserveSessionStatus(port, generation, "s1", "retry", RetryNotice("rate limited", 100, 2)) {
		t.Fatal("updated retry must broadcast")
	}
	got := a.sessionNoticeOnPort("s1", port)
	if got == nil || got.Attempt != 2 || got.RetryAt != 100 {
		t.Fatalf("notice = %+v", got)
	}
	got.Message = "changed by caller"
	if a.sessionNoticeOnPort("s1", port).Message != "rate limited" {
		t.Fatal("caller mutated registry")
	}
	if a.sessionNoticeOnPort("s1", "8888") != nil {
		t.Fatal("notice leaked to another port")
	}
	seq := a.statusSeq()
	a.ObserveSessionStatus(port, generation, "s1", "busy")
	a.SeedSessionStatus(port, generation, seq, map[string]string{"s1": "retry"}, map[string]*db.SessionNotice{"s1": notice})
	if a.sessionNoticeOnPort("s1", port) != nil {
		t.Fatal("stale retry snapshot overwrote newer busy event")
	}
	for _, status := range []string{"busy", "idle"} {
		a.ObserveSessionStatus(port, generation, "s1", "retry", notice)
		if !a.ObserveSessionStatus(port, generation, "s1", status) || a.sessionNoticeOnPort("s1", port) != nil {
			t.Fatalf("%s did not clear retry notice", status)
		}
	}
	a.ObserveSessionStatus(port, generation, "s1", "retry", notice)
	a.ClearSessionStatusForPort(port)
	a.ObserveSessionStatus(port, generation, "s1", "retry", notice)
	if a.sessionNoticeOnPort("s1", port) != nil {
		t.Fatal("departed port or stale stream left retry notice")
	}
	if (*Adapter)(nil).sessionNoticeOnPort("s1", port) != nil || (&Adapter{}).sessionNoticeOnPort("s1", port) != nil || a.sessionNoticeOnPort("s1", "") != nil {
		t.Fatal("unavailable registry returned a notice")
	}
}

func TestRetryNotice(t *testing.T) {
	for _, tc := range []struct{ message, kind string }{
		{"This request would exceed your account's rate limit.", "rate_limit"},
		{"RateLimitError", "rate_limit"},
		{"rate_limit", "rate_limit"},
		{"rate-limit", "rate_limit"},
		{"Too many requests", "rate_limit"},
		{"Provider is overloaded", "provider_overloaded"},
		{"At capacity", "provider_overloaded"},
		{"Capacity exceeded", "provider_overloaded"},
		{"Connection reset", "retry"},
		{"", "retry"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			got := RetryNotice(tc.message, 42, 3)
			if got.Kind != tc.kind || got.Message == "" || got.RetryAt != 42 || got.Attempt != 3 {
				t.Fatalf("notice = %+v", got)
			}
		})
	}
}
