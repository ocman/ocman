package plugins

import (
	"encoding/json"
	"strings"
	"testing"
)

func pageDescription() Description {
	return Description{ID: "org.example.pages", Name: "Pages", Version: "1", Protocol: Version{Major: 1}, Scope: ScopeOwner, MaxConcurrency: 2, Capabilities: []Capability{PageCapability}, RequestedGrants: []string{PageProjectGrant}, Pages: []PageDescriptor{{ID: "workspace", Label: "Workspace"}}}
}

func TestPageDeclarationsAndAuthorization(t *testing.T) {
	d := pageDescription()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if !d.PageAllowed("workspace", []string{PageProjectGrant}) || d.PageAllowed("workspace", nil) || d.PageAllowed("other", []string{PageProjectGrant}) {
		t.Fatal("page authorization")
	}
	for _, mutate := range []func(*Description){
		func(d *Description) { d.Scope = ScopeHub }, func(d *Description) { d.RequestedGrants = nil }, func(d *Description) { d.Capabilities = nil }, func(d *Description) { d.Pages = append(d.Pages, d.Pages[0]) }, func(d *Description) { d.Pages = nil },
	} {
		d = pageDescription()
		mutate(&d)
		if d.Validate() == nil {
			t.Fatal("invalid page declaration accepted")
		}
	}
}

func TestPageDataIsBoundedAndTyped(t *testing.T) {
	doc := PageDocument{Revision: "revision", Fields: []PageField{{Key: "target_name", Label: "Target", Type: "text"}}, Values: json.RawMessage(`{"target_name":"<script>inert text</script>"}`)}
	raw, _ := json.Marshal(doc)
	if _, err := DecodePageDocument(raw); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"unknown-field", "html", "duplicate", "depth", "tree", "values", "long"} {
		t.Run(kind, func(t *testing.T) {
			p := doc
			p.Fields = append([]PageField(nil), doc.Fields...)
			switch kind {
			case "html":
				p.Fields[0].Type = "html"
			case "duplicate":
				p.Fields = append(p.Fields, p.Fields[0])
			case "depth":
				for i := 0; i < 6; i++ {
					p.Fields = []PageField{{Key: "nested", Label: "Nested", Type: "list", Fields: p.Fields}}
				}
			case "tree":
				p.Tree = PaneTree{Nodes: []TreeNode{{ID: "a", Title: "a"}}}
			case "values":
				p.Values = []byte(`[]`)
			case "long":
				p.Revision = strings.Repeat("x", 129)
			}
			raw, _ := json.Marshal(p)
			if kind == "unknown-field" {
				raw = []byte(`{"javascript":"bad"}`)
			}
			if _, err := DecodePageDocument(raw); err == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
	for _, raw := range []string{`{"pageId":"workspace","projectId":"../bad"}`, `{"pageId":"workspace"}`, `{"pageId":"workspace","projectId":"project-a","directory":"/etc"}`} {
		if _, err := DecodePageRequest([]byte(raw)); err == nil {
			t.Fatal("invalid page context accepted")
		}
	}
	if _, err := DecodePageRequest([]byte(`{"pageId":"workspace","projectId":"project-a"}`)); err != nil {
		t.Fatal(err)
	}
}
