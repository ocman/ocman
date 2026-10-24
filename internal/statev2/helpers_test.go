package statev2

import (
	"encoding/json"
	"testing"
)

func TestReadableConversationFormat(t *testing.T) {
	for format, want := range map[int]bool{0: false, 1: true, ConversationReadFormat: true, ConversationReadFormat + 1: false} {
		if got := ReadableConversationFormat(format); got != want {
			t.Fatalf("format %d readable = %v, want %v", format, got, want)
		}
	}
}

func TestSourceInstanceIDIsStableAndScoped(t *testing.T) {
	id := SourceInstanceID("owner", "opencode", "source")
	if id != SourceInstanceID("owner", "opencode", "source") || len(id) != len("source-")+64 {
		t.Fatalf("unstable source identity: %q", id)
	}
	if id == SourceInstanceID("owner", "opencode", "other") || id == SourceInstanceID("other", "opencode", "source") {
		t.Fatal("source identity ignored part of its scope")
	}
}

func TestInstancesListsOneOwnerPlatform(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "second")
	if err := d.RegisterInstance(t.Context(), Instance{ID: "claude", OwnerID: "owner", Platform: "claudecode", SourceKey: "claude"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.Instances(t.Context(), "owner", "opencode")
	if err != nil || len(got) != 2 || got[0].ID != "first" || got[1].ID != "second" {
		t.Fatalf("instances: %+v %v", got, err)
	}
	if _, err := d.Instances(t.Context(), "", "opencode"); err == nil {
		t.Fatal("listed instances without an owner")
	}
	d.Close()
	if _, err := d.Instances(t.Context(), "owner", "opencode"); err == nil {
		t.Fatal("listed instances from a closed store")
	}
}

func TestSourceChildPrefersExplicitMarker(t *testing.T) {
	for _, tc := range []struct {
		session Session
		want    bool
	}{
		{Session{ParentID: "parent"}, true},
		{Session{}, false},
		{Session{ParentID: "parent", Extensions: json.RawMessage(`{"SourceChild":false}`)}, false},
		{Session{Extensions: json.RawMessage(`{"SourceChild":true}`)}, true},
	} {
		if got := SourceChild(tc.session); got != tc.want {
			t.Fatalf("SourceChild(%+v) = %v, want %v", tc.session, got, tc.want)
		}
	}
}

func TestSourceValueReadsOneKey(t *testing.T) {
	d := openTestDB(t)
	if got, err := d.SourceValue(t.Context(), "first", "version/session"); err != nil || got != "" {
		t.Fatalf("missing value: %q %v", got, err)
	}
	if err := d.SetSourceState(t.Context(), "first", "version/session", "v1"); err != nil {
		t.Fatal(err)
	}
	if got, err := d.SourceValue(t.Context(), "first", "version/session"); err != nil || got != "v1" {
		t.Fatalf("stored value: %q %v", got, err)
	}
}
