package statev2

import (
	"encoding/json"
	"maps"
	"sync"
	"sync/atomic"
)

// SidebarLiveSnapshot is an immutable, process-local checkpoint. SQL still
// commits background completion edges before this snapshot becomes visible.
// A new process starts empty: persisted observations cannot establish live state.
type SidebarLiveSnapshot struct {
	Owner, Platform, Source, Generation string
	ExpiresAt                           int64
	Observations                        map[string]SidebarObservation
}

type sidebarLive struct {
	write sync.Mutex
	value atomic.Pointer[sidebarLiveView]
}

type sidebarLiveView struct {
	sources map[string]SidebarLiveSnapshot
	encoded string
	running string
}

func sidebarLiveKey(owner, platform, source string) string {
	return owner + "\x00" + platform + "\x00" + source
}

func (d *DB) publishSidebarLive(scope Instance, generation string, expiresAt int64, observations map[string]SidebarObservation, renew bool) {
	key := sidebarLiveKey(scope.OwnerID, scope.Platform, scope.SourceKey)
	next := map[string]SidebarLiveSnapshot{}
	if previous := d.sidebarLive.value.Load(); previous != nil {
		next = maps.Clone(previous.sources)
	}
	if renew {
		observations = next[key].Observations
	} else {
		observations = maps.Clone(observations)
	}
	next[key] = SidebarLiveSnapshot{scope.OwnerID, scope.Platform, scope.SourceKey, generation, expiresAt, observations}
	// Encode once per checkpoint, not once per client read. Positional tuples
	// avoid repeated object-key parsing by SQLite's JSON table-valued reader.
	encoded := encodeSidebarLive(next, false)
	running := encodeSidebarLive(next, true)
	d.sidebarLive.value.Store(&sidebarLiveView{sources: next, encoded: encoded, running: running})
}

// Empty means use the indexed checkpoint tables for a dense snapshot. Bound
// encoding at publication too, not just the work admitted by each SQL reader.
func encodeSidebarLive(sources map[string]SidebarLiveSnapshot, runningOnly bool) string {
	wire := make([][6]any, 0, len(sources))
	total := 0
	for _, s := range sources {
		if !runningOnly && total+len(s.Observations) > 1000 {
			return ""
		}
		rows := make([][7]any, 0)
		for id, o := range s.Observations {
			if runningOnly && o.Status != "busy" && !o.PendingPermission && !o.PendingQuestion {
				continue
			}
			total++
			if total > 1000 {
				return ""
			}
			rows = append(rows, [7]any{id, o.Status, o.ResetsAt, o.PendingPermission, o.PendingQuestion, o.BackgroundTasks, o.BackgroundServices})
		}
		wire = append(wire, [6]any{s.Owner, s.Platform, s.Source, s.Generation, s.ExpiresAt, rows})
	}
	encoded, _ := json.Marshal(wire) // Only strings, integers and booleans.
	return string(encoded)
}

// SidebarLiveJSON is an immutable encoded snapshot, shared by all local readers.
// An empty string selects the indexed SQL path for large observations.
func (d *DB) SidebarLiveJSON(runningOnly bool) string {
	if current := d.sidebarLive.value.Load(); current != nil {
		if runningOnly {
			return current.running
		}
		return current.encoded
	}
	return "[]"
}
