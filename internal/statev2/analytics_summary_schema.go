package statev2

const analyticsSummarySchema = `
CREATE TABLE analytics_session (
 instance_id TEXT NOT NULL, id TEXT NOT NULL,
 parent_id TEXT NOT NULL, directory TEXT NOT NULL, title TEXT NOT NULL,
 time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, project_id TEXT NOT NULL,
 source_deleted INTEGER NOT NULL, user_messages INTEGER NOT NULL,
 input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL, cost REAL NOT NULL,
 PRIMARY KEY(instance_id,id),
 FOREIGN KEY(instance_id,id) REFERENCES session(instance_id,id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX analytics_session_native ON analytics_session(id);
CREATE TABLE analytics_fact (
 instance_id TEXT NOT NULL, session_id TEXT NOT NULL, id TEXT NOT NULL,
 time_created INTEGER NOT NULL, role TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, agent TEXT NOT NULL,
 cost REAL NOT NULL, input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
 cache_read INTEGER NOT NULL, cache_write INTEGER NOT NULL,
 started_at INTEGER NOT NULL, completed_at INTEGER NOT NULL, duration_ms INTEGER NOT NULL,
 agent_ms INTEGER NOT NULL, tool_ms INTEGER NOT NULL, unknown_ms INTEGER NOT NULL, tokens_per_second REAL NOT NULL,
 finish TEXT NOT NULL, is_error INTEGER NOT NULL, is_completed INTEGER NOT NULL, valid INTEGER NOT NULL,
 PRIMARY KEY(instance_id,session_id,id),
 FOREIGN KEY(instance_id,session_id) REFERENCES session(instance_id,id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX analytics_fact_window ON analytics_fact(instance_id,role,time_created);
CREATE INDEX analytics_fact_activity ON analytics_fact(instance_id,session_id,role,time_created);
CREATE INDEX analytics_fact_runtime ON analytics_fact(instance_id,role,completed_at,started_at);
CREATE INDEX analytics_fact_native ON analytics_fact(id);
CREATE TABLE analytics_hour (
 instance_id TEXT NOT NULL, session_id TEXT NOT NULL, bucket INTEGER NOT NULL,
 role TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, valid INTEGER NOT NULL,
 first_at INTEGER NOT NULL, last_at INTEGER NOT NULL, messages INTEGER NOT NULL,
 input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
 cache_read INTEGER NOT NULL, cache_write INTEGER NOT NULL, cost REAL NOT NULL,
 PRIMARY KEY(instance_id,session_id,bucket,role,provider,model,valid),
 FOREIGN KEY(instance_id,session_id) REFERENCES session(instance_id,id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX analytics_hour_window ON analytics_hour(instance_id,role,bucket);
CREATE TABLE analytics_question (
 instance_id TEXT NOT NULL, session_id TEXT NOT NULL, message_id TEXT NOT NULL, id TEXT NOT NULL,
 start INTEGER NOT NULL, end INTEGER NOT NULL,
 PRIMARY KEY(instance_id,session_id,message_id,id),
 FOREIGN KEY(instance_id,session_id) REFERENCES session(instance_id,id) ON DELETE CASCADE
) WITHOUT ROWID;
`
