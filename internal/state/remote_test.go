package state

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	d, err := OpenFromSQL(raw)
	if err != nil {
		t.Fatalf("OpenFromSQL: %v", err)
	}
	return d
}

func TestInstanceIdentity_StableAcrossCalls(t *testing.T) {
	d := newTestDB(t)
	first, err := d.InstanceIdentity()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.InstanceID == "" || first.RemoteToken == "" {
		t.Fatalf("expected non-empty id and token, got %+v", first)
	}
	second, err := d.InstanceIdentity()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.InstanceID != second.InstanceID || first.RemoteToken != second.RemoteToken {
		t.Fatalf("identity changed across calls: %+v vs %+v", first, second)
	}
}

func TestInstanceIdentity_DistinctAcrossInstances(t *testing.T) {
	a, err := newTestDB(t).InstanceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newTestDB(t).InstanceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if a.InstanceID == b.InstanceID {
		t.Fatalf("two instances share an ID: %s", a.InstanceID)
	}
}

func TestRemote_TokenRoundTrip(t *testing.T) {
	d := newTestDB(t)
	// Seed an auth secret so the cipher has key material.
	if err := d.SetAuthSecret([]byte("test-hmac-key-material")); err != nil {
		t.Fatal(err)
	}

	id, err := d.AddRemote("ws.local:8229", "super-secret-token", "Workstation")
	if err != nil {
		t.Fatalf("AddRemote: %v", err)
	}

	got, err := d.RemoteToken(id)
	if err != nil {
		t.Fatalf("RemoteToken: %v", err)
	}
	if got != "super-secret-token" {
		t.Fatalf("token round-trip mismatch: got %q", got)
	}

	// Stored token must not be plaintext.
	var sealed []byte
	if err := d.db.QueryRow(`SELECT token_encrypted FROM remote WHERE local_id = ?`, id).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if string(sealed) == "super-secret-token" {
		t.Fatal("token stored in plaintext")
	}
}

func TestRemote_TokenCryptoWithoutAuthSecret(t *testing.T) {
	d := newTestDB(t) // no auth secret set
	id, err := d.AddRemote("ws:8229", "tok", "Box")
	if err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	got, err := d.RemoteToken(id)
	if err != nil {
		t.Fatalf("RemoteToken: %v", err)
	}
	if got != "tok" {
		t.Fatalf("got %q", got)
	}
}

func TestRemote_CRUD(t *testing.T) {
	d := newTestDB(t)
	id, err := d.AddRemote("addr:1", "t1", "Name1")
	if err != nil {
		t.Fatal(err)
	}

	r, err := d.GetRemote(id)
	if err != nil {
		t.Fatal(err)
	}
	if r.DisplayName != "Name1" || r.Address != "addr:1" || !r.Enabled || r.RemoteID != "" {
		t.Fatalf("unexpected remote: %+v", r)
	}

	// Update config without replacing token.
	if err := d.UpdateRemoteConfig(id, "Name2", "addr:2", false, nil); err != nil {
		t.Fatal(err)
	}
	r, _ = d.GetRemote(id)
	if r.DisplayName != "Name2" || r.Address != "addr:2" || r.Enabled {
		t.Fatalf("update not applied: %+v", r)
	}
	if tok, _ := d.RemoteToken(id); tok != "t1" {
		t.Fatalf("token should be unchanged, got %q", tok)
	}

	// Replace token.
	newTok := "t2"
	if err := d.UpdateRemoteConfig(id, "Name2", "addr:2", false, &newTok); err != nil {
		t.Fatal(err)
	}
	if tok, _ := d.RemoteToken(id); tok != "t2" {
		t.Fatalf("token should be replaced, got %q", tok)
	}

	// Learn health + remote_id.
	if err := d.SetRemoteHealth(id, "abc123", "connected", "ws.host", 1, 12345); err != nil {
		t.Fatal(err)
	}
	r, _ = d.GetRemote(id)
	if r.RemoteID != "abc123" || r.LastHealth != "connected" || r.Hostname != "ws.host" || r.ProtocolVersion != 1 || r.LastSeen != 12345 {
		t.Fatalf("health not applied: %+v", r)
	}

	// Transient failure with empty remoteID keeps learned ID.
	if err := d.SetRemoteHealth(id, "", "offline", "", 0, 99999); err != nil {
		t.Fatal(err)
	}
	r, _ = d.GetRemote(id)
	if r.RemoteID != "abc123" || r.LastHealth != "offline" || r.Hostname != "ws.host" {
		t.Fatalf("transient failure clobbered learned data: %+v", r)
	}

	// List then delete.
	list, err := d.ListRemotes()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 remote, got %d", len(list))
	}
	if err := d.DeleteRemote(id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetRemote(id); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows after delete, got %v", err)
	}
}

func TestRemote_DuplicateRemoteIDRejected(t *testing.T) {
	d := newTestDB(t)
	a, _ := d.AddRemote("a:1", "t", "A")
	b, _ := d.AddRemote("b:1", "t", "B")
	if err := d.SetRemoteHealth(a, "dup", "connected", "h", 1, 1); err != nil {
		t.Fatal(err)
	}
	// Second remote learning the same remote_id must fail the UNIQUE constraint.
	if err := d.SetRemoteHealth(b, "dup", "connected", "h", 1, 1); err == nil {
		t.Fatal("expected UNIQUE violation for duplicate remote_id")
	}
}
