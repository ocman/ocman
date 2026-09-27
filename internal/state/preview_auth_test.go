package state

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPreviewCredentialBoundToRowKey(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	c := PreviewCredential{ViewerID: "alice", OwnerID: "o", Provider: "p", WorkspaceID: "w", AccessToken: "secret-a", RefreshToken: "secret-r"}
	if err := d.PutPreviewCredential(ctx, c); err != nil {
		t.Fatal(err)
	}
	// Copying alice's sealed row onto bob's key must not yield her token.
	if _, err := d.db.Exec(`INSERT INTO preview_credential (viewer_id, owner_id, provider, workspace_id, access_enc, refresh_enc, updated_at)
		SELECT 'bob', owner_id, provider, workspace_id, access_enc, refresh_enc, updated_at FROM preview_credential WHERE viewer_id='alice'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PreviewCredential(ctx, "bob", "o", "p", "w"); err == nil {
		t.Fatal("swapped ciphertext decrypted for another viewer")
	}
	got, err := d.PreviewCredential(ctx, "alice", "o", "p", "w")
	if err != nil || got.AccessToken != "secret-a" || got.RefreshToken != "secret-r" || !got.Refreshable {
		t.Fatalf("alice = %+v, %v", got, err)
	}

	// Expired states cannot be taken.
	if err := d.PutPreviewOAuthState(ctx, "h", PreviewOAuthState{ViewerID: "alice", OwnerID: "o", Provider: "p", Verifier: "v", ReturnTo: "/", ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TakePreviewOAuthState(ctx, "h"); !errors.Is(err, ErrPreviewNotFound) {
		t.Fatalf("expired state = %v", err)
	}
}

func TestDeletePreviewViewerSpansOwners(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	for _, c := range []PreviewCredential{
		{ViewerID: "alice", OwnerID: "hub", Provider: "p", WorkspaceID: "w", AccessToken: "a-hub"},
		{ViewerID: "alice", OwnerID: "r1", Provider: "p", WorkspaceID: "w", AccessToken: "a-r1"},
		{ViewerID: "bob", OwnerID: "r1", Provider: "p", WorkspaceID: "w", AccessToken: "b-r1"},
	} {
		if err := d.PutPreviewCredential(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	gone, err := d.DeletePreviewViewer(ctx, "alice")
	if err != nil || len(gone) != 2 || gone[0].AccessToken == gone[1].AccessToken {
		t.Fatalf("gone = %+v, %v", gone, err)
	}
	if left, _ := d.PreviewCredentials(ctx, "alice", "r1", ""); len(left) != 0 {
		t.Fatalf("alice left = %+v", left)
	}
	if left, _ := d.PreviewCredentials(ctx, "bob", "r1", ""); len(left) != 1 || left[0].OwnerID != "r1" {
		t.Fatalf("bob = %+v", left)
	}
}
