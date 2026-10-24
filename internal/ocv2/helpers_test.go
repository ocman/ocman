package ocv2

import "testing"

func TestFirstNonEmptyAndTimeOf(t *testing.T) {
	if got := firstNonEmpty("", "  ", "x", "y"); got != "x" {
		t.Errorf("firstNonEmpty = %q, want x", got)
	}
	if got := firstNonEmpty("", " "); got != "" {
		t.Errorf("firstNonEmpty of blanks = %q, want empty", got)
	}
	item := map[string]any{}
	timeOf(item)["created"] = 1
	if created := obj(item, "time")["created"]; created != 1 {
		t.Fatalf("timeOf did not attach a time map: %v", item)
	}
	if timeOf(item)["created"] != 1 {
		t.Fatal("timeOf replaced an existing time map")
	}
}
