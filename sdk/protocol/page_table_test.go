package protocol

import (
	"encoding/json"
	"testing"
)

func TestPageTableDisplays(t *testing.T) {
	for _, tc := range []struct {
		display string
		value   string
		valid   bool
	}{
		{"summary", `{"label":"Race condition","description":"src/model.go:12","count":2}`, true},
		{"summary", `{"label":"Project"}`, true},
		{"summary", `{"label":""}`, false},
		{"summary", `{"label":true}`, false},
		{"summary", `{"label":"Project","description":false}`, false},
		{"summary", `{"label":"Project","count":-1}`, false},
		{"summary", `{"label":"Project","count":1.5}`, false},
		{"summary", `{"label":"Project","count":1000001}`, false},
		{"summary", `{"label":"Project","count":"2"}`, false},
		{"summary", `"not an object"`, false},
		{"severity", `"critical"`, true},
		{"severity", `"high"`, true},
		{"severity", `"medium"`, true},
		{"severity", `"low"`, true},
		{"severity", `"info"`, true},
		{"severity", `"unknown"`, true},
		{"severity", `"HIGH"`, false},
		{"severity", `"danger arbitrary-class"`, false},
		{"severity", `2`, false},
	} {
		t.Run(tc.display+tc.value, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.value), &value); err != nil {
				t.Fatal(err)
			}
			doc := PageDocument{Revision: "r1", Values: json.RawMessage(`{}`), Collection: &PageCollection{
				Title: "Findings", Columns: []PageColumn{{Key: "name", Label: "Name", Display: tc.display}},
				Rows: []PageRow{{ID: "finding", Values: map[string]any{"name": value}}},
			}}
			raw, _ := json.Marshal(doc)
			decoded, err := DecodePageDocument(raw)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			if !tc.valid {
				return
			}
			if ValidatePageVersion(decoded, PageModelCapability.Version) == nil {
				t.Fatal("new display accepted on 1.3")
			}
			if err := ValidatePageVersion(decoded, PageTableCapability.Version); err != nil {
				t.Fatal(err)
			}
			doc.Detail = &PageDetail{Title: "Finding", Properties: []PageProperty{{Label: "Value", Display: tc.display, Value: value}}}
			doc.Collection = nil
			if ValidatePageVersion(doc, PageModelCapability.Version) == nil {
				t.Fatal("new detail accepted on 1.3")
			}
			if err := ValidatePageVersion(doc, PageTableCapability.Version); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPageDrawerPresentation(t *testing.T) {
	for _, presentation := range []string{"", "drawer", "executable-script"} {
		doc := PageDocument{Revision: "r1", Values: json.RawMessage(`{}`), Collection: &PageCollection{
			Title: "Items", DetailPresentation: presentation, Columns: []PageColumn{{Key: "name", Label: "Name", Display: "text"}}, Rows: []PageRow{},
		}}
		raw, _ := json.Marshal(doc)
		decoded, err := DecodePageDocument(raw)
		if (err == nil) != (presentation != "executable-script") {
			t.Fatalf("presentation %q: %v", presentation, err)
		}
		if err != nil {
			continue
		}
		if (ValidatePageVersion(decoded, PageModelCapability.Version) == nil) != (presentation == "") {
			t.Fatal("drawer version gate missing")
		}
		if err := ValidatePageVersion(decoded, PageTableCapability.Version); err != nil {
			t.Fatal(err)
		}
	}
}
