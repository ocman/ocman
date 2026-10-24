package forge

import "testing"

func TestListOptionsWithDefaults(t *testing.T) {
	got := ListOptions{}.WithDefaults()
	if got != (ListOptions{State: "open", Page: 1, PerPage: DefaultPerPage}) {
		t.Fatalf("defaults = %+v", got)
	}
	in := ListOptions{State: "closed", Mine: "me", Page: 3, PerPage: 5}
	if got := in.WithDefaults(); got != in {
		t.Fatalf("explicit options changed: %+v", got)
	}
	if got := (ListOptions{Page: -1, PerPage: -1}).WithDefaults(); got.Page != 1 || got.PerPage != DefaultPerPage {
		t.Fatalf("negative options = %+v", got)
	}
}
