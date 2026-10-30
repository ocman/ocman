package agentui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func example(t *testing.T) map[string]any {
	t.Helper()
	raw, err := json.Marshal(help()["example"])
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "action")
	return doc
}

func validateDoc(t *testing.T, doc map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Validate(raw)
	return err
}

func TestValidateExample(t *testing.T) {
	if err := validateDoc(t, example(t)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsInvalidResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"missing title", func(d map[string]any) { delete(d, "title") }},
		{"empty summary", func(d map[string]any) { d["summary"] = "" }},
		{"too large", func(d map[string]any) { d["summary"] = strings.Repeat("x", MaxBytes) }},
		{"wrong version", func(d map[string]any) { messages(d)[0]["version"] = "v0.8" }},
		{"wrong catalog", func(d map[string]any) { messages(d)[0]["createSurface"].(map[string]any)["catalogId"] = "other" }},
		{"wrong surface", func(d map[string]any) { messages(d)[1]["updateComponents"].(map[string]any)["surfaceId"] = "other" }},
		{"update before create", func(d map[string]any) { m := messages(d); d["messages"] = []any{m[1], m[0]} }},
		{"duplicate surface", func(d map[string]any) { d["messages"] = append(d["messages"].([]any), messages(d)[0]) }},
		{"missing root", func(d map[string]any) { components(d)[0].(map[string]any)["id"] = "notRoot" }},
		{"missing child", func(d map[string]any) { components(d)[0].(map[string]any)["children"] = []string{"absent"} }},
		{"cycle", func(d map[string]any) { components(d)[0].(map[string]any)["children"] = []string{"root"} }},
		{"unknown component", func(d map[string]any) { components(d)[0].(map[string]any)["component"] = "HTML" }},
		{"arbitrary property", func(d map[string]any) { components(d)[1].(map[string]any)["html"] = "<script/>" }},
		{"nested binding", func(d map[string]any) {
			components(d)[3].(map[string]any)["value"] = map[string]any{"path": "/user/name"}
		}},
		{"unsafe binding", func(d map[string]any) {
			components(d)[3].(map[string]any)["value"] = map[string]any{"path": "/__proto__"}
		}},
		{"nested data", func(d map[string]any) {
			messages(d)[2]["updateDataModel"].(map[string]any)["value"] = map[string]any{"user": map[string]any{"name": "x"}}
		}},
		{"button child control", func(d map[string]any) { components(d)[4].(map[string]any)["child"] = "followup" }},
		{"function action", func(d map[string]any) {
			components(d)[4].(map[string]any)["action"] = map[string]any{"functionCall": map[string]any{"call": "openUrl"}}
		}},
		{"multiple operations", func(d map[string]any) { messages(d)[0]["updateComponents"] = messages(d)[1]["updateComponents"] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := example(t)
			tc.edit(d)
			if err := validateDoc(t, d); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
	if _, err := Validate([]byte(`{`)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestGraphLimits(t *testing.T) {
	for _, count := range []int{18, 101} {
		d := example(t)
		items := []any{}
		for i := range count {
			id := "node" + strings.Repeat("x", i%50)
			if i == 0 {
				id = "root"
			}
			items = append(items, map[string]any{"id": id, "component": "Column", "children": []string{"node" + strings.Repeat("x", (i+1)%50)}})
		}
		messages(d)[1]["updateComponents"].(map[string]any)["components"] = items
		if err := validateDoc(t, d); err == nil {
			t.Fatal("unbounded graph accepted")
		}
	}
}

func TestAggregateComponentLimit(t *testing.T) {
	d := example(t)
	items := []any{map[string]any{"id": "root", "component": "Text", "text": "Root"}}
	for i := range 99 {
		items = append(items, map[string]any{"id": fmt.Sprintf("node%d", i), "component": "Text", "text": "Value"})
	}
	messages(d)[1]["updateComponents"].(map[string]any)["components"] = items
	if err := validateDoc(t, d); err != nil {
		t.Fatal(err)
	}
	d["messages"] = append(d["messages"].([]any), map[string]any{
		"version": "v0.9.1", "updateComponents": map[string]any{"surfaceId": "investigation", "components": []any{
			map[string]any{"id": "overflow", "component": "Text", "text": "Value"},
		}},
	})
	if err := validateDoc(t, d); err == nil || !strings.Contains(err.Error(), "100 components") {
		t.Fatalf("expected aggregate component limit, got %v", err)
	}
}

func TestSharedSubtreeRenderLimit(t *testing.T) {
	d := example(t)
	items := []any{map[string]any{"id": "root", "component": "Column", "children": []string{"left0", "right0"}}}
	for i := range 10 {
		for _, side := range []string{"left", "right"} {
			c := map[string]any{"id": fmt.Sprintf("%s%d", side, i), "component": "Text", "text": "Leaf"}
			if i < 9 {
				c = map[string]any{"id": fmt.Sprintf("%s%d", side, i), "component": "Column", "children": []string{fmt.Sprintf("left%d", i+1), fmt.Sprintf("right%d", i+1)}}
			}
			items = append(items, c)
		}
	}
	messages(d)[1]["updateComponents"].(map[string]any)["components"] = items
	if err := validateDoc(t, d); err == nil || !strings.Contains(err.Error(), "render limits") {
		t.Fatalf("expected render node limit, got %v", err)
	}
}

func messages(d map[string]any) []map[string]any {
	var out []map[string]any
	for _, v := range d["messages"].([]any) {
		out = append(out, v.(map[string]any))
	}
	return out
}

func components(d map[string]any) []any {
	return messages(d)[1]["updateComponents"].(map[string]any)["components"].([]any)
}
