package sessioncatalog

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"
)

func TestReconcileUnchangedInventoryUsesBoundedQueries(t *testing.T) {
	s, _ := openStore(t)
	rows := make([]NativeSession, 40)
	for i := range rows {
		rows[i] = NativeSession{ID: fmt.Sprint(i), Directory: "/repo"}
		if i > 0 {
			rows[i].ParentID = "0"
		}
	}
	if _, err := s.Reconcile(t.Context(), catalogSource(), rows); err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err := s.db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	var queries atomic.Int64
	counted, err := otelsql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)",
		otelsql.WithInstrumentAttributesGetter(func(_ context.Context, _ otelsql.Method, query string, _ []driver.NamedValue) []attribute.KeyValue {
			if query != "" {
				queries.Add(1)
			}
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer counted.Close()
	counted.SetMaxOpenConns(1)
	s.db = counted
	result, err := s.Reconcile(t.Context(), catalogSource(), rows)
	if err != nil || result != (ReconcileResult{}) {
		t.Fatalf("unchanged reconcile = %+v, %v", result, err)
	}
	if got := queries.Load(); got > 2 {
		t.Fatalf("unchanged inventory used %d SQL operations, want <=2", got)
	}
}

func TestLargeInventoryStillRepairsMetadataAndRelationships(t *testing.T) {
	for _, change := range []string{"title", "origin", "parent cleared", "missing parent", "late child", "configuring", "purged", "cycle", "partial source"} {
		t.Run(change, func(t *testing.T) {
			s, _ := openStore(t)
			input := make([]NativeSession, 40)
			for i := range input {
				input[i] = NativeSession{ID: fmt.Sprint(i), ParentID: "0", Directory: "/repo"}
			}
			input[0].ParentID = ""
			if _, err := s.Reconcile(t.Context(), catalogSource(), input); err != nil {
				t.Fatal(err)
			}
			first, err := s.FindBinding(t.Context(), nativeBinding("1"))
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "title":
				input[1].Title = "new title"
			case "origin":
				input[1].Origin, input[1].OriginID = Routine, "routine"
			case "parent cleared":
				input[1].ParentID = ""
			case "missing parent":
				input[1].ParentID = "missing"
			case "late child":
				if _, err := s.db.Exec(`UPDATE session_catalog SET parent_id=NULL WHERE id=?`, first.ID); err != nil {
					t.Fatal(err)
				}
				// The broken child is absent from this inventory but still repaired.
				input = append(input[:1], input[2:]...)
			case "configuring":
				if _, err := s.db.Exec(`UPDATE session_catalog SET creation_state='configuring' WHERE id=?`, first.ID); err != nil {
					t.Fatal(err)
				}
			case "purged":
				if _, err := s.db.Exec(`UPDATE session_catalog SET purged=1 WHERE id=?`, first.ID); err != nil {
					t.Fatal(err)
				}
			case "cycle":
				root, err := s.FindBinding(t.Context(), nativeBinding("0"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`UPDATE session_catalog SET parent_id=? WHERE id=?`, first.ID, root.ID); err != nil {
					t.Fatal(err)
				}
			case "partial source":
				extra := make([]NativeSession, 50)
				for i := range extra {
					extra[i] = NativeSession{ID: fmt.Sprintf("extra%d", i)}
				}
				if _, err := s.Reconcile(t.Context(), catalogSource(), extra); err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.Reconcile(t.Context(), catalogSource(), input)
			if change == "cycle" {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("cycle accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			after, err := s.Get(t.Context(), first.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "title":
				if after.Title != "new title" {
					t.Fatal("metadata not updated")
				}
			case "origin":
				if after.Origin != Routine || after.OriginID != "routine" {
					t.Fatal("origin not enriched")
				}
			case "parent cleared":
				if after.ParentID != "" || after.NativeParentID != "" {
					t.Fatal("native parent not cleared")
				}
			case "missing parent", "late child":
				if after.ParentID == "" {
					t.Fatal("parent not repaired")
				}
			case "configuring":
				if result.Deferred != 1 || after.CreationState != Configuring {
					t.Fatalf("configuration not preserved: %+v %+v", result, after)
				}
			case "purged":
				if !after.Purged {
					t.Fatal("purged session resurrected")
				}
			}
		})
	}
}

func TestInventoryParentValidation(t *testing.T) {
	for _, tt := range []struct {
		name    string
		parents map[string]string
		valid   bool
	}{
		{"shared", map[string]string{"a": "root", "b": "root", "root": ""}, true},
		{"cycle", map[string]string{"a": "b", "b": "a"}, false},
		{"missing", map[string]string{"a": "missing", "b": ""}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := validInventoryParents(t.Context(), tt.parents, []string{"a", "b"}); got != tt.valid {
				t.Fatalf("valid = %v, want %v", got, tt.valid)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if validInventoryParents(ctx, map[string]string{"a": ""}, []string{"a"}) {
		t.Fatal("cancelled validation succeeded")
	}
}
