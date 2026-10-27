package codexappserver

import "context"

const MethodThreadLoadedList = "thread/loaded/list"

// ListLoadedThreads lists only threads loaded in this app-server, not the
// shared on-disk history. It is suitable for proving local writer ownership.
func (c *Client) ListLoadedThreads(ctx context.Context) ([]string, error) {
	return collect[string](ctx, c, MethodThreadLoadedList, func(cursor string) any {
		return struct {
			Cursor string `json:"cursor,omitempty"`
		}{Cursor: cursor}
	})
}
