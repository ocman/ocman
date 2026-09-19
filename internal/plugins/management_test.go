package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSettingAccepts(t *testing.T) {
	for _, tt := range []struct {
		kind, value string
		valid       bool
	}{
		{"string", `"value"`, true}, {"boolean", `false`, true}, {"number", `2.5`, true}, {"integer", `2`, true},
		{"integer", `2.5`, false}, {"object", `{}`, false}, {"string", `null`, false}, {"number", `"2"`, false},
		{"string", `broken`, false},
	} {
		if got := (Setting{Type: tt.kind}).Accepts(json.RawMessage(tt.value)); got != tt.valid {
			t.Fatalf("%s %s: %v", tt.kind, tt.value, got)
		}
	}
}

func TestProcessRedactedStderr(t *testing.T) {
	p := &Process{}
	token := p.launchToken()
	redact := func(text string) string { return strings.ReplaceAll(text, "password", "[REDACTED]") }
	for _, text := range []string{"password " + token + "\n", "partial " + token[:20]} {
		p.stderr = []byte(text)
		got := p.RedactedStderr(redact)
		if strings.Contains(got, "password") || strings.Contains(got, token[:20]) || !strings.Contains(got, "[REDACTED]") {
			t.Fatal(got)
		}
	}
	p.stderr = []byte(strings.Repeat("x", MaxStderrBytes))
	if got := p.RedactedStderr(redact); got != "[stderr limit reached]" {
		t.Fatal(got)
	}
	p.stderr = []byte("short")
	if got := p.RedactedStderr(func(string) string { return strings.Repeat("x", MaxStderrBytes+1) }); len(got) != MaxStderrBytes {
		t.Fatal(len(got))
	}
}

func TestProcessConfigurationValidation(t *testing.T) {
	config := processFixture(t, "success")
	for _, value := range []string{"invalid", strings.Repeat(" ", MaxMessageBytes+1)} {
		config.Configuration = json.RawMessage(value)
		if _, err := StartProcess(context.Background(), config); !errors.Is(err, ErrInvalidMessage) {
			t.Fatal(err)
		}
	}
}
