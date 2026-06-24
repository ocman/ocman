package loops

import (
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/state"
)

// renderTemplate substitutes loop-context placeholders in an action
// template. Placeholders use {{name}} syntax; unknown ones are left
// untouched so a typo is visible rather than silently dropped.
//
// Supported placeholders:
//
//	{{iteration}}    current iteration number (1-based, this firing)
//	{{project}}      project name
//	{{directory}}    working directory
//	{{last_summary}} summary from the previous iteration
//	{{trigger}}      the trigger detail string (e.g. "PR #42: 3 new comments")
//	{{pr_number}}    PR number (pr_event loops)
//
// ponytail: strings.Replacer instead of text/template — placeholders are
// a fixed flat set, no logic/loops needed. Switch to text/template if
// conditionals/ranges become necessary.
func renderTemplate(tmpl string, l state.Loop, tc TriggerConfig, triggerDetail string) string {
	prNumber := ""
	if tc.PRNumber > 0 {
		prNumber = strconv.Itoa(tc.PRNumber)
	}
	r := strings.NewReplacer(
		"{{iteration}}", strconv.Itoa(l.Iteration+1),
		"{{project}}", l.ProjectName,
		"{{directory}}", l.Directory,
		"{{last_summary}}", l.LastSummary,
		"{{trigger}}", triggerDetail,
		"{{pr_number}}", prNumber,
	)
	out := r.Replace(tmpl)
	if strings.TrimSpace(out) == "" {
		// Empty template would send a blank prompt; fall back to the
		// trigger detail so the action is at least meaningful.
		return triggerDetail
	}
	return out
}
