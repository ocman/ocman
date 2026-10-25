package plugins

import (
	"encoding/json"
	"testing"
)

func TestPageModelField(t *testing.T) {
	for _, value := range []string{`""`, `"openai/gpt-5"`, `"claudecode:anthropic/sonnet"`} {
		doc, err := DecodePageDocument([]byte(`{"revision":"r1","fields":[{"key":"model","label":"Model","type":"model"}],"values":{"model":` + value + `},"tree":{}}`))
		if err != nil {
			t.Fatalf("model field rejected: %v", err)
		}
		for _, minor := range []int{0, 1, 2, 3} {
			if err := ValidatePageVersion(doc, Version{Major: 1, Minor: minor}); (err == nil) != (minor >= 3) {
				t.Fatalf("wrong model field negotiation at minor %d: %v", minor, err)
			}
		}
		values, _ := PageValuesObject(doc.Values)
		if issues := ValidatePageInput(doc.Fields, values); len(issues) != 0 {
			t.Fatalf("valid selection rejected: %+v", issues)
		}
	}
	for _, value := range []string{`null`, `42`, `{"platform":"claudecode","model":"sonnet"}`} {
		if _, err := DecodePageDocument([]byte(`{"fields":[{"key":"model","label":"Model","type":"model"}],"values":{"model":` + value + `},"tree":{}}`)); err == nil {
			t.Fatal("non-string model accepted")
		}
	}
	doc := PageDocument{Fields: []PageField{{Key: "rows", Label: "Rows", Type: "list", Fields: []PageField{{Key: "model", Label: "Model", Type: "model"}}}}, Values: json.RawMessage(`{}`)}
	if ValidatePageVersion(doc, Version{Major: 1, Minor: 2}) == nil {
		t.Fatal("nested model field accepted by an older host")
	}
}
