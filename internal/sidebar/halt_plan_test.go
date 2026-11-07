package sidebar

import (
	"reflect"
	"strings"
	"testing"
)

// Halts are sparse user actions, not a reason to walk every retained session
// on each sidebar poll, even when there are no halt settings at all.
func TestSidebarHaltSeedUsesSparseLookups(t *testing.T) {
	r, _, _ := sidebarInventory(t, 10000)
	plan := queryPlan(t, r, selectSessions, sparseArgs(false))
	inHalted, seed, found := false, false, false
	for _, detail := range plan {
		if detail == "MATERIALIZE halted" || detail == "CO-ROUTINE halted" {
			inHalted = true
		}
		if inHalted && detail == "SETUP" {
			seed = true
			found = true
		}
		if seed && detail == "RECURSIVE STEP" {
			seed = false
			inHalted = false
		}
		if seed && (strings.Contains(detail, "SCAN c") || (strings.Contains(detail, "SEARCH c ") && !strings.Contains(detail, "(id=?)") && !strings.Contains(detail, "native_id=?"))) {
			t.Errorf("halt seed walks catalogue instead of looking up halted identities: %s", detail)
		}
	}
	if !found {
		t.Fatal("halt seed missing from query plan")
	}
}

// Preserve the old seed as an independent oracle for canonical/legacy keys,
// cycles, parent propagation, retained sources and publication boundaries.
const catalogueHaltCTE = `, halted(id,parent_id,at,owner_id,platform,source_key) AS (
 SELECT c.id,c.parent_native_id,h.updated_at,c.owner_id,c.platform,c.source_key FROM core.session_catalog c
 JOIN runtime r ON r.owner=c.owner_id AND r.platform=c.platform AND r.source=c.source_key
 JOIN core.setting h ON h.key='session.halt.latest.'||c.id OR (c.legacy_keys=1 AND h.key='session.halt.latest.'||c.native_id)
 WHERE c.creation_state='ready' AND c.purged=0
 UNION SELECT c.id,c.parent_native_id,h.at,c.owner_id,c.platform,c.source_key FROM halted h
 JOIN core.session_catalog c ON c.native_id=h.parent_id AND c.native_id<>'' AND c.owner_id=h.owner_id AND c.platform=h.platform AND c.source_key=h.source_key
 WHERE c.creation_state='ready' AND c.purged=0
 ), halt_flags AS (SELECT id,max(at) AS at FROM halted GROUP BY id)`

func TestSparseHaltsMatchCatalogueWalk(t *testing.T) {
	r, core, _ := sidebarInventory(t, 100)
	_, err := core.SQL().Exec(`UPDATE session_catalog SET legacy_keys=1 WHERE updated_at IN (1,2,3);
 UPDATE session_catalog SET parent_native_id='native-2' WHERE native_id='native-1';
 UPDATE session_catalog SET parent_native_id='native-1' WHERE native_id='native-2';
 UPDATE session_catalog SET source_key='retained' WHERE native_id='native-4';
 UPDATE session_catalog SET purged=1 WHERE native_id='native-5';
 UPDATE session_catalog SET creation_state='configuring' WHERE native_id='native-6';
 INSERT INTO setting(key,value,updated_at) SELECT 'session.halt.latest.'||id,'',updated_at*10 FROM session_catalog WHERE updated_at<=6;
 INSERT INTO setting(key,value,updated_at) VALUES ('session.halt.latest.native-1','',99),('session.halt.latest.native-3','',77),('session.halt.latest.native-7','',999),('unrelated','',9999)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{selectSessions, selectInventory} {
		for _, limit := range []int{1, 50, -1} {
			args := sparseArgs(false)
			args[len(args)-2] = limit
			got := drainQuery(t, r, query, args)
			want := drainQuery(t, r, strings.Replace(query, haltsCTE, catalogueHaltCTE, 1), args)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("halt results differ at limit %d", limit)
			}
		}
	}
}
