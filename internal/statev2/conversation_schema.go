package statev2

// Older snapshots remain retained but are not eligible for normal conversation
// reads until background ingestion supplies the missing wire-format metadata.
const conversationSchema = `
ALTER TABLE session ADD COLUMN read_format INTEGER NOT NULL DEFAULT 0;
ALTER TABLE message ADD COLUMN conversation_visible INTEGER NOT NULL DEFAULT 1;
ALTER TABLE part ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE part ADD COLUMN text_present INTEGER NOT NULL DEFAULT 0;
CREATE INDEX message_conversation_page ON message(instance_id,session_id,source_deleted,conversation_visible,position,id);
`

const ConversationReadFormat = 1

// Adapters now mark subagent sessions during ingestion. Rows imported before
// carry the title rule analytics filtered on until then, so counts do not
// change on upgrade; later imports replace it with the adapter's decision.
const sessionSubagentSchema = `
ALTER TABLE session ADD COLUMN is_subagent INTEGER NOT NULL DEFAULT 0 CHECK(is_subagent IN (0,1));
UPDATE session SET is_subagent=1 WHERE title LIKE '%(% subagent)';
`
