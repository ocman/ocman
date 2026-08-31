package opencode

import (
	"context"
	"testing"
)

func TestFetchSessionFromOpenCodeCtx_Healthy(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"

	fake := newOpencodeFake(t)
	fake.SetSession(sid, []byte(`{"id":"sess-1","title":"hello","directory":"/tmp/proj","time":{"created":1000,"updated":1500}}`))
	fake.AddMessage(sid, []byte(`{
		"info": {"id":"m1","sessionID":"sess-1","role":"user","time":{"created":1100}},
		"parts": [{"id":"p1","messageID":"m1","sessionID":"sess-1","type":"text","text":"hi"}]
	}`))

	withTestPort(t, dir, fake.Port())
	database := newTestDBWithSession(t, sid, dir)

	a := New(database, nil)
	detail, ok := a.fetchSessionFromOpenCodeCtx(context.Background(), sid, 30, 0)
	if !ok {
		t.Fatalf("fetchSessionFromOpenCodeCtx: ok=false; hits=%v", fake.hits)
	}
	if detail == nil || detail.Session == nil {
		t.Fatalf("nil detail or session")
	}
	if detail.Session.ID != sid {
		t.Errorf("session id = %q, want %q", detail.Session.ID, sid)
	}
	if len(detail.Messages) != 1 {
		t.Errorf("got %d messages, want 1", len(detail.Messages))
	}
}

func TestFetchSessionFromOpenCodeCtx_CarriesLastErrorMetadata(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"

	fake := newOpencodeFake(t)
	fake.SetSession(sid, []byte(`{"id":"sess-1","title":"hello","directory":"/tmp/proj","time":{"created":1000,"updated":1500}}`))
	fake.AddMessage(sid, []byte(`{
		"info": {
			"id":"m1",
			"sessionID":"sess-1",
			"role":"assistant",
			"finish":"error",
			"time":{"created":1100},
			"error":{"name":"ProviderOverloadedError","data":{"message":"provider is overloaded"}}
		},
		"parts": []
	}`))

	withTestPort(t, dir, fake.Port())
	database := newTestDBWithSession(t, sid, dir)

	a := New(database, nil)
	detail, ok := a.fetchSessionFromOpenCodeCtx(context.Background(), sid, 30, 0)
	if !ok {
		t.Fatalf("fetchSessionFromOpenCodeCtx: ok=false; hits=%v", fake.hits)
	}
	if detail.Session.Status != "error" {
		t.Errorf("status = %q, want error", detail.Session.Status)
	}
	if detail.Session.LastErrorName != "ProviderOverloadedError" {
		t.Errorf("LastErrorName = %q, want ProviderOverloadedError", detail.Session.LastErrorName)
	}
	if detail.Session.LastErrorMessage != "provider is overloaded" {
		t.Errorf("LastErrorMessage = %q, want provider is overloaded", detail.Session.LastErrorMessage)
	}
	if detail.Session.LastErrorAt != 1100 {
		t.Errorf("LastErrorAt = %d, want 1100", detail.Session.LastErrorAt)
	}
}

func TestFetchSessionFromOpenCodeCtx_CarriesTopLevelErrorMessage(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"

	fake := newOpencodeFake(t)
	fake.SetSession(sid, []byte(`{"id":"sess-1","title":"hello","directory":"/tmp/proj","time":{"created":1000,"updated":1500}}`))
	fake.AddMessage(sid, []byte(`{
		"info": {
			"id":"m1",
			"sessionID":"sess-1",
			"role":"assistant",
			"finish":"error",
			"time":{"created":1100},
			"error":{"name":"RateLimitError","message":"This request would exceed your account's rate limit. Please try again later. [retrying in 58m attempt #1]"}
		},
		"parts": []
	}`))

	withTestPort(t, dir, fake.Port())
	database := newTestDBWithSession(t, sid, dir)

	a := New(database, nil)
	detail, ok := a.fetchSessionFromOpenCodeCtx(context.Background(), sid, 30, 0)
	if !ok {
		t.Fatalf("fetchSessionFromOpenCodeCtx: ok=false; hits=%v", fake.hits)
	}
	if detail.Session.LastErrorMessage != "This request would exceed your account's rate limit. Please try again later. [retrying in 58m attempt #1]" {
		t.Errorf("LastErrorMessage = %q", detail.Session.LastErrorMessage)
	}
}

