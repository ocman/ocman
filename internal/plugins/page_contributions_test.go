package plugins

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestPageContributionDeclarationAndVersion(t *testing.T) {
	d := pageDescription()
	d.Pages[0].Validate = true
	if d.Validate() == nil {
		t.Fatal("1.0 accepted validation hook")
	}
	d.Capabilities = []Capability{PageContributionCapability}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if !d.PageAllowed("workspace", []string{PageProjectGrant}) {
		t.Fatal("1.1 page denied")
	}
	d.Capabilities[0].Version.Minor = 5
	if d.PageVersion() != PageModelCapability.Version {
		t.Fatal("unsupported minor selected")
	}
	d.Capabilities = nil
	if d.PageVersion() != (Version{}) {
		t.Fatal("undeclared version")
	}
	for _, raw := range []string{
		`{"pageId":"workspace","projectId":"project-a","itemId":"../escape"}`,
		`{"pageId":"workspace","projectId":"project-a","cursor":"bad\nvalue"}`,
	} {
		if _, err := DecodePageRequest([]byte(raw)); err == nil {
			t.Fatal("unsafe request accepted")
		}
	}
	for _, tc := range []struct {
		method string
		req    PageRequest
		minor  int
		valid  bool
	}{
		{"read", PageRequest{}, 0, true}, {"save", PageRequest{}, 0, true},
		{"validate", PageRequest{}, 0, false}, {"validate", PageRequest{}, 1, true},
		{"execute", PageRequest{}, 1, false}, {"read", PageRequest{ItemID: "a"}, 0, false},
		{"read", PageRequest{Cursor: "next"}, 1, true}, {"save", PageRequest{Cursor: "next"}, 1, false},
		{"read", PageRequest{Values: json.RawMessage(`{}`)}, 1, false},
	} {
		tc.req.ProjectID = "project-a"
		if (ValidatePageCall(tc.method, tc.req, Version{Major: 1, Minor: tc.minor}) == nil) != tc.valid {
			t.Fatalf("call %+v", tc)
		}
	}
}

func contributionDocument() PageDocument {
	return PageDocument{Values: json.RawMessage(`{}`), Collection: &PageCollection{Title: "Jobs", Columns: []PageColumn{{Key: "name", Label: "Name", Display: "text"}}, Rows: []PageRow{{ID: "job-1", Values: map[string]any{"name": "<script>inert</script>"}}}}, Detail: &PageDetail{Title: "Job", Properties: []PageProperty{{Label: "Active", Display: "boolean", Value: true}}}}
}

func TestPageContributionContent(t *testing.T) {
	for _, mutate := range []func(*PageDocument){
		func(p *PageDocument) {},
		func(p *PageDocument) { p.Collection.Rows[0].Values["name"] = "ok" },
	} {
		p := contributionDocument()
		mutate(&p)
		raw, _ := json.Marshal(p)
		if _, err := DecodePageDocument(raw); err != nil {
			t.Fatal(err)
		}
		if ValidatePageVersion(p, PageCapability.Version) == nil {
			t.Fatal("1.1 leaked to 1.0")
		}
		if err := ValidatePageVersion(p, PageContributionCapability.Version); err != nil {
			t.Fatal(err)
		}
	}
	for name, mutate := range map[string]func(*PageDocument){
		"duplicate-row":         func(p *PageDocument) { p.Collection.Rows = append(p.Collection.Rows, p.Collection.Rows[0]) },
		"duplicate-column":      func(p *PageDocument) { p.Collection.Columns = append(p.Collection.Columns, p.Collection.Columns[0]) },
		"extra-cell":            func(p *PageDocument) { p.Collection.Rows[0].Values["html"] = "bad" },
		"wrong-cell":            func(p *PageDocument) { p.Collection.Rows[0].Values["name"] = true },
		"missing-cell":          func(p *PageDocument) { delete(p.Collection.Rows[0].Values, "name") },
		"unknown-display":       func(p *PageDocument) { p.Collection.Columns[0].Display = "html" },
		"no-columns":            func(p *PageDocument) { p.Collection.Columns = nil },
		"too-many-rows":         func(p *PageDocument) { p.Collection.Rows = make([]PageRow, 201) },
		"too-many-columns":      func(p *PageDocument) { p.Collection.Columns = make([]PageColumn, 17) },
		"cursor":                func(p *PageDocument) { p.Collection.NextCursor = strings.Repeat("a", 1025) },
		"title":                 func(p *PageDocument) { p.Detail.Title = "" },
		"property-type":         func(p *PageDocument) { p.Detail.Properties[0].Value = "true" },
		"too-many-properties":   func(p *PageDocument) { p.Detail.Properties = make([]PageProperty, 65) },
		"issue-path":            func(p *PageDocument) { p.Issues = []PageIssue{{Path: "not-pointer", Message: "Bad"}} },
		"issue-escape":          func(p *PageDocument) { p.Issues = []PageIssue{{Path: "/bad~2", Message: "Bad"}} },
		"issue-trailing-escape": func(p *PageDocument) { p.Issues = []PageIssue{{Path: "/bad~", Message: "Bad"}} },
		"issue-message":         func(p *PageDocument) { p.Issues = []PageIssue{{Path: "/name", Message: ""}} },
		"issue-limit":           func(p *PageDocument) { p.Issues = make([]PageIssue, 129) },
	} {
		t.Run(name, func(t *testing.T) {
			p := contributionDocument()
			mutate(&p)
			raw, _ := json.Marshal(p)
			if _, err := DecodePageDocument(raw); err == nil {
				t.Fatal("invalid content accepted")
			}
		})
	}
	for _, tc := range []struct {
		display string
		value   any
		valid   bool
	}{
		{"text", "hello", true}, {"text", strings.Repeat("x", 4097), false},
		{"number", float64(2), true}, {"number", math.Inf(1), false}, {"number", "2", false},
		{"boolean", false, true}, {"status", "blocked", true}, {"status", "custom-css", false}, {"html", "<b>x</b>", false},
	} {
		if validPageDisplay(tc.display, tc.value) != tc.valid {
			t.Fatalf("display %+v", tc)
		}
	}
}

