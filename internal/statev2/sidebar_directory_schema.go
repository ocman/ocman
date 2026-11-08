package statev2

// Directory pages span instances, so the instance-prefixed directory index
// cannot stream their shared recency order without scanning unrelated history.
const sidebarDirectoryRecencySchema = `CREATE INDEX IF NOT EXISTS sidebar_directory_recency ON session_sidebar(directory,updated_at DESC,id);`
