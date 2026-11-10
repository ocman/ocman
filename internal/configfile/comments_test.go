package configfile

import (
	"strings"
	"testing"
)

func TestSequenceCommentsFollowSurvivingEntries(t *testing.T) {
	doc, err := Parse([]byte("version: 1\nsettings:\n  entries:\n    # first note\n    - id: first\n      value: old # first value\n    # second note\n    - id: second\n      value: kept # second value\n"))
	if err != nil {
		t.Fatal(err)
	}
	next := []map[string]string{{"id": "second", "value": "kept"}, {"id": "first", "value": "changed"}, {"id": "new", "value": "added"}}
	if err := doc.Set("settings", "entries", next); err != nil {
		t.Fatal(err)
	}
	data, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"# first note", "# first value", "# second note", "# second value", "changed", "added"} {
		if !strings.Contains(string(data), text) {
			t.Errorf("lost %q in %s", text, data)
		}
	}
	if strings.Index(string(data), "# second note") > strings.Index(string(data), "# first note") {
		t.Fatal("comments did not follow the reordered entries")
	}
}

func TestSequenceEditsDoNotGuessAmbiguousIdentities(t *testing.T) {
	for _, original := range []string{
		"    - value: old # unknown entry\n",
		"    - title: duplicate # one\n      value: first\n    - title: duplicate # two\n      value: second\n",
		"    - old # scalar note\n",
	} {
		doc, err := Parse([]byte("version: 1\nsettings:\n  entries:\n" + original))
		if err != nil {
			t.Fatal(err)
		}
		next := []map[string]string{{"title": "duplicate", "value": "changed"}}
		if err := doc.Set("settings", "entries", next); err != nil {
			t.Fatal(err)
		}
		data, err := doc.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "#") {
			t.Fatal("attached an unmatched or ambiguous entry's comment")
		}
	}
}
