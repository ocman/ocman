package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPreviewCredentialBoundToRowKey(t *testing.T) {
	d, err := Open(templateDBPath(t))
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
