package db

import (
	"context"
	"encoding/json"
	"errors"
)

// SubscriptionOAuth contains only the fields needed for server-side usage
// requests. Never serialize it into browser responses or log its values.
type SubscriptionOAuth struct {
	Type      string `json:"type"`
	Access    string `json:"access"`
	Expires   int64  `json:"expires"`
	AccountID string `json:"accountId"`
}

// SubscriptionCredentials reads the same account OpenCode v2 selects: active
// first, then newest creation time and ID for legacy rows without a selection.
// A selected API key must not fall back to an unselected OAuth account.
func (d *DB) SubscriptionCredentials(ctx context.Context) (map[string]SubscriptionOAuth, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT integration_id, value FROM credential
		WHERE integration_id IN ('openai', 'anthropic')
		ORDER BY active DESC, time_created DESC, id DESC`)
	if err != nil {
		return nil, errors.New("cannot read OpenCode subscription credentials")
	}
	defer rows.Close()
	result := make(map[string]SubscriptionOAuth)
	for rows.Next() {
		var id, value string
		if err := rows.Scan(&id, &value); err != nil {
			return nil, errors.New("cannot read OpenCode subscription credential")
		}
		if _, seen := result[id]; seen {
			continue
		}
		var auth struct {
			SubscriptionOAuth
			Metadata struct {
				AccountID string `json:"accountID"`
			} `json:"metadata"`
		}
		if json.Unmarshal([]byte(value), &auth) != nil {
			return nil, errors.New("cannot decode OpenCode subscription credential")
		}
		if auth.Metadata.AccountID != "" {
			auth.AccountID = auth.Metadata.AccountID
		}
		result[id] = auth.SubscriptionOAuth
	}
	if rows.Err() != nil {
		return nil, errors.New("cannot read OpenCode subscription credentials")
	}
	return result, nil
}
