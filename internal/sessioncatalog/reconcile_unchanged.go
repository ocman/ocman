package sessioncatalog

import (
	"context"
	"database/sql"
)

// A stable inventory needs no per-session SQL or relationship writes. Check
// its metadata and both parent graphs from one transaction-local snapshot.
// Anything requiring repair falls back to the existing mutation path.
func unchangedInventory(ctx context.Context, tx *sql.Tx, source Source, input []NativeSession) (bool, error) {
	// Large partial inventories must not load an entire source just to update
	// a few sessions. Bound this speculative read by the input size.
	limit := 2 * len(input)
	rows, err := tx.QueryContext(ctx, `SELECT `+columns+` FROM session_catalog
		WHERE owner_id=? AND platform=? AND source_key=? AND native_id<>'' LIMIT ?`,
		source.OwnerID, source.Platform, source.SourceKey, limit+1)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	byNative := make(map[string]Session, len(input))
	canonicalParents := make(map[string]string, len(input))
	nativeParents := make(map[string]string, len(input))
	for rows.Next() {
		row, err := scan(rows)
		if err != nil {
			return false, err
		}
		byNative[row.NativeID] = row
		canonicalParents[row.ID] = row.ParentID
		nativeParents[row.NativeID] = row.NativeParentID
		if len(byNative) > limit {
			return false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	wanted := make(map[string]bool, len(input))
	canonicalRoots := make([]string, 0, len(input))
	nativeRoots := make([]string, 0, len(input))
	for _, metadata := range input {
		row, ok := byNative[metadata.ID]
		if !ok || row.CreationState != Ready || row.Purged || row.Title != metadata.Title || row.Directory != metadata.Directory || row.NativeParentID != metadata.ParentID {
			return false, nil
		}
		if row.Origin == External && metadata.Origin != "" && metadata.Origin != External {
			return false, nil
		}
		if row.NativeParentID != "" {
			parent, ok := byNative[row.NativeParentID]
			if !ok || row.ParentID != parent.ID {
				return false, nil
			}
		}
		wanted[row.NativeID] = true
		canonicalRoots = append(canonicalRoots, row.ID)
		nativeRoots = append(nativeRoots, row.NativeID)
	}
	// Reconcile also repairs previously imported children of the supplied
	// parents, including children not present in this particular inventory.
	for _, row := range byNative {
		if row.CreationState == Ready && wanted[row.NativeParentID] && row.ParentID != byNative[row.NativeParentID].ID {
			return false, nil
		}
	}
	return validInventoryParents(ctx, canonicalParents, canonicalRoots) && validInventoryParents(ctx, nativeParents, nativeRoots), ctx.Err()
}

// Memoize validated ancestors so a deep/shared parent is walked only once.
// Missing ancestors or cycles use the normal path's existing error/repair rules.
func validInventoryParents(ctx context.Context, parents map[string]string, roots []string) bool {
	done := make(map[string]bool, len(parents))
	for _, root := range roots {
		path := make(map[string]bool)
		for id := root; id != "" && !done[id]; {
			if ctx.Err() != nil || path[id] {
				return false
			}
			parent, ok := parents[id]
			if !ok {
				return false
			}
			path[id] = true
			id = parent
		}
		for id := range path {
			done[id] = true
		}
	}
	return true
}
