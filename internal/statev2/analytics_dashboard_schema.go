package statev2

// analyticsHourAggregates summarizes analytics_fact rows for one hour bucket.
// The extra outcome, timing and priced/unpriced splits let dashboard readers
// rebuild every request-level total without loading facts. Effective cost is
// reported cost when positive, otherwise linear token pricing of the unpriced
// token sums. Column order matches analytics_hour after the dashboard rebuild.
const analyticsHourAggregates = `min(time_created),max(time_created),count(*),
	sum(input_tokens),sum(output_tokens),sum(cache_read),sum(cache_write),sum(cost),
	sum(is_completed),sum(is_error),sum(is_completed AND NOT is_error),
	sum(duration_ms),sum(duration_ms>0),sum(agent_ms),sum(tool_ms),sum(unknown_ms),
	sum(CASE WHEN tokens_per_second>0 THEN tokens_per_second ELSE 0 END),sum(tokens_per_second>0),
	sum(CASE WHEN cache_read+cache_write>0 THEN cache_read*1.0/(cache_read+cache_write) ELSE 0 END),
	sum(CASE WHEN cost>0 THEN cost ELSE 0 END),
	sum(CASE WHEN cost>0 THEN 0 ELSE input_tokens END),sum(CASE WHEN cost>0 THEN 0 ELSE output_tokens END),
	sum(CASE WHEN cost>0 THEN 0 ELSE cache_read END),sum(CASE WHEN cost>0 THEN 0 ELSE cache_write END)`

// analyticsHourInsert rebuilds hour rows from facts; callers append filters
// before analyticsHourGroup.
const analyticsHourInsert = `INSERT INTO analytics_hour
	SELECT instance_id,session_id,(time_created/3600000)*3600000,role,provider,model,valid,agent,finish,` + analyticsHourAggregates + `
	FROM analytics_fact`

const analyticsHourGroup = ` GROUP BY instance_id,session_id,3,role,provider,model,valid,agent,finish`

// analyticsDashboardSchema adds agent and stop-reason dimensions plus outcome
// and timing sums to analytics_hour, then rebuilds it from retained facts.
const analyticsDashboardSchema = `
CREATE TABLE analytics_hour_next (
 instance_id TEXT NOT NULL, session_id TEXT NOT NULL, bucket INTEGER NOT NULL,
 role TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, valid INTEGER NOT NULL,
 agent TEXT NOT NULL, finish TEXT NOT NULL,
 first_at INTEGER NOT NULL, last_at INTEGER NOT NULL, messages INTEGER NOT NULL,
 input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
 cache_read INTEGER NOT NULL, cache_write INTEGER NOT NULL, cost REAL NOT NULL,
 completed INTEGER NOT NULL, errors INTEGER NOT NULL, successes INTEGER NOT NULL,
 duration_ms INTEGER NOT NULL, timed INTEGER NOT NULL,
 agent_ms INTEGER NOT NULL, tool_ms INTEGER NOT NULL, unknown_ms INTEGER NOT NULL,
 tokens_per_second REAL NOT NULL, throughput INTEGER NOT NULL, cache_efficiency REAL NOT NULL,
 priced_cost REAL NOT NULL, unpriced_input INTEGER NOT NULL, unpriced_output INTEGER NOT NULL,
 unpriced_read INTEGER NOT NULL, unpriced_write INTEGER NOT NULL,
 PRIMARY KEY(instance_id,session_id,bucket,role,provider,model,valid,agent,finish),
 FOREIGN KEY(instance_id,session_id) REFERENCES session(instance_id,id) ON DELETE CASCADE
) WITHOUT ROWID;
INSERT INTO analytics_hour_next
 SELECT instance_id,session_id,(time_created/3600000)*3600000,role,provider,model,valid,agent,finish,` + analyticsHourAggregates + `
 FROM analytics_fact` + analyticsHourGroup + `;
DROP TABLE analytics_hour;
ALTER TABLE analytics_hour_next RENAME TO analytics_hour;
CREATE INDEX analytics_hour_window ON analytics_hour(instance_id,role,bucket);
`
