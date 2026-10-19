package statev2

const analyticsIncrementalSchema = `
ALTER TABLE analytics_session ADD COLUMN is_subagent INTEGER NOT NULL DEFAULT 0 CHECK(is_subagent IN (0,1));
UPDATE analytics_session SET is_subagent=(SELECT s.is_subagent FROM session s WHERE s.instance_id=analytics_session.instance_id AND s.id=analytics_session.id);
CREATE INDEX analytics_fact_hour ON analytics_fact(instance_id,session_id,(time_created/3600000)*3600000);
`
