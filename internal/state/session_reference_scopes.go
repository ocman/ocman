package state

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ocman/ocman/internal/platforms/identity"
)

// QuarantineUnattributedSessionReferences cannot assign sessions to an inferred
// machine. Only explicit pending-reference tokens are accepted as replacements.
func (d *DB) QuarantineUnattributedSessionReferences(ctx context.Context, mapping map[string]string) error {
	for _, id := range mapping {
		if !identity.IsPendingReference(id) {
			return fmt.Errorf("unattributed session reference requires quarantine")
		}
	}
	return d.rekeySessionReferences(ctx, "", mapping)
}

// SessionReferenceScopes inventories exact legacy routing scopes before cutover.
// Empty scope is retained as unknown, not normalized to a default platform.
func (d *DB) SessionReferenceScopes(ctx context.Context) ([]string, error) {
	set := map[string]bool{}
	collect := func(query string, args ...any) error {
		rows, err := d.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var scope string
			if err := rows.Scan(&scope); err != nil {
				return err
			}
			set[scope] = true
		}
		return rows.Err()
	}
	for _, ref := range sessionReferenceColumns {
		if err := collect("SELECT DISTINCT " + ref.owner + " FROM " + ref.table + " WHERE " + ref.column + " IS NOT NULL AND " + ref.column + "<>''"); err != nil {
			return nil, err
		}
	}
	if err := collect(`SELECT DISTINCT l.platform FROM loop_iterations i JOIN loops l ON l.id=i.loop_id WHERE i.target_session_id<>'' OR i.child_session_id<>''`); err != nil {
		return nil, err
	}
	for _, ref := range sessionJSONReferences {
		if err := collect("SELECT DISTINCT COALESCE(json_extract(" + ref.column + ",'$.platform'),'') FROM " + ref.table + " WHERE " + ref.scope + " AND json_valid(" + ref.column + ") AND COALESCE(json_extract(" + ref.column + ",'$.sessionId'),'')<>''"); err != nil {
			return nil, err
		}
	}
	if err := collect(`SELECT DISTINCT r.platform FROM session_replacement r WHERE EXISTS(SELECT 1 FROM json_each(r.baseline_json)) OR EXISTS(SELECT 1 FROM json_each(r.stop_baseline_json))`); err != nil {
		return nil, err
	}
	if err := collect(`SELECT 'opencode' WHERE EXISTS(SELECT 1 FROM auto_approved_permission WHERE judge_session_id<>'')`); err != nil {
		return nil, err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT entity_key FROM unarchived_entity WHERE kind='session' AND remote_id='local'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var packed string
		if err := rows.Scan(&packed); err != nil {
			rows.Close()
			return nil, err
		}
		if scope, _, ok := strings.Cut(packed, "\x00"); ok {
			set[scope] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []string
	for scope := range set {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out, nil
}