func TestFetchSessionFromOpenCodeCtx_Upstream500(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"

	fake := newOpencodeFake(t)
	fake.sessionStatus = 500
	fake.messagesStatus = 500
	withTestPort(t, dir, fake.Port())
	database := newTestDBWithSession(t, sid, dir)

	a := New(database, nil)
	detail, ok := a.fetchSessionFromOpenCodeCtx(context.Background(), sid, 0, 0)
	if ok || detail != nil {
		t.Fatalf("expected ok=false, detail=nil on 500; got ok=%v detail=%+v", ok, detail)
	}
}

func TestFetchSessionFromOpenCodeCtx_MalformedJSON(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"

	fake := newOpencodeFake(t)
	fake.SetSession(sid, []byte(`{"id":"sess-1"}`))
	fake.failJSON = true
	withTestPort(t, dir, fake.Port())
	database := newTestDBWithSession(t, sid, dir)

	a := New(database, nil)
	_, ok := a.fetchSessionFromOpenCodeCtx(context.Background(), sid, 0, 0)
	if ok {
		t.Fatalf("expected ok=false on malformed JSON")
	}
}

func TestFetchSessionFromOpenCodeCtx_NoLivePort(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"
	// No fake; no port mapping.
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		return map[string]string{}
	})
	resetPortCacheForTests()
	t.Cleanup(func() {
		restore()
		resetPortCacheForTests()
	})
	database := newTestDBWithSession(t, sid, dir)

	a := New(database, nil)
	_, ok := a.fetchSessionFromOpenCodeCtx(context.Background(), sid, 0, 0)
	if ok {
		t.Fatalf("expected ok=false when no live port; got ok=true")
	}
}

func TestFetchSessionFromOpenCodeCtx_PaginationCutoff(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"

	fake := newOpencodeFake(t)
	fake.SetSession(sid, []byte(`{"id":"sess-1","time":{"created":1000,"updated":1500}}`))
	fake.AddMessage(sid, []byte(`{
		"info":{"id":"u","sessionID":"sess-1","role":"user","providerID":"anthropic","modelID":"claude-opus-4","time":{"created":1100}},
		"parts":[]
	}`))
	for range 4 {
		fake.AddMessage(sid, []byte(`{
			"info":{"id":"a","sessionID":"sess-1","role":"assistant","providerID":"anthropic","modelID":"claude-opus-4","time":{"created":1200}},
			"parts":[]
		}`))
	}
	withTestPort(t, dir, fake.Port())
	database := newTestDBWithSession(t, sid, dir)

	a := New(database, nil)
	detail, ok := a.fetchSessionFromOpenCodeCtx(context.Background(), sid, 2, 0)
	if !ok {
		t.Fatalf("fetchSessionFromOpenCodeCtx: ok=false")
	}
	if detail.TotalMessages != 5 {
		t.Errorf("TotalMessages = %d, want 5 (count is unaffected by pagination)", detail.TotalMessages)
	}
	if len(detail.Messages) != 2 {
		t.Errorf("paged Messages len = %d, want 2", len(detail.Messages))
	}
	if detail.DefaultModel != "anthropic/claude-opus-4" {
		t.Errorf("default model = %q, want model from user message outside page", detail.DefaultModel)
	}
}

func TestFetchSessionFromOpenCodeCtx_SkipsMessagesWithMissingInfo(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"

	fake := newOpencodeFake(t)
	fake.SetSession(sid, []byte(`{"id":"sess-1","time":{"created":1000,"updated":1500}}`))
	// One valid + one with no info field at all.
	fake.AddMessage(sid, []byte(`{
		"info":{"id":"m1","sessionID":"sess-1","role":"user","time":{"created":1100}},
		"parts":[]
	}`))
	fake.AddMessage(sid, []byte(`{"noinfo":true}`))

	withTestPort(t, dir, fake.Port())
	database := newTestDBWithSession(t, sid, dir)

	a := New(database, nil)
	detail, ok := a.fetchSessionFromOpenCodeCtx(context.Background(), sid, 30, 0)
	if !ok {
		t.Fatalf("fetchSessionFromOpenCodeCtx: ok=false")
	}
	if detail.TotalMessages != 1 {
		t.Errorf("expected exactly 1 valid message, got TotalMessages=%d", detail.TotalMessages)
	}
}
