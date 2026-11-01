package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

const subscriptionSampleInterval = 5 * time.Minute
const subscriptionHistoryFineRetention = 30 * 24 * time.Hour

type subscriptionUsageSample struct {
	SampledAt        int64                     `json:"sampledAt"`
	SampleIntervalMs int64                     `json:"sampleIntervalMs"`
	Provider         subscriptionProviderUsage `json:"provider"`
}

type subscriptionUsageHistory struct {
	SampleIntervalMs int64                     `json:"sampleIntervalMs"`
	Samples          []subscriptionUsageSample `json:"samples"`
}

func createSubscriptionUsageHistory(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE subscription_usage_samples (
		platform TEXT NOT NULL, sampled_at INTEGER NOT NULL, payload TEXT NOT NULL,
		PRIMARY KEY (platform, sampled_at)
	); CREATE INDEX subscription_usage_sampled ON subscription_usage_samples(sampled_at);`)
	return err
}

// Store normalized account data only. One row per platform and five-minute
// bucket keeps restarts and duplicate collectors from accumulating samples.
func recordSubscriptionUsage(ctx context.Context, db *sql.DB, now time.Time, providers []subscriptionProviderUsage) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Compact only complete old UTC hours, keeping the latest real snapshot.
	// Never average quota windows across resets or discard the hourly history.
	cutoff := now.Add(-subscriptionHistoryFineRetention).Truncate(time.Hour).UnixMilli()
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscription_usage_samples WHERE sampled_at < ?
		AND (platform, sampled_at) NOT IN (
			SELECT platform, max(sampled_at) FROM subscription_usage_samples WHERE sampled_at < ?
			GROUP BY platform, sampled_at / ?
		)`, cutoff, cutoff, time.Hour.Milliseconds()); err != nil {
		return err
	}
	for _, provider := range providers {
		provider.Spend = nil // the TypeSafe estimate ledger is not subscription usage
		payload, err := json.Marshal(provider)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscription_usage_samples (platform, sampled_at, payload)
			VALUES (?, ?, ?) ON CONFLICT(platform, sampled_at) DO UPDATE SET payload=excluded.payload`,
			provider.ID, now.Truncate(subscriptionSampleInterval).UnixMilli(), string(payload)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func readSubscriptionUsageHistory(ctx context.Context, db *sql.DB, now time.Time, days int) (subscriptionUsageHistory, error) {
	out := subscriptionUsageHistory{SampleIntervalMs: subscriptionSampleInterval.Milliseconds(), Samples: []subscriptionUsageSample{}}
	since := int64(0)
	if days > 0 {
		since = now.Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	}
	rows, err := db.QueryContext(ctx, `SELECT sampled_at, payload FROM subscription_usage_samples
		WHERE sampled_at >= ? AND sampled_at <= ? ORDER BY sampled_at, platform`,
		since, now.UnixMilli())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var sample subscriptionUsageSample
		var payload string
		if err := rows.Scan(&sample.SampledAt, &payload); err != nil {
			return out, err
		}
		if err := json.Unmarshal([]byte(payload), &sample.Provider); err != nil {
			return out, err
		}
		sample.SampleIntervalMs = subscriptionSampleInterval.Milliseconds()
		if sample.SampledAt < now.Add(-subscriptionHistoryFineRetention).Truncate(time.Hour).UnixMilli() {
			sample.SampleIntervalMs = time.Hour.Milliseconds()
		}
		out.Samples = append(out.Samples, sample)
	}
	return out, rows.Err()
}
