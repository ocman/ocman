package protocol

import (
	"strings"
	"testing"
)

func TestPermissionProfileNameContract(t *testing.T) {
	for _, name := range []string{"read-only", "two words", strings.Repeat("x", 128), strings.Repeat("é", 64)} {
		if !ValidPermissionProfileName(name) {
			t.Fatalf("rejected valid name %q", name)
		}
	}
	for _, name := range []string{"", " leading", "trailing ", "embedded\nnewline", "control\u0085", "bad\xffutf8", strings.Repeat("é", 65)} {
		if ValidPermissionProfileName(name) {
			t.Fatalf("accepted invalid name %q", name)
		}
		if err := (SessionRunParams{Prompt: "go", PermissionProfile: name}).Validate(); name != "" && err == nil {
			t.Fatalf("run accepted invalid profile %q", name)
		}
	}
}
