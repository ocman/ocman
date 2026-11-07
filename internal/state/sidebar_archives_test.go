package state

import "testing"

func TestSidebarArchivesOnlyRequestedOwners(t *testing.T) {
	d := openTestStateDB(t)
	key := Key{Platform: "local", SessionID: "same"}
	other := Key{Platform: "remote:test", SessionID: "same"}
	for _, k := range []Key{key, other, {Platform: "local", SessionID: "unrelated"}} {
		if err := d.ArchiveSession(t.Context(), k.Platform, k.SessionID, 42); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SetSetting(t.Context(), "session.archive_resurface", "activity"); err != nil {
		t.Fatal(err)
	}
	archives, mode, err := d.SidebarArchives(t.Context(), []Key{key})
	if err != nil || mode != "activity" || len(archives) != 1 || archives[key] != 42 {
		t.Fatalf("%v %q %v", archives, mode, err)
	}
	archives, _, err = d.SidebarArchives(t.Context(), nil)
	if err != nil || len(archives) != 0 {
		t.Fatalf("empty keys: %v %v", archives, err)
	}
	_ = d.Close()
	if _, _, err := d.SidebarArchives(t.Context(), []Key{key}); err == nil {
		t.Fatal("closed store accepted read")
	}
}

func BenchmarkSidebarArchivePreferences(b *testing.B) {
	d := openTestStateDB(b)
	if _, err := d.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<20000)
	 INSERT INTO seen_session(platform,session_id,session_time_updated,seen_at,interrupted)
	 SELECT 'local','session-'||x,x,x,0 FROM n;
	 INSERT INTO archived_session(platform,session_id,session_time_updated,archived_at,snoozed_until) VALUES('local','watched',42,42,0)`); err != nil {
		b.Fatal(err)
	}
	for _, scoped := range []bool{false, true} {
		name := "all-preferences"
		if scoped {
			name = "watched-archives"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if scoped {
					if _, _, err := d.SidebarArchives(b.Context(), []Key{{Platform: "local", SessionID: "watched"}}); err != nil {
						b.Fatal(err)
					}
				} else if _, err := d.SidebarFlags(b.Context()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
