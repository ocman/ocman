package statev2

// A stable integer key is necessary: VACUUM can renumber part's implicit rowids.
// The index stores terms, not a second copy of the transcript. Visibility is
// also checked against authoritative rows on every read.
const searchIndexSchema = `
CREATE TABLE IF NOT EXISTS session_search_doc (
  doc_id INTEGER PRIMARY KEY,
  instance_id TEXT NOT NULL, session_id TEXT NOT NULL, message_id TEXT NOT NULL, part_id TEXT NOT NULL,
  UNIQUE(instance_id,session_id,message_id,part_id)
);
CREATE VIRTUAL TABLE IF NOT EXISTS session_search_fts USING fts5(
  text, content='', contentless_delete=1, tokenize='trigram case_sensitive 1'
);
INSERT INTO session_search_fts(session_search_fts,rank) VALUES('secure-delete',1);
CREATE TABLE IF NOT EXISTS session_search_progress (
  id INTEGER PRIMARY KEY CHECK(id=1), cursor_instance TEXT NOT NULL, cursor_session TEXT NOT NULL,
  cursor_message TEXT NOT NULL, cursor_part TEXT NOT NULL, done INTEGER NOT NULL
);
INSERT OR IGNORE INTO session_search_progress VALUES(1,'','','','',NOT EXISTS(SELECT 1 FROM part));

CREATE VIEW IF NOT EXISTS session_search_eligible AS
  SELECT p.instance_id,p.session_id,p.message_id,p.id,p.text FROM part p
  JOIN message m ON (m.instance_id,m.session_id,m.id)=(p.instance_id,p.session_id,p.message_id)
  WHERE p.kind='text' AND p.source_deleted=0 AND length(p.text)>=3
    AND m.source_deleted=0 AND m.conversation_visible=1 AND m.role IN ('user','assistant');

CREATE TRIGGER IF NOT EXISTS session_search_doc_insert AFTER INSERT ON session_search_doc BEGIN
  INSERT INTO session_search_fts(rowid,text)
    SELECT new.doc_id,lower(text) FROM part
    WHERE (instance_id,session_id,message_id,id)=(new.instance_id,new.session_id,new.message_id,new.part_id);
END;
CREATE TRIGGER IF NOT EXISTS session_search_doc_delete AFTER DELETE ON session_search_doc BEGIN
  DELETE FROM session_search_fts WHERE rowid=old.doc_id;
END;
CREATE TRIGGER IF NOT EXISTS session_search_part_insert AFTER INSERT ON part
WHEN new.kind='text' AND new.source_deleted=0 BEGIN
  INSERT OR IGNORE INTO session_search_doc(instance_id,session_id,message_id,part_id)
    SELECT instance_id,session_id,message_id,id FROM session_search_eligible
    WHERE (instance_id,session_id,message_id,id)=(new.instance_id,new.session_id,new.message_id,new.id);
END;
CREATE TRIGGER IF NOT EXISTS session_search_part_update AFTER UPDATE ON part
WHEN (old.kind='text' OR new.kind='text') AND (
  (old.instance_id,old.session_id,old.message_id,old.id,old.kind,old.source_deleted,old.text) IS NOT
  (new.instance_id,new.session_id,new.message_id,new.id,new.kind,new.source_deleted,new.text)
) BEGIN
  DELETE FROM session_search_doc WHERE (instance_id,session_id,message_id,part_id)=(old.instance_id,old.session_id,old.message_id,old.id);
  INSERT OR IGNORE INTO session_search_doc(instance_id,session_id,message_id,part_id)
    SELECT instance_id,session_id,message_id,id FROM session_search_eligible
    WHERE (instance_id,session_id,message_id,id)=(new.instance_id,new.session_id,new.message_id,new.id);
END;
CREATE TRIGGER IF NOT EXISTS session_search_part_delete AFTER DELETE ON part
WHEN old.kind='text' BEGIN
  DELETE FROM session_search_doc WHERE (instance_id,session_id,message_id,part_id)=(old.instance_id,old.session_id,old.message_id,old.id);
END;
CREATE TRIGGER IF NOT EXISTS session_search_message_update AFTER UPDATE OF role,source_deleted,conversation_visible ON message
WHEN (old.role IN ('user','assistant') AND old.source_deleted=0 AND old.conversation_visible=1) IS NOT
     (new.role IN ('user','assistant') AND new.source_deleted=0 AND new.conversation_visible=1) BEGIN
  DELETE FROM session_search_doc WHERE (instance_id,session_id,message_id)=(old.instance_id,old.session_id,old.id);
  INSERT OR IGNORE INTO session_search_doc(instance_id,session_id,message_id,part_id)
    SELECT instance_id,session_id,message_id,id FROM session_search_eligible
    WHERE (instance_id,session_id,message_id)=(new.instance_id,new.session_id,new.id);
END;
`