func TestPageStaticRules(t *testing.T) {
	fields := []PageField{
		{Key: "name", Label: "Name", Type: "text", Required: true, MinLength: ptr(2), MaxLength: ptr(4)},
		{Key: "count", Label: "Count", Type: "integer", Minimum: ptr(1.0), Maximum: ptr(3.0)},
		{Key: "enabled", Label: "Enabled", Type: "boolean", Required: true},
		{Key: "mode", Label: "Mode", Type: "select", Options: []PageOption{{Value: "fast", Label: "Fast"}}},
		{Key: "rows", Label: "Rows", Type: "list", MinItems: ptr(1), MaxItems: ptr(2), Fields: []PageField{{Key: "name", Label: "Name", Type: "text", Required: true}}},
	}
	for _, tc := range []struct {
		raw  string
		path string
	}{
		{`{"name":"ok","count":2,"enabled":false,"mode":"fast","rows":[{"name":"a"}]}`, ""},
		{`{"name":"","rows":[{"name":"a"}]}`, "/name"},
		{`{"name":"a"}`, "/name"}, {`{"name":"abcde"}`, "/name"},
		{`{"name":"ok","count":0}`, "/count"}, {`{"name":"ok","count":4}`, "/count"}, {`{"name":"ok","count":1.5}`, "/count"},
		{`{"name":"ok","enabled":null}`, "/enabled"}, {`{"name":"ok","mode":"bad"}`, "/mode"},
		{`{"name":"ok","rows":[]}`, "/rows"}, {`{"name":"ok","rows":[{},{},{}]}`, "/rows"},
		{`{"name":"ok","rows":[{}]}`, "/rows/0/name"},
		{`{"name":"ok","rows":[null]}`, "/rows"},
	} {
		values, err := PageValuesObject([]byte(tc.raw))
		if err != nil {
			t.Fatal(err)
		}
		issues := ValidatePageInput(fields, values)
		if tc.path == "" {
			if len(issues) != 0 {
				t.Fatalf("valid input: %+v", issues)
			}
			continue
		}
		found := false
		for _, issue := range issues {
			found = found || issue.Path == tc.path
		}
		if !found {
			t.Fatalf("%s: %+v", tc.raw, issues)
		}
	}
	for _, raw := range []string{`null`, `[]`, `invalid`} {
		if _, err := PageValuesObject([]byte(raw)); err == nil {
			t.Fatal("non-object accepted")
		}
	}
	values, _ := PageValuesObject([]byte(`{"extra":"value"}`))
	if len(ValidatePageInput(nil, values)) != 1 {
		t.Fatal("unknown value accepted")
	}
	for _, f := range fields {
		if !validPageRules(f) {
			t.Fatalf("valid rules rejected: %+v", f)
		}
		if ValidatePageVersion(PageDocument{Fields: []PageField{f}}, PageCapability.Version) == nil && f.Type != "boolean" {
			t.Fatal("rules leaked")
		}
	}
}

func TestPageInvalidRules(t *testing.T) {
	for _, f := range []PageField{
		{Type: "text", Minimum: ptr(1.0)}, {Type: "number", MinLength: ptr(1)}, {Type: "text", MinItems: ptr(1)},
		{Type: "number", Minimum: ptr(math.NaN())}, {Type: "number", Minimum: ptr(3.0), Maximum: ptr(2.0)},
		{Type: "text", MinLength: ptr(-1)}, {Type: "list", MaxItems: ptr(-1)}, {Type: "text", MinLength: ptr(3), MaxLength: ptr(2)},
		{Type: "text", Options: []PageOption{{Value: "a", Label: "A"}}},
		{Type: "select", OptionsFrom: "rows", Options: []PageOption{{Value: "a", Label: "A"}}},
		{Type: "select", Options: []PageOption{{Value: "a", Label: "A"}, {Value: "a", Label: "Again"}}},
		{Type: "select", Options: []PageOption{{Value: "", Label: "A"}}},
		{Type: "select", Options: make([]PageOption, 129)},
	} {
		if validPageRules(f) {
			t.Fatalf("invalid rules accepted: %+v", f)
		}
	}
}

func TestPageContentRequiresArrays(t *testing.T) {
	for _, arrays := range []string{"omitted", "null", "empty"} {
		t.Run(arrays, func(t *testing.T) {
			rows, properties := "", ""
			if arrays == "null" {
				rows, properties = `,"rows":null`, `,"properties":null`
			}
			if arrays == "empty" {
				rows, properties = `,"rows":[]`, `,"properties":[]`
			}
			raw := []byte(`{"revision":"r","values":{},"collection":{"title":"Jobs","columns":[{"key":"name","label":"Name","display":"text"}]` + rows + `},"detail":{"title":"Details"` + properties + `}}`)
			doc, err := DecodePageDocument(raw)
			if arrays != "empty" {
				if err == nil {
					t.Fatal("accepted missing renderer arrays")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if doc.Collection.Rows == nil || doc.Detail.Properties == nil {
				t.Fatal("accepted content still contains null renderer arrays")
			}
			encoded, err := json.Marshal(doc)
			if err != nil || !strings.Contains(string(encoded), `"rows":[]`) || !strings.Contains(string(encoded), `"properties":[]`) {
				t.Fatalf("arrays not serialized as empty: %s %v", encoded, err)
			}
		})
	}
}
