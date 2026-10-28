package protocol

import (
	"encoding/json"
	"testing"
)

func TestPageProjectPickerAndSelection(t *testing.T) {
	configured := true
	doc := PageDocument{Revision: "r", Configured: &configured, Fields: []PageField{
		{Key: "selected", Label: "Target to scan", Type: "select", OptionsFrom: "targets"},
		{Key: "targets", Label: "Targets", Type: "list", Fields: []PageField{
			{Key: "name", Label: "Name", Type: "text"},
			{Key: "projects", Label: "Code projects", Type: "projects"},
		}},
	}, Values: json.RawMessage(`{"selected":"staging","targets":[{"name":"staging","projects":"/srv/web\n/srv/api"}]}`)}
	for _, test := range []struct {
		name   string
		mutate func(*PageDocument)
		valid  bool
	}{
		{"pickers", func(*PageDocument) {}, true},
		{"missing-source", func(d *PageDocument) { d.Fields[0].OptionsFrom = "missing" }, false},
		{"wrong-source", func(d *PageDocument) { d.Fields[0].OptionsFrom = "selected" }, false},
		{"source-on-text", func(d *PageDocument) { d.Fields[0].Type = "text" }, false},
		{"bad-selection-value", func(d *PageDocument) { d.Values = json.RawMessage(`{"selected":[]}`) }, false},
		{"bad-project-value", func(d *PageDocument) { d.Values = json.RawMessage(`{"targets":[{"projects":["/srv/web"]}]}`) }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := doc
			copy.Fields = append([]PageField(nil), doc.Fields...)
			test.mutate(&copy)
			raw, _ := json.Marshal(copy)
			got, err := DecodePageDocument(raw)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
			if test.valid && (got.Configured == nil || !*got.Configured) {
				t.Fatal("configured state lost")
			}
		})
	}
}
