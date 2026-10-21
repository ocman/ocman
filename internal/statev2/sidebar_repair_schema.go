package statev2

// v11 was deployed before stream deltas maintained the projection. Rebuild
// only derived browsing rows; retained history and checkpoints are untouched.
const sidebarStreamRepair = `DELETE FROM session_sidebar;
INSERT INTO session_sidebar ` + sidebarSelect + `;`
