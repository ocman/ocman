package statev2

import (
	"context"
	"fmt"
)

// ReadConversation reads the current displayable snapshot from the newest end.
// It excludes superseded and opaque native-only records. Source-deleted
// sessions remain readable.
func (d *DB) ReadConversation(ctx context.Context, key Key, limit, offset int) (*Detail, error) {
	if limit < 0 || offset < 0 {
		return nil, fmt.Errorf("statev2: conversation page requires nonnegative limit and offset")
	}
	tx, err := d.reader().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	detail, err := readDetail(ctx, tx, key, limit, offset)
	if err != nil {
		return nil, err
	}
	if err := conversationMetadata(ctx, tx, key, detail); err != nil {
		return nil, err
	}
	if err := conversationTotals(ctx, tx, key, detail); err != nil {
		return nil, err
	}
	return detail, nil
}
