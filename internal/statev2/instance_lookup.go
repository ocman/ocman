package statev2

import "context"

// FindInstance resolves one immutable physical source, never a platform-wide or
// cross-owner guess. A changed configuration cannot reuse another source's rows.
func (d *DB) FindInstance(ctx context.Context, owner, platform, source string) (Instance, error) {
	var i Instance
	err := d.reader().QueryRowContext(ctx, `SELECT id,owner_id,platform,source_key,name FROM instance WHERE owner_id=? AND platform=? AND source_key=?`, owner, platform, source).
		Scan(&i.ID, &i.OwnerID, &i.Platform, &i.SourceKey, &i.Name)
	return i, notFound(err)
}
