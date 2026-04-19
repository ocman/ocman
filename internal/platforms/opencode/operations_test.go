package opencode

import "testing"

func TestParseOpenCodeModelRef(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantNil      bool
		wantProvider string
		wantModel    string
	}{
		{"empty", "", true, "", ""},
		{"whitespace only", "   ", true, "", ""},
		{"model only", "gpt-4", false, "", "gpt-4"},
		{"provider/model", "openai/gpt-4", false, "openai", "gpt-4"},
		{"with spaces", "  openai / gpt-4  ", false, "openai", "gpt-4"},
		{"empty provider", "/gpt-4", false, "", "/gpt-4"},
		{"empty model", "openai/", false, "", "openai/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseOpenCodeModelRefInternal(tt.input)
			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if result.ProviderID != tt.wantProvider {
				t.Errorf("ProviderID = %q, want %q", result.ProviderID, tt.wantProvider)
			}
			if result.ModelID != tt.wantModel {
				t.Errorf("ModelID = %q, want %q", result.ModelID, tt.wantModel)
			}
		})
	}
}
