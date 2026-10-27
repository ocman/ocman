package statev2

const sidebarBackgroundSchema = `
ALTER TABLE sidebar_observation ADD COLUMN background_tasks INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sidebar_observation ADD COLUMN background_services INTEGER NOT NULL DEFAULT 0;
`
