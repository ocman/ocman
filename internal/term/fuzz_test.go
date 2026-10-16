package term

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/tmux"
)

// FuzzDerivedTmuxNames checks the tmux names ocman derives from a
// directory. A terminal window name always satisfies tmux.ValidComponent,
// is recognised for that directory (cleaned or not) and keeps its index. A
// project session name never contains '.', which tmux would parse as a
// window separator, is stable under filepath.Clean, and satisfies
// tmux.ValidComponent whenever the directory's own characters do.
func FuzzDerivedTmuxNames(f *testing.F) {
	for _, dir := range []string{"/home/u/src/github.com/foo", "/a:b/c", "/", ".", "", "/a/../../b", "rel/x", "~/x", "/tmp/my_app.v2/x", "/x/with space"} {
		f.Add(dir, 1)
	}
	f.Add("/a/../b", 99999999999)
	f.Fuzz(func(t *testing.T, dir string, idx int) {
		session := tmux.SessionNameForPath(dir)
		if session == "" || strings.Contains(session, ".") {
			t.Fatalf("SessionNameForPath(%q) = %q", dir, session)
		}
		// Known finding: the "/" fallback is checked before cleaning, so
		// "/." is named "/" while "/" is named "opencode".
		if filepath.IsAbs(dir) && filepath.Clean(dir) != "/" {
			if again := tmux.SessionNameForPath(filepath.Clean(dir)); again != session {
				t.Fatalf("SessionNameForPath(%q) = %q but %q once cleaned", dir, session, again)
			}
		}
		if tmux.ValidComponent.MatchString(dir) && !tmux.ValidComponent.MatchString(session) {
			t.Fatalf("SessionNameForPath(%q) = %q is not a valid component", dir, session)
		}

		if idx < 1 {
			return
		}
		window := WindowPrefix(dir) + strconv.Itoa(idx)
		if !tmux.ValidComponent.MatchString(window) {
			t.Fatalf("window name %q for %q is not a valid component", window, dir)
		}
		if !IsWindowForDir(window, dir) || !IsWindowForDir(window, filepath.Clean(dir)) {
			t.Fatalf("window %q not recognised for %q", window, dir)
		}
		if got := termWindowIndex(window); got != idx {
			t.Fatalf("termWindowIndex(%q) = %d, want %d", window, got, idx)
		}
	})
}
