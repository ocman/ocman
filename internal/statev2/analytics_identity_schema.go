package statev2

// Sparse conflict markers make strict reader validation independent of history
// size. Triggers keep them atomic through refreshes, cascade purges and repairs.
// Markers include retained/deleted sources; readers check active selected owners.
const analyticsIdentitySchema = `
CREATE TABLE analytics_conflict (kind TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(kind,id)) WITHOUT ROWID;
INSERT INTO analytics_conflict SELECT 'session',id FROM analytics_session GROUP BY id HAVING count(*)>1;
INSERT INTO analytics_conflict SELECT 'message',id FROM analytics_fact GROUP BY id HAVING count(*)>1;
CREATE TRIGGER analytics_session_insert AFTER INSERT ON analytics_session BEGIN
 INSERT OR IGNORE INTO analytics_conflict SELECT 'session',NEW.id
 WHERE EXISTS(SELECT 1 FROM analytics_session WHERE id=NEW.id LIMIT 1 OFFSET 1);
END;
CREATE TRIGGER analytics_session_delete AFTER DELETE ON analytics_session BEGIN
 DELETE FROM analytics_conflict WHERE kind='session' AND id=OLD.id
 AND NOT EXISTS(SELECT 1 FROM analytics_session WHERE id=OLD.id LIMIT 1 OFFSET 1);
END;
CREATE TRIGGER analytics_fact_insert AFTER INSERT ON analytics_fact BEGIN
 INSERT OR IGNORE INTO analytics_conflict SELECT 'message',NEW.id
 WHERE EXISTS(SELECT 1 FROM analytics_fact WHERE id=NEW.id LIMIT 1 OFFSET 1);
END;
CREATE TRIGGER analytics_fact_delete AFTER DELETE ON analytics_fact BEGIN
 DELETE FROM analytics_conflict WHERE kind='message' AND id=OLD.id
 AND NOT EXISTS(SELECT 1 FROM analytics_fact WHERE id=OLD.id LIMIT 1 OFFSET 1);
END;
`
