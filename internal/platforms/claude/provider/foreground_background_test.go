package claudeprovider

import (
	"encoding/json"
	"testing"
)

func TestForegroundIdleRetainsBackgroundProcessAndSnapshot(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.system(p, []byte(`{"subtype":"task_started","task_id":"vite","task_type":"local_bash","description":"pnpm dev"}`))
	l.system(p, []byte(`{"subtype":"session_state_changed","state":"running"}`))
	l.result(p, []byte(`{"subtype":"success"}`))
	if !p.busy {
		t.Fatal("foreground running state must still hold the result")
	}
	l.system(p, []byte(`{"subtype":"session_state_changed","state":"idle"}`))
	if p.busy || len(p.tasks) != 1 || p.idle != nil {
		t.Fatal("idle foreground must preserve the background process without scheduling eviction")
	}
	found := false
	for _, e := range l.snapshot(p.id) {
		if e.Type != "ocman.session.background" {
			continue
		}
		var props struct {
			Count int                 `json:"count"`
			Tasks []map[string]string `json:"tasks"`
		}
		if err := json.Unmarshal(e.Properties, &props); err != nil {
			t.Fatal(err)
		}
		found = props.Count == 1 && len(props.Tasks) == 1 && props.Tasks[0]["id"] == "vite"
	}
	if !found {
		t.Fatal("idle snapshot lost background task controls")
	}
	l.system(p, []byte(`{"subtype":"task_notification","task_id":"vite"}`))
	if p.busy || len(p.tasks) != 0 || p.idle == nil {
		t.Fatal("last background completion must enable process cleanup without a foreground turn")
	}
}

func TestForegroundFollowupReusesBackgroundHost(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.system(p, []byte(`{"subtype":"task_started","task_id":"vite","task_type":"local_bash"}`))
	l.result(p, []byte(`{"subtype":"success"}`))
	got, err := l.acquire(t.Context(), p.id, "", "high", "next", &waitTicket{})
	if err != nil || got != p || !p.busy || !p.tasks["vite"] {
		t.Fatalf("followup replaced the background host: %v", err)
	}
}
