package statev2

// Browser pages span instances. The per-instance recency index cannot satisfy
// their global order, forcing every retained row through joins and a temp sort.
const sidebarGlobalRecencySchema = `CREATE INDEX IF NOT EXISTS sidebar_global_recency ON session_sidebar(updated_at DESC,id);`
