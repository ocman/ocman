package db

import "context"

// GetModelUsage aggregates retained usage, scoped by time and directory.
func (d *DB) GetModelUsage(ctx context.Context, since int64, dir string) ([]ModelUsage, error) {
	return d.historyModelUsage(ctx, since, dir)
}

// RecentModel is one model picker's recent-use signal.
type RecentModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	LastUsed int64  `json:"lastUsed"`
}

// GetRecentModels samples the latest sessions in retained history.
func (d *DB) GetRecentModels(ctx context.Context, sessionLimit, maxResults int) ([]RecentModel, error) {
	if sessionLimit <= 0 {
		sessionLimit = 50
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	return d.historyRecentModels(ctx, sessionLimit, maxResults)
}
