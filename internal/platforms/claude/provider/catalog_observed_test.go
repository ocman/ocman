package claudeprovider

import (
	"reflect"
	"testing"
)

func TestObservedCommandsSupplementProbe(t *testing.T) {
	c := newCommands(Options{})
	base := []initCommand{{Name: "/review", Description: "Review changes"}}
	c.observeCommands("/repo", []string{"/review", "context", "context", ""})
	want := []initCommand{base[0], {Name: "context"}}
	if got := c.withObserved("/repo", base); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %+v, want %+v", got, want)
	}
	c.observeCommands("/repo", nil) // older CLI: don't erase an existing observation
	if got := c.withObserved("/repo", nil); len(got) != 2 {
		t.Fatalf("nil observation erased commands: %+v", got)
	}
	if got := c.withObserved("/other", base); !reflect.DeepEqual(got, base) {
		t.Fatalf("commands crossed directories: %+v", got)
	}
	c.observeCommands("/repo", []string{})
	if got := c.withObserved("/repo", base); !reflect.DeepEqual(got, base) {
		t.Fatalf("empty observation did not clear commands: %+v", got)
	}
}
