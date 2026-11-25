package usagewire

import "testing"

func TestDailyValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Daily)
		want bool
	}{
		{"valid", func(*Daily) {}, true},
		{"zero sessions", func(d *Daily) { d.Sessions = 0 }, false},
		{"too many sessions", func(d *Daily) { d.Sessions = 1_000_001 }, false},
		{"missing models", func(d *Daily) { d.Models = nil }, false},
		{"too many models", func(d *Daily) { d.Models = make([]ModelUsage, len(publicModels)+2) }, false},
		{"unknown", func(d *Daily) { d.Models[0].Model = "custom-name" }, false},
		{"duplicate", func(d *Daily) { d.Models = append(d.Models, d.Models[0]) }, false},
		{"uncovered session", func(d *Daily) { d.Sessions++ }, false},
		{"too many model sessions", func(d *Daily) { d.Models[0].Sessions++ }, false},
		{"negative input", func(d *Daily) { d.Models[0].InputTokens = -1 }, false},
		{"oversized output", func(d *Daily) { d.Models[0].OutputTokens = 1_000_000_000_001 }, false},
		{"other", func(d *Daily) { d.Models[0].Model = OtherModel }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Daily{Sessions: 1, Models: []ModelUsage{{Model: "gpt-5.4", Sessions: 1, InputTokens: 10, OutputTokens: 20}}}
			tc.edit(&d)
			if got := d.Valid(); got != tc.want {
				t.Fatalf("Daily.Valid() = %t, want %t", got, tc.want)
			}
		})
	}
}
