package forge

import "testing"

func TestRenderPrompt(t *testing.T) {
	tests := []struct {
		name, tmpl string
		vars       map[string]string
		want       string
	}{
		{
			name: "replaces known placeholders",
			tmpl: "Handle PR #{number}: {title}\n\nBy {author} on {host}/{repo}\nBranch: {branch}\nURL: {url}\n\n{body}",
			vars: map[string]string{
				"number": "42",
				"title":  "Tighten the slug rules",
				"author": "alice",
				"host":   "github.com",
				"repo":   "alice/myproj",
				"branch": "tighten-slug",
				"url":    "https://github.com/alice/myproj/pull/42",
				"body":   "Description here.",
			},
			want: "Handle PR #42: Tighten the slug rules\n\nBy alice on github.com/alice/myproj\nBranch: tighten-slug\nURL: https://github.com/alice/myproj/pull/42\n\nDescription here.",
		},
		{
			name: "unknown placeholders are left literal",
			tmpl: "Hello {title} — {unknown} — {also_unknown}",
			vars: map[string]string{"title": "world"},
			want: "Hello world — {unknown} — {also_unknown}",
		},
		{
			// branch is empty for issues; the placeholder is still replaced.
			name: "empty values are allowed",
			tmpl: "Branch: '{branch}'",
			vars: map[string]string{"branch": ""},
			want: "Branch: ''",
		},
		{
			name: "no placeholders",
			tmpl: "static text only",
			vars: map[string]string{"title": "x"},
			want: "static text only",
		},
		{
			name: "nil vars treat every placeholder as unknown",
			tmpl: "Hello {title}",
			want: "Hello {title}",
		},
		// Placeholder text inside a substituted value (an attacker-controlled
		// PR title or body) must stay literal: substitution is a single pass.
		{
			name: "title and body contain placeholders",
			tmpl: "Handle {title}\n\n{body}",
			vars: map[string]string{"title": "{body}", "body": "see {url}", "url": "https://example.com/pr/1"},
			want: "Handle {body}\n\nsee {url}",
		},
		{
			name: "value contains its own placeholder",
			tmpl: "{title}",
			vars: map[string]string{"title": "{title}{title}"},
			want: "{title}{title}",
		},
		{
			name: "value contains an unprovided placeholder",
			tmpl: "{body} {branch}",
			vars: map[string]string{"body": "{branch}"},
			want: "{branch} {branch}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderPrompt(tt.tmpl, tt.vars); got != tt.want {
				t.Errorf("RenderPrompt = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderPrompt_AllSupportedKeys(t *testing.T) {
	// Document the full set of supported placeholders. If this list
	// grows, add the new key here and to template.go's renderer.
	for _, key := range []string{"number", "title", "body", "url", "branch", "author", "host", "repo"} {
		tmpl := "x{" + key + "}y"
		got := RenderPrompt(tmpl, map[string]string{key: "Z"})
		want := "xZy"
		if got != want {
			t.Errorf("placeholder {%s} not substituted.\n got: %q\nwant: %q", key, got, want)
		}
	}
}
