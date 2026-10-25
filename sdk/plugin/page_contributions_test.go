package plugin

import (
	"context"
	"encoding/json"
	"testing"
)

func TestContributionPageHandler(t *testing.T) {
	d := Description{Pages: []PageDescriptor{{ID: "jobs", Label: "Jobs", Validate: true}}, Scope: ScopeOwner, Capabilities: []Capability{PageContributionCapability}, RequestedGrants: []string{PageProjectGrant}}
	called := 0
	h := PageHandler(d, func(_ context.Context, method string, req PageRequest) (PageDocument, error) {
		called++
		return PageDocument{Values: json.RawMessage(`{}`), Issues: []PageIssue{{Path: "/name", Message: "Unavailable"}}}, nil
	})
	call := Call{Capability: "page", Version: PageContributionCapability.Version, Method: "validate", Params: json.RawMessage(`{"pageId":"jobs","projectId":"project-a","values":{"name":"a"}}`)}
	if _, err := h(t.Context(), call, nil); err != nil || called != 1 {
		t.Fatalf("validate: %v", err)
	}
	for _, mutate := range []func(*Call){
		func(c *Call) { c.Version.Minor = 0 }, func(c *Call) { c.Version.Minor = 2 }, func(c *Call) { c.Version.Major = 2 },
		func(c *Call) { c.Params = []byte(`{"pageId":"jobs","projectId":"project-a","cursor":"next"}`) },
	} {
		c := call
		mutate(&c)
		if _, err := h(t.Context(), c, nil); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	d.Pages[0].Validate = false
	if _, err := h(t.Context(), call, nil); err == nil {
		t.Fatal("undeclared hook called")
	}
	d.Pages[0].Validate = true
	call.Method = "read"
	call.Version = PageCapability.Version
	call.Params = []byte(`{"pageId":"jobs","projectId":"project-a"}`)
	if _, err := h(t.Context(), call, nil); err == nil {
		t.Fatal("extension result leaked over 1.0")
	}
}
