package forge

import "strings"

// SupportedPlaceholders enumerates the keys that RenderPrompt
// recognises. Other tokens in the template (e.g. an accidental
// `{unknown}`) are left as literal text rather than treated as an
// error — see RenderPrompt for the full semantics.
//
// Kept in package scope (not a const) so the settings handler can
// surface the list to the UI without re-declaring it.
var SupportedPlaceholders = []string{
	"number",
	"title",
	"body",
	"url",
	"branch",
	"author",
	"host",
	"repo",
}

// RenderPrompt expands `{key}` placeholders in tmpl using vars.
//
// Rules:
//   - Each entry in vars maps a placeholder key (without braces) to
//     its replacement value. Empty-string values are valid and
//     produce an empty replacement (used e.g. for `{branch}` on
//     issues, which have no source branch).
//   - Placeholders for keys NOT present in vars are left as literal
//     text (e.g. `{unknown}` stays `{unknown}`). This matches FR-10:
//     unknown placeholders are not template errors.
//   - Only keys in SupportedPlaceholders are recognised; passing an
//     unsupported key in vars has no effect.
//
// The implementation is a fixed loop over the known keys rather than
// a regex sweep so the cost is O(len(template) * |placeholders|),
// not O(len(template)^2). For prompt-sized strings the difference
// is academic; this just keeps the rule "supported keys only" easy
// to reason about.
func RenderPrompt(tmpl string, vars map[string]string) string {
	out := tmpl
	for _, key := range SupportedPlaceholders {
		val, ok := vars[key]
		if !ok {
			// Not provided: leave the literal `{key}` in place.
			continue
		}
		out = strings.ReplaceAll(out, "{"+key+"}", val)
	}
	return out
}
