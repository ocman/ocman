package autoapprove

import (
	"sort"
	"sync"
	"testing"
)

// dirtyRecorder captures what the watcher marked dirty, replacing the
// package-level cache seams so the assertions don't depend on the
// opencode package's global snapshot state.
type dirtyRecorder struct {
	mu   sync.Mutex
	ids  []string
	full int
}

func (r *dirtyRecorder) markSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, sessionID)
}

func (r *dirtyRecorder) markAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.full++
}

func (r *dirtyRecorder) sortedIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string(nil), r.ids...)
	sort.Strings(out)
	return out
}

func (r *dirtyRecorder) fullMarks() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.full
}

func newRecordingWatcher(t *testing.T) (*autoApproveWatcher, *dirtyRecorder) {
	t.Helper()
	rec := &dirtyRecorder{}
	w := newAutoApproveWatcher(nil)
	w.markSessionDirty = rec.markSession
	w.markSessionsDirty = rec.markAll
	return w, rec
}

// TestHandleSessionChangedMarksEveryUpdateDirty pins that dirty marking
// is not gated on the first-sighting dedup: the first session.updated
// says "a session appeared", every later one says "this session's row
// changed", and the snapshot needs both.
func TestHandleSessionChangedMarksEveryUpdateDirty(t *testing.T) {
	w, rec := newRecordingWatcher(t)

	w.handleSessionChanged("")
	w.handleSessionChanged("ses-1")
	w.handleSessionChanged("ses-1")
	w.handleSessionChanged("ses-2")

	want := []string{"ses-1", "ses-1", "ses-2"}
	if got := rec.sortedIDs(); len(got) != len(want) {
		t.Fatalf("dirty marks = %v, want %v (every update, empty id ignored)", got, want)
	}
	if rec.fullMarks() != 0 {
		t.Errorf("full snapshot marked dirty %d times, want 0", rec.fullMarks())
	}
}

// TestHandleSessionChangedBroadcastUnchanged pins that adding dirty
// marking did not change what the watcher broadcasts: still exactly one
// broadcast per first-seen session.
func TestHandleSessionChangedBroadcastUnchanged(t *testing.T) {
	var mu sync.Mutex
	var broadcasts []string
	svc := &Service{}
	svc.deps.BroadcastSessionChanged = func(sessionID string) {
		mu.Lock()
		defer mu.Unlock()
		broadcasts = append(broadcasts, sessionID)
	}

	rec := &dirtyRecorder{}
	w := newAutoApproveWatcher(svc)
	w.markSessionDirty = rec.markSession
	w.markSessionsDirty = rec.markAll

	w.handleSessionChanged("ses-1")
	w.handleSessionChanged("ses-1")
	w.handleSessionChanged("ses-2")

	mu.Lock()
	defer mu.Unlock()
	if len(broadcasts) != 2 || broadcasts[0] != "ses-1" || broadcasts[1] != "ses-2" {
		t.Fatalf("broadcasts = %v, want one per first-seen session", broadcasts)
	}
	if got := len(rec.sortedIDs()); got != 3 {
		t.Errorf("dirty marks = %d, want 3 (marking is independent of the broadcast dedup)", got)
	}
}

// TestHandleSessionDataChangedMarksDirty covers the message/part and
// deletion events: an identified session marks just that session, and an
// unattributable one marks the whole snapshot rather than approximating.
func TestHandleSessionDataChangedMarksDirty(t *testing.T) {
	w, rec := newRecordingWatcher(t)

	w.handleSessionDataChanged("ses-1")
	w.handleSessionDataChanged("")

	if got := rec.sortedIDs(); len(got) != 1 || got[0] != "ses-1" {
		t.Errorf("dirty ids = %v, want [ses-1]", got)
	}
	if rec.fullMarks() != 1 {
		t.Errorf("full snapshot marks = %d, want 1 for the unattributable event", rec.fullMarks())
	}
}

// TestHandleSessionTurnMarksDirty pins that turn-lifecycle events mark
// the session dirty too: a finished turn changes the stored messages the
// list aggregates over (tokens, cost, finish, error).
func TestHandleSessionTurnMarksDirty(t *testing.T) {
	w, rec := newRecordingWatcher(t)

	w.markSessionDirtyIfKnown("ses-1")
	w.markSessionDirtyIfKnown("")

	if got := rec.sortedIDs(); len(got) != 1 || got[0] != "ses-1" {
		t.Errorf("dirty ids = %v, want [ses-1] (empty id ignored)", got)
	}
	if rec.fullMarks() != 0 {
		t.Errorf("full snapshot marks = %d, want 0", rec.fullMarks())
	}
}
