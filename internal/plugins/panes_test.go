package plugins

import (
	"strings"
	"testing"
)

func paneDescription() Description {
	return Description{ID: "org.example.tree", Name: "Tree", Version: "1", Protocol: Version{Major: 1}, Scope: ScopeOwner, MaxConcurrency: 1,
		Capabilities: []Capability{PaneCapability}, RequestedGrants: []string{PaneProjectGrant}, Panes: []PaneDescriptor{{ID: "items", Label: "Items"}}}
}

func TestPaneDescription(t *testing.T) {
	d := paneDescription()
	if d.Validate() != nil || !d.PaneAllowed("items", []string{PaneProjectGrant}) {
		t.Fatal("valid pane rejected")
	}
	for _, change := range []func(*Description){
		func(d *Description) { d.Scope = ScopeHub }, func(d *Description) { d.RequestedGrants = nil },
		func(d *Description) { d.Panes = nil }, func(d *Description) { d.Capabilities = nil },
		func(d *Description) { d.Panes = append(d.Panes, d.Panes[0]) },
		func(d *Description) { d.Panes[0].ID = "bad/id" }, func(d *Description) { d.Panes[0].Label = "bad\nlabel" },
		func(d *Description) { d.Panes = make([]PaneDescriptor, 17) },
	} {
		d := paneDescription()
		change(&d)
		if d.Validate() == nil {
			t.Fatalf("accepted %+v", d)
		}
	}
	if d.PaneAllowed("missing", []string{PaneProjectGrant}) || d.PaneAllowed("items", nil) {
		t.Fatal("unapproved pane allowed")
	}
	d.Capabilities[0].Version.Minor = 1
	if !d.PaneAllowed("items", []string{PaneProjectGrant}) {
		t.Fatal("compatible minor rejected")
	}
	d.Capabilities[0].Version.Major = 2
	if d.PaneAllowed("items", []string{PaneProjectGrant}) {
		t.Fatal("incompatible major allowed")
	}
}

func TestPaneInputs(t *testing.T) {
	r := PaneRequest{PluginID: "org.example.tree", PaneID: "items", OwnerID: "machine", Directory: "/repo"}
	if r.Validate() != nil {
		t.Fatal("valid request rejected")
	}
	for _, change := range []func(*PaneRequest){
		func(r *PaneRequest) { r.PluginID = "bad" }, func(r *PaneRequest) { r.PaneID = "bad/id" },
		func(r *PaneRequest) { r.OwnerID = "" }, func(r *PaneRequest) { r.Directory = "relative" },
		func(r *PaneRequest) { r.Directory = "/repo\x00" }, func(r *PaneRequest) { r.Directory = "/" + strings.Repeat("a", 4096) },
	} {
		next := r
		change(&next)
		if next.Validate() == nil {
			t.Fatalf("accepted %+v", next)
		}
	}
	for _, data := range []string{`{"paneId":"items","directory":"/repo"}`, `{"paneId":"items","directory":"/repo","unknown":true}`, `{"paneId":"items","directory":"relative"}`} {
		_, err := DecodePaneRead([]byte(data))
		if (err == nil) != (data == `{"paneId":"items","directory":"/repo"}`) {
			t.Fatalf("%s: %v", data, err)
		}
	}
}

func TestPaneTree(t *testing.T) {
	for _, data := range []string{
		`{"available":false}`, `{"available":true,"nodes":[]}`,
		`{"available":true,"warning":true,"nodes":[{"id":"a","title":"<script>","status":"open","badge":"P1","kind":"task"},{"id":"b","title":"Child","parentId":"a"}]}`,
		`{"available":true,"nodes":[{"id":"a","title":"A","parentId":"missing"}]}`,
	} {
		if _, err := DecodePaneTree([]byte(data)); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
	}
	for _, data := range []string{
		`null`, `{`, `{"available":null}`, `{"Available":true}`, `{"available":true,"html":"<script>"}`,
		`{"available":true,"available":false}`, `{"available":false,"warning":true}`,
		`{"available":false,"nodes":[{"id":"a","title":"A"}]}`,
		`{"available":true,"nodes":[{"id":"a","title":"A"},{"id":"a","title":"B"}]}`,
		`{"available":true,"nodes":[{"id":"a","title":"A","parentId":"a"}]}`,
		`{"available":true,"nodes":[{"id":"a","title":"A","parentId":"b"},{"id":"b","title":"B","parentId":"a"}]}`,
		`{"available":true,"nodes":[{"id":"","title":"A"}]}`, `{"available":true,"nodes":[{"id":"a","title":""}]}`,
		`{"available":true,"nodes":[{"id":"a","title":"A","status":"bad"}]}`,
		`{"available":true,"nodes":[{"id":"a","title":"bad\u0000"}]}`,
		strings.Repeat(" ", MaxMessageBytes+1),
	} {
		if _, err := DecodePaneTree([]byte(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}
