package state

import "testing"

func TestSidebarFlagsReadRemotePreferencesInOneQuery(t *testing.T) {
	exporter := recordStateSQL(t)
	d := openTestStateDB(t)
	key := Key{Platform: "r-remote:test", SessionID: "session"}
	for _, err := range []error{
		d.MarkSessionSeen(t.Context(), key.Platform, key.SessionID, 20, true), d.PinSession(t.Context(), key.Platform, key.SessionID),
		d.SnoozeSession(t.Context(), key.Platform, key.SessionID, 9999999999999), d.ArchiveProject(t.Context(), "remote", "/repo"), d.SetSetting(t.Context(), "session.archive_resurface", "activity"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	exporter.Reset()
	flags, err := d.SidebarFlags(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if flags.Seen[key].TimeUpdated != 20 || !flags.Seen[key].Interrupted || flags.Pinned[key] == 0 || flags.Snoozed[key] != 9999999999999 || flags.Projects[ProjectKey{RemoteID: "remote", Root: "/repo"}] == 0 || flags.Mode != "activity" {
		t.Fatalf("flags=%+v", flags)
	}
	queries := 0
	for _, span := range exporter.GetSpans() {
		if span.Name == "sql.conn.query" {
			queries++
		}
	}
	if queries != 1 {
		t.Fatalf("flag reads=%d", queries)
	}
	_ = d.Close()
	if _, err := d.SidebarFlags(t.Context()); err == nil {
		t.Fatal("closed store accepted flags read")
	}
}
