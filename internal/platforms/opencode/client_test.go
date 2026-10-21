package opencode

import (
	"testing"
)

// --- truncatePartOutput tests ---

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name, in string
		n        int
		want     string
	}{
		{"short", "abc", 5, "abc"},
		{"ascii", "abcdef", 3, "abc"},
		{"rune at limit", "ab€", 2, "ab"},
		{"straddling rune", "a€b", 2, "a"},
		{"invalid continuation run", "a\x80\x80\x80\x80\x80", 5, "a\x80\x80\x80\x80"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateUTF8(tt.in, tt.n); got != tt.want {
				t.Errorf("truncateUTF8(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}
}

// --- convertOpenCodeMessages tests ---

// --- isSynthesizedTerminal tests ---

// --- computeMessageStats tests ---

// --- paginateUntyped tests ---

// --- filterPartsUntyped tests ---

// --- copyMap tests ---

func TestCopyMap(t *testing.T) {
	original := map[string]string{"a": "1", "b": "2"}
	cp := copyMap(original)

	if len(cp) != 2 || cp["a"] != "1" || cp["b"] != "2" {
		t.Errorf("copy doesn't match original")
	}

	// Mutating the copy should not affect the original
	cp["c"] = "3"
	if _, ok := original["c"]; ok {
		t.Error("mutating copy affected original")
	}
}

func TestCopyMap_Nil(t *testing.T) {
	cp := copyMap(nil)
	if cp == nil {
		t.Error("expected non-nil empty map")
	}
	if len(cp) != 0 {
		t.Error("expected empty map")
	}
}
