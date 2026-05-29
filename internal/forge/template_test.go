package forge

import "testing"

func TestRenderPrompt_ReplacesKnownPlaceholders(t *testing.T) {
	tmpl := "Handle PR #{number}: {title}\n\nBy {author} on {host}/{repo}\nBranch: {branch}\nURL: {url}\n\n{body}"
	vars := map[string]string{
		"number": "42",
		"title":  "Tighten the slug rules",
		"author": "alice",
		"host":   "github.com",
		"repo":   "alice/myproj",
		"branch": "tighten-slug",
		"url":    "https://github.com/alice/myproj/pull/42",
		"body":   "Description here.",
	}
	got := RenderPrompt(tmpl, vars)
	want := "Handle PR #42: Tighten the slug rules\n\nBy alice on github.com/alice/myproj\nBranch: tighten-slug\nURL: https://github.com/alice/myproj/pull/42\n\nDescription here."
	if got != want {
		t.Errorf("RenderPrompt mismatch.\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderPrompt_UnknownPlaceholdersLeftLiteral(t *testing.T) {
	tmpl := "Hello {title} — {unknown} — {also_unknown}"
	got := RenderPrompt(tmpl, map[string]string{"title": "world"})
	want := "Hello world — {unknown} — {also_unknown}"
	if got != want {
		t.Errorf("RenderPrompt unknown placeholders.\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderPrompt_EmptyValuesAllowed(t *testing.T) {
	// branch is empty for issues; placeholder must still be replaced with ""
	tmpl := "Branch: '{branch}'"
	got := RenderPrompt(tmpl, map[string]string{"branch": ""})
	want := "Branch: ''"
	if got != want {
		t.Errorf("RenderPrompt empty value.\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderPrompt_NoPlaceholders(t *testing.T) {
	tmpl := "static text only"
	got := RenderPrompt(tmpl, map[string]string{"title": "x"})
	if got != tmpl {
		t.Errorf("RenderPrompt static.\n got: %q\nwant: %q", got, tmpl)
	}
}

func TestRenderPrompt_NilVarsTreatsAllAsUnknown(t *testing.T) {
	tmpl := "Hello {title}"
	got := RenderPrompt(tmpl, nil)
	if got != tmpl {
		t.Errorf("RenderPrompt nil vars should leave placeholders literal.\n got: %q\nwant: %q", got, tmpl)
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
