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

func TestPageValuesMatchSchema(t *testing.T) {
	fields := []PageField{{Key: "targets", Label: "Targets", Type: "list", Fields: []PageField{
		{Key: "name", Label: "Name", Type: "text"},
		{Key: "notes", Label: "Notes", Type: "multiline"},
		{Key: "secret", Label: "Secret", Type: "secret"},
		{Key: "enabled", Label: "Enabled", Type: "boolean"},
		{Key: "children", Label: "Children", Type: "list", Fields: []PageField{{Key: "name", Label: "Name", Type: "text"}}},
	}}}
	for _, tc := range []struct {
		values string
		valid  bool
	}{
		{`{}`, true},
		{`{"targets":[]}`, true},
		{`{"targets":[{}]}`, true},
		{`{"targets":[{"name":"a","notes":"b","secret":"","enabled":false,"children":[{"name":"c"}]}]}`, true},
		{`{"targets":[null]}`, false},
		{`{"targets":[[]]}`, false},
		{`{"targets":["row"]}`, false},
		{`{"targets":{}}`, false},
		{`{"targets":null}`, false},
		{`{"targets":[{"name":null}]}`, false},
		{`{"targets":[{"name":true}]}`, false},
		{`{"targets":[{"notes":42}]}`, false},
		{`{"targets":[{"secret":{}}]}`, false},
		{`{"targets":[{"enabled":"false"}]}`, false},
		{`{"targets":[{"enabled":null}]}`, false},
		{`{"targets":[{"children":[null]}]}`, false},
	} {
		t.Run(tc.values, func(t *testing.T) {
			raw, _ := json.Marshal(PageDocument{Fields: fields, Values: json.RawMessage(tc.values)})
			_, err := DecodePageDocument(raw)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
		})
	}
}

func TestPageListDepthBoundary(t *testing.T) {
	for _, depth := range []int{4, 5} {
		fields := []PageField{{Key: "name", Label: "Name", Type: "text"}}
		for i := 0; i < depth; i++ {
			fields = []PageField{{Key: "items", Label: "Items", Type: "list", Fields: fields}}
		}
		raw, _ := json.Marshal(PageDocument{Fields: fields, Values: json.RawMessage(`{}`)})
		_, err := DecodePageDocument(raw)
		if (err == nil) != (depth == 4) {
			t.Fatalf("depth=%d, err=%v", depth, err)
		}
	}
}

func TestPageResultsOnlyDocumentHasEmptySchema(t *testing.T) {
	for _, schema := range []string{`"fields":null`, `"fields":[]`, ""} {
		parts := []string{`"revision":"r"`, `"values":{}`, `"tree":{"available":true}`}
		if schema != "" {
			parts = append(parts, schema)
		}
		doc, err := DecodePageDocument([]byte("{" + strings.Join(parts, ",") + "}"))
		if err != nil {
			t.Fatalf("schema %s refused: %v", schema, err)
		}
		if doc.Fields == nil {
			t.Fatalf("schema %s reaches the renderer as null", schema)
		}
		blob, _ := json.Marshal(doc)
		if !strings.Contains(string(blob), `"fields":[]`) {
			t.Fatalf("schema %s serialized as %s", schema, blob)
		}
	}
	if _, err := DecodePageDocument([]byte(`{"revision":"r","fields":[null],"values":{},"tree":{"available":true}}`)); err == nil {
		t.Fatal("null schema row accepted")
	}
}

// Only the schema itself is optional; its shape stays under the same strict,
// recursive decode as the rest of the protocol.
func TestPageSchemaStaysStrict(t *testing.T) {
	for _, schema := range []string{
		`[{"key":"a","label":"A","type":"text","javascript":"alert(1)"}]`,
		`[{"Key":"a","key":"a","label":"A","type":"text"}]`,
		`[{"key":null,"label":"A","type":"text"}]`,
		`{"key":"a","label":"A","type":"text"}`,
		`[{"key":"a","label":"A","type":"list","fields":{"key":"b","label":"B","type":"text"}}]`,
		`[{"key":"a","label":"A","type":"list","fields":[{"key":"b","label":"B","type":"text","extra":1}]}]`,
	} {
		raw := []byte(`{"revision":"r","fields":` + schema + `,"values":{},"tree":{"available":true}}`)
		if _, err := DecodePageDocument(raw); err == nil {
			t.Fatalf("malformed schema accepted: %s", schema)
		}
	}
	raw := []byte(`{"revision":"r","fields":[{"key":"a","label":"A","type":"list","fields":[{"key":"b","label":"B","type":"text"}]}],"values":{},"tree":{"available":true}}`)
	if _, err := DecodePageDocument(raw); err != nil {
		t.Fatalf("valid schema refused: %v", err)
	}
}
