package plugin

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPageHandler(t *testing.T) {
	d := Description{Pages: []PageDescriptor{{ID: "pentest", Label: "Pentest"}}, Scope: ScopeOwner, Capabilities: []Capability{PageCapability}, RequestedGrants: []string{PageProjectGrant}}
	called := 0
	h := PageHandler(d, func(_ context.Context, method string, r PageRequest) (PageDocument, error) {
		called++
		if r.ProjectID != "project-a" || method != "save" {
			t.Fatal("request changed")
		}
		return PageDocument{Values: json.RawMessage(`{}`), Fields: []PageField{{Key: "name", Label: "Name", Type: "text"}}}, nil
	})
	c := Call{Capability: "page", Version: PageCapability.Version, Method: "save", Params: json.RawMessage(`{"pageId":"pentest","projectId":"project-a","values":{}}`)}
	if _, err := h(t.Context(), c, nil); err != nil || called != 1 {
		t.Fatalf("handler: %v", err)
	}
	c.Method = "execute"
	if _, err := h(t.Context(), c, nil); err == nil || called != 1 {
		t.Fatal("unknown method reached handler")
	}
	c.Method = "save"
	c.Params = []byte(`{"pageId":"other","projectId":"project-a"}`)
	if _, err := h(t.Context(), c, nil); err == nil || called != 1 {
		t.Fatal("undeclared page reached handler")
	}
}
