package statev2

// Migrations are append-only. Projection-era databases (v5+) upgrade in place;
// summaries are backfilled from the retained compact projections, not platforms.
// Unsupported schemas fail closed. Stored history is never disposable.
var migrations = []string{`
CREATE TABLE instance (
	id TEXT PRIMARY KEY CHECK(id <> ''),
	owner_id TEXT NOT NULL CHECK(owner_id <> ''),
	platform TEXT NOT NULL CHECK(platform <> ''),
	source_key TEXT NOT NULL CHECK(source_key <> ''),
	name TEXT NOT NULL,
	UNIQUE(owner_id, platform, source_key)
);
CREATE TABLE session (
	instance_id TEXT NOT NULL REFERENCES instance(id),
	id TEXT NOT NULL CHECK(id <> ''),
	title TEXT NOT NULL, directory TEXT NOT NULL,
	created_at INTEGER NOT NULL CHECK(created_at >= 0),
	updated_at INTEGER NOT NULL CHECK(updated_at >= 0),
	source_deleted INTEGER NOT NULL CHECK(source_deleted IN (0,1)),
	revision INTEGER NOT NULL CHECK(revision > 0),
	extensions TEXT NOT NULL CHECK(json_valid(extensions)),
	PRIMARY KEY(instance_id, id)
);
CREATE TABLE message (
	instance_id TEXT NOT NULL, session_id TEXT NOT NULL,
	id TEXT NOT NULL CHECK(id <> ''),
	position INTEGER NOT NULL CHECK(position >= 0),
	role TEXT NOT NULL CHECK(role <> ''), model TEXT NOT NULL,
	created_at INTEGER NOT NULL CHECK(created_at >= 0),
	completed_at INTEGER NOT NULL CHECK(completed_at >= 0),
	source_deleted INTEGER NOT NULL CHECK(source_deleted IN (0,1)),
	extensions TEXT NOT NULL CHECK(json_valid(extensions)),
	PRIMARY KEY(instance_id, session_id, id),
	FOREIGN KEY(instance_id, session_id) REFERENCES session(instance_id, id) ON DELETE CASCADE
);
CREATE INDEX message_page ON message(instance_id, session_id, position, id);
CREATE TABLE part (
	instance_id TEXT NOT NULL, session_id TEXT NOT NULL, message_id TEXT NOT NULL,
	id TEXT NOT NULL CHECK(id <> ''),
	position INTEGER NOT NULL CHECK(position >= 0),
	kind TEXT NOT NULL CHECK(kind <> ''), text TEXT NOT NULL,
	source_deleted INTEGER NOT NULL CHECK(source_deleted IN (0,1)),
	extensions TEXT NOT NULL CHECK(json_valid(extensions)),
	PRIMARY KEY(instance_id, session_id, message_id, id),
	FOREIGN KEY(instance_id, session_id, message_id)
		REFERENCES message(instance_id, session_id, id) ON DELETE CASCADE
);
CREATE TABLE checkpoint (
	instance_id TEXT NOT NULL REFERENCES instance(id),
	stream TEXT NOT NULL CHECK(stream <> ''), cursor TEXT NOT NULL CHECK(cursor <> ''),
	PRIMARY KEY(instance_id, stream)
);
CREATE TABLE purged_session (
	instance_id TEXT NOT NULL REFERENCES instance(id),
	session_id TEXT NOT NULL CHECK(session_id <> ''),
	PRIMARY KEY(instance_id, session_id)
);
CREATE TABLE change_outbox (
	sequence INTEGER PRIMARY KEY AUTOINCREMENT,
	instance_id TEXT NOT NULL REFERENCES instance(id), session_id TEXT NOT NULL,
	revision INTEGER NOT NULL,
	kind TEXT NOT NULL CHECK(kind IN ('updated', 'purged'))
);
`, `
CREATE TABLE source_state (
	instance_id TEXT NOT NULL REFERENCES instance(id),
	key TEXT NOT NULL CHECK(key <> ''),
	value TEXT NOT NULL,
	PRIMARY KEY(instance_id, key)
);
`, `
ALTER TABLE session ADD COLUMN parent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE session ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE message ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE message ADD COLUMN cost REAL NOT NULL DEFAULT 0;
ALTER TABLE message ADD COLUMN tokens_input INTEGER NOT NULL DEFAULT 0;
ALTER TABLE message ADD COLUMN tokens_output INTEGER NOT NULL DEFAULT 0;
ALTER TABLE message ADD COLUMN tokens_reasoning INTEGER NOT NULL DEFAULT 0;
ALTER TABLE message ADD COLUMN tokens_cache_read INTEGER NOT NULL DEFAULT 0;
ALTER TABLE message ADD COLUMN tokens_cache_write INTEGER NOT NULL DEFAULT 0;
ALTER TABLE message ADD COLUMN finish TEXT NOT NULL DEFAULT '';
ALTER TABLE message ADD COLUMN has_error INTEGER NOT NULL DEFAULT 0 CHECK(has_error IN (0,1));
ALTER TABLE message ADD COLUMN error_name TEXT NOT NULL DEFAULT '';
CREATE INDEX message_created ON message(created_at);
CREATE INDEX message_model ON message(instance_id, model, created_at);
CREATE INDEX session_parent ON session(instance_id, parent_id);
CREATE INDEX session_project ON session(instance_id, project_id);
`, `
ALTER TABLE message ADD COLUMN agent TEXT NOT NULL DEFAULT '';
CREATE INDEX message_role ON message(instance_id, role, created_at);
CREATE INDEX session_created ON session(instance_id, created_at);
`, `
CREATE TABLE analytics_message (
	instance_id TEXT NOT NULL, session_id TEXT NOT NULL, id TEXT NOT NULL,
	time_created INTEGER NOT NULL, settled INTEGER NOT NULL,
	data TEXT NOT NULL CHECK(json_valid(data)),
	PRIMARY KEY(instance_id,session_id,id),
	FOREIGN KEY(instance_id,session_id) REFERENCES session(instance_id,id) ON DELETE CASCADE
);
CREATE INDEX analytics_message_time ON analytics_message(instance_id,time_created);
CREATE INDEX analytics_message_session ON analytics_message(instance_id,session_id,time_created,id);
CREATE INDEX analytics_message_role ON analytics_message(instance_id,json_extract(data,'$.role'),time_created);
CREATE TABLE analytics_part (
	instance_id TEXT NOT NULL, session_id TEXT NOT NULL, message_id TEXT NOT NULL, id TEXT NOT NULL,
	data TEXT NOT NULL CHECK(json_valid(data)),
	PRIMARY KEY(instance_id,session_id,message_id,id),
	FOREIGN KEY(instance_id,session_id) REFERENCES session(instance_id,id) ON DELETE CASCADE
);
CREATE INDEX analytics_part_message ON analytics_part(instance_id,message_id);
`, analyticsSummarySchema, analyticsIdentitySchema, conversationSchema, sessionSubagentSchema, projectsCacheSchema, sidebarSchema, sidebarStreamRepair, sidebarAttentionSchema, analyticsIncrementalSchema, preparationSchema, contextTokensSchema, sidebarBackgroundSchema}
