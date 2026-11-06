package sidebar

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

// A running row requires a current connection lease, a matching physical source
// and generation, and no pending prompts, including prompts bubbled from children.
// Stored statuses cannot establish busy. Keep the visibility predicates aligned
// with selectSessions; paging, seen state, attention and accounting are irrelevant.
const countRunning = observationsCTE + `
 SELECT count(*) FROM live o
 JOIN core.session_catalog c ON c.owner_id=o.owner_id AND c.platform=o.platform AND c.source_key=o.source_key AND c.native_id=o.native_id
 JOIN main.instance i ON i.owner_id=c.owner_id AND i.platform=c.platform AND i.source_key=c.source_key
 JOIN main.session_sidebar s ON s.instance_id=i.id AND s.id=c.id
 LEFT JOIN flags f ON f.id=s.id
 LEFT JOIN core.pinned_session pin ON pin.platform=i.platform AND pin.session_id=s.id
 LEFT JOIN core.archived_session a ON a.platform=i.platform AND a.session_id=s.id
 WHERE i.owner_id=? AND c.creation_state='ready' AND c.purged=0
 AND o.status='busy' AND COALESCE(f.permission,0)=0 AND COALESCE(f.question,0)=0
 AND (s.source_deleted=0 OR pin.session_id IS NOT NULL OR COALESCE(a.snoozed_until,0)>0 OR s.id IN requested)
 AND (s.is_subagent=0 OR s.source_child=1 OR pin.session_id IS NOT NULL OR COALESCE(a.snoozed_until,0)>0 OR s.id IN requested)
 AND (?='' OR s.directory=?) AND (?='' OR c.origin=?) AND (?='' OR c.origin_id=?)`

func (r *Reader) RunningCount(ctx context.Context, scopes []Scope, q platforms.SidebarQuery) (int, error) {
	if len(scopes) == 0 {
		return 0, nil
	}
	owner := scopes[0].Owner
	for _, scope := range scopes {
		if owner == "" || scope.Owner != owner || scope.Platform == "" {
			return 0, fmt.Errorf("sidebar: invalid count scope")
		}
	}
	raw, err := json.Marshal(scopes)
	if err != nil {
		return 0, err
	}
	included, err := json.Marshal(q.IncludeIDs)
	if err != nil {
		return 0, err
	}
	var count int
	err = r.db.QueryRowContext(ctx, countRunning, string(raw), string(included), time.Now().UnixMilli(), owner,
		q.Directory, q.Directory, q.Origin, q.Origin, q.OriginID, q.OriginID).Scan(&count)
	return count, err
}
