package sidebar

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestDirectoryPagesMatchGlobalScan(t *testing.T) {
	r, core, history := sidebarInventory(t, 200)
	conn, err := sql.Open("sqlite", history.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Newer rows in the same directory from other sources, platforms and owners
	// must retain the same catalogue fences and cross-instance ordering.
	for index, instance := range []statev2.Instance{
		{ID: "second", OwnerID: "owner", Platform: "test", SourceKey: "second"},
		{ID: "foreign", OwnerID: "foreign", Platform: "test", SourceKey: "foreign"},
		{ID: "other-platform", OwnerID: "owner", Platform: "other", SourceKey: "other"},
	} {
		if err := history.RegisterInstance(t.Context(), instance); err != nil {
			t.Fatal(err)
		}
		offset := (index + 1) * 1000
		if _, err := core.SQL().Exec(`INSERT INTO session_catalog(id,owner_id,platform,directory,title,origin,origin_id,creation_state,source_key,native_id,created_at,updated_at)
		 SELECT printf('ocm_%08d-0000-0000-0000-%012d',updated_at+?,updated_at+?),?, ?,directory,title,origin,origin_id,creation_state,?,native_id,created_at,updated_at+1000 FROM session_catalog WHERE owner_id='owner' AND source_key='source' AND updated_at>190`, offset, offset, instance.OwnerID, instance.Platform, instance.SourceKey); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`INSERT INTO session(instance_id,id,title,directory,created_at,updated_at,source_deleted,revision,extensions)
		 SELECT ?,printf('ocm_%08d-0000-0000-0000-%012d',updated_at+?,updated_at+?),title,directory,created_at,updated_at+1000,0,1,'{}' FROM session WHERE instance_id='instance' AND updated_at>190;
		 INSERT INTO session_sidebar SELECT instance_id,id,title,directory,project_id,parent_id,created_at,updated_at,updated_at,source_deleted,0,0,directory,'','assistant','stop','' FROM session WHERE instance_id=?`, instance.ID, offset, offset, instance.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := core.SQL().Exec(`INSERT INTO archived_session(platform,session_id,session_time_updated,archived_at)
	 SELECT platform,id,updated_at,updated_at FROM session_catalog WHERE updated_at%4=0;
	 INSERT INTO pinned_session(platform,session_id,pinned_at) SELECT platform,id,updated_at FROM session_catalog WHERE updated_at%7=0;
	 INSERT INTO seen_session(platform,session_id,session_time_updated,interrupted,seen_at) SELECT platform,id,updated_at,0,updated_at FROM session_catalog WHERE updated_at%5=0;
	 UPDATE archived_session SET snoozed_until=12345678900000 WHERE session_time_updated%8=0;
	 UPDATE session_catalog SET purged=1 WHERE updated_at%29=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE session_sidebar SET source_deleted=(updated_at%11=0),is_subagent=(updated_at%13=0),source_child=(updated_at%17=0);
	 UPDATE session_sidebar SET updated_at=200 WHERE updated_at BETWEEN 180 AND 190`); err != nil {
		t.Fatal(err)
	}
	instance := statev2.Instance{OwnerID: "owner", Platform: "test", SourceKey: "source"}
	if err := history.BeginSidebarGeneration(t.Context(), instance, "g"); err != nil {
		t.Fatal(err)
	}
	if err := history.ReplaceSidebarObservations(t.Context(), instance, "g", time.Now().Add(time.Hour).UnixMilli(), map[string]statev2.SidebarObservation{
		"native-17": {Status: "busy"}, "native-34": {Status: "busy", PendingQuestion: true},
		"native-52": {Status: "waiting", PendingPermission: true},
	}); err != nil {
		t.Fatal(err)
	}
	primary := Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}
	scopes := [][]Scope{
		{primary},
		{primary, {Owner: "owner", Platform: "test", Source: "second", Generation: "g"}},
		{primary, {Owner: "owner", Platform: "other", Source: "other", Generation: "g"}},
		{{Owner: "foreign", Platform: "test", Source: "foreign", Generation: "g"}},
		{{Owner: "missing", Platform: "test"}},
		{{Owner: "owner", Platform: "test", Source: "source", Generation: "old"}},
	}
	for _, live := range []bool{false, true} {
		reader := *r
		if live {
			reader.snapshots = history.SidebarLiveJSON
		}
		for i, scope := range scopes {
			for j, q := range []platforms.SidebarQuery{
				{Directory: "/one", Limit: 50},
				{Directory: "/two", Limit: 7, Offset: 3, Since: 150},
				{Directory: "/one", Limit: 50, Origin: "routine", OriginID: "r"},
				{Directory: "/two", Limit: 50, IncludeIDs: []string{"ocm_00000013-0000-0000-0000-000000000013"}},
				{Directory: "/one", Limit: 5, Offset: 1000},
				{Directory: "/missing", Limit: 5},
				{Directory: "/one' OR 1=1 --", Limit: 5},
			} {
				t.Run(fmt.Sprintf("live=%v/scope=%d/query=%d", live, i, j), func(t *testing.T) {
					want, err := reader.sessionsFor(t.Context(), scope, q, selectSessions)
					if err != nil {
						t.Fatal(err)
					}
					got, err := reader.SessionsFor(t.Context(), scope, q)
					if err != nil || !reflect.DeepEqual(want, got) {
						t.Fatalf("directory page changed rows or ordering: want=%d got=%d err=%v", len(want), len(got), err)
					}
				})
			}
		}
	}
}
