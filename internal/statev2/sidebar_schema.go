package statev2

const sidebarSelect = `SELECT s.instance_id,s.id,s.title,s.directory,s.project_id,s.parent_id,
 s.created_at,max(s.updated_at,COALESCE(m.created_at,0)),COALESCE(m.created_at,0),s.source_deleted,s.is_subagent,
 COALESCE(json_extract(s.extensions,'$.SourceChild'),s.parent_id!=''),
 COALESCE(json_extract(s.extensions,'$.ProjectDirectory'),''),
 COALESCE(json_extract(s.extensions,'$.InferredStatus'),''),
 COALESCE(m.role,''),COALESCE(m.finish,''),COALESCE(NULLIF(m.error_name,''),CASE WHEN m.has_error=1 THEN 'error' ELSE '' END)
 FROM session s LEFT JOIN message m ON m.instance_id=s.instance_id AND m.session_id=s.id AND m.id=(
 SELECT id FROM message WHERE instance_id=s.instance_id AND session_id=s.id AND source_deleted=0 AND conversation_visible=1 ORDER BY position DESC,id DESC LIMIT 1)`

const sidebarSchema = `
CREATE TABLE session_sidebar (
 instance_id TEXT NOT NULL,id TEXT NOT NULL,title TEXT NOT NULL,directory TEXT NOT NULL,
 project_id TEXT NOT NULL,parent_id TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,
 last_message_at INTEGER NOT NULL,source_deleted INTEGER NOT NULL,is_subagent INTEGER NOT NULL,
 source_child INTEGER NOT NULL,project_directory TEXT NOT NULL,inferred_status TEXT NOT NULL,
 last_role TEXT NOT NULL,last_finish TEXT NOT NULL,last_error TEXT NOT NULL,
 PRIMARY KEY(instance_id,id),FOREIGN KEY(instance_id,id) REFERENCES session(instance_id,id) ON DELETE CASCADE
) WITHOUT ROWID;
CREATE INDEX sidebar_recency ON session_sidebar(instance_id,updated_at DESC,id);
CREATE INDEX sidebar_directory ON session_sidebar(instance_id,directory,updated_at DESC,id);
CREATE TRIGGER sidebar_source_deleted AFTER UPDATE OF source_deleted ON session BEGIN
 UPDATE session_sidebar SET source_deleted=NEW.source_deleted WHERE instance_id=NEW.instance_id AND id=NEW.id;
END;
CREATE INDEX message_unread_range ON message(instance_id,session_id,source_deleted,created_at);
CREATE TABLE sidebar_branch (
 owner_id TEXT NOT NULL,directory TEXT NOT NULL,branch TEXT NOT NULL,observed_at INTEGER NOT NULL,
 PRIMARY KEY(owner_id,directory)
) WITHOUT ROWID;
CREATE TABLE sidebar_connection (
 owner_id TEXT NOT NULL,platform TEXT NOT NULL,source_key TEXT NOT NULL,generation TEXT NOT NULL,expires_at INTEGER NOT NULL,
 PRIMARY KEY(owner_id,platform,source_key)
) WITHOUT ROWID;
CREATE TABLE sidebar_observation (
 owner_id TEXT NOT NULL,platform TEXT NOT NULL,source_key TEXT NOT NULL,native_id TEXT NOT NULL,
 generation TEXT NOT NULL,status TEXT NOT NULL,resets_at INTEGER NOT NULL,pending_permission INTEGER NOT NULL,pending_question INTEGER NOT NULL,
 PRIMARY KEY(owner_id,platform,source_key,native_id)
) WITHOUT ROWID;
INSERT INTO session_sidebar ` + sidebarSelect + `;`
