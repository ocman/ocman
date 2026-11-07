package sidebar

import (
	"fmt"
	"strings"
)

// liveObservationsCTE binds one immutable owner-local snapshot to the query.
// The joins retain source/generation/lease fencing and pre-pagination prompt
// bubbling without writing live state to SQLite and then reading it back.
const liveObservationsCTE = `snapshots AS MATERIALIZED (
 SELECT json_extract(value,'$[0]') AS owner_id,json_extract(value,'$[1]') AS platform,
 json_extract(value,'$[2]') AS source_key,json_extract(value,'$[3]') AS generation,
 json_extract(value,'$[4]') AS expires_at,json_extract(value,'$[5]') AS observations FROM json_each(?)
), connections AS (
 SELECT x.* FROM snapshots x JOIN runtime r ON r.owner=x.owner_id AND r.platform=x.platform AND r.source=x.source_key AND r.generation=x.generation WHERE x.expires_at>?
), observations AS MATERIALIZED (
 SELECT x.owner_id,x.platform,x.source_key,x.generation,json_extract(o.value,'$[0]') AS native_id,
 json_extract(o.value,'$[1]') AS status,json_extract(o.value,'$[2]') AS resets_at,
 json_extract(o.value,'$[3]') AS pending_permission,json_extract(o.value,'$[4]') AS pending_question,
 json_extract(o.value,'$[5]') AS background_tasks,json_extract(o.value,'$[6]') AS background_services
 FROM snapshots x,json_each(x.observations) o
), live AS (
 SELECT o.* FROM observations o JOIN connections x ON x.owner_id=o.owner_id AND x.platform=o.platform AND x.source_key=o.source_key AND x.generation=o.generation
)`

// OpenLive shares the history owner's process-local observation snapshots.
// The SQL-only Open remains available for offline readers and equivalence tests.
func OpenLive(history, core string, snapshots func(bool) string) (*Reader, error) {
	if snapshots == nil {
		return nil, fmt.Errorf("sidebar: live observations required")
	}
	r, err := Open(history, core)
	if err == nil {
		r.snapshots = snapshots
	}
	return r, err
}

func (r *Reader) liveQuery(query string, args []any) (string, []any) {
	if r.snapshots == nil {
		return query, args
	}
	raw := r.snapshots(query == countRunning)
	// Dense JSON joins cost more than the indexed checkpoint tables. Keep that
	// established path for large snapshots; never trade fewer SQL reads for
	// more CPU. Typical counts bind only busy rows and pending prompt ancestry.
	if raw == "" || len(raw) > 64<<10 {
		return query, args
	}
	start := strings.Index(query, "connections AS (")
	end := strings.Index(query, ", prompts(")
	query = query[:start] + liveObservationsCTE + query[end:]
	// JSON-derived rows do not carry column constraints. Expose the ready-row
	// invariant so SQLite uses the catalogue's partial native binding index.
	query = strings.ReplaceAll(query, "c.native_id=o.native_id", "c.native_id=o.native_id AND c.native_id<>''")
	query = strings.ReplaceAll(query, "main.sidebar_observation bg", "observations bg")
	args = append(args[:2:2], append([]any{raw}, args[2:]...)...)
	return query, args
}
