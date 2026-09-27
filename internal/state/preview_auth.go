package state

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Preview-provider consent state (authenticated rich link previews).
//
// A viewer is one browser on one owner machine: the browser holds a random
// cookie secret and state.db stores only its SHA-256 (ViewerID), so reading
// the database does not let anyone impersonate the browser. Every row below
// is keyed by (viewer_id, owner_id): a credential never answers for another
// browser or another machine. Tokens and PKCE verifiers are AES-GCM sealed
// with a dedicated key, and the row key is the AEAD's associated data, so a
// ciphertext copied onto another viewer's row fails to open.

// ErrPreviewNotFound reports a missing viewer, state or credential row.
var ErrPreviewNotFound = errors.New("preview auth record not found")

const previewKeySetting = "preview_credential_key"

func migrateToV100(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS preview_viewer (
			viewer_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (viewer_id, owner_id)
		);
		CREATE TABLE IF NOT EXISTS preview_oauth_state (
			state_hash TEXT PRIMARY KEY,
			viewer_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			verifier_enc BLOB NOT NULL,
			return_to TEXT NOT NULL,
			expires_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS preview_credential (
			viewer_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			workspace_name TEXT NOT NULL DEFAULT '',
			account_name TEXT NOT NULL DEFAULT '',
			sites_json TEXT NOT NULL DEFAULT '[]',
			access_enc BLOB NOT NULL,
			refresh_enc BLOB,
			expires_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (viewer_id, owner_id, provider, workspace_id)
		);`)
	return err
}

// PreviewSite is one site/project reachable through a single grant (e.g.
// the Jira sites of one Atlassian token).
type PreviewSite struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PreviewCredential is one viewer's grant for one provider workspace.
// AccessToken/RefreshToken are only populated by PreviewCredential reads for
// server-side provider calls; they must never be serialized to a client.
type PreviewCredential struct {
	ViewerID, OwnerID, Provider, WorkspaceID string
	WorkspaceName, AccountName               string
	Sites                                    []PreviewSite
	AccessToken, RefreshToken                string
	ExpiresAt                                time.Time // zero = no expiry
	Refreshable                              bool      // a refresh token is stored
}

// PreviewOAuthState is a pending, one-time authorization request.
type PreviewOAuthState struct {
	ViewerID, OwnerID, Provider, Verifier, ReturnTo string
	ExpiresAt                                       time.Time
}

func (d *DB) previewCipher(ctx context.Context) (cipher.AEAD, error) {
	d.previewKeyMu.Lock()
	defer d.previewKeyMu.Unlock()
	stored, ok, err := d.GetSetting(ctx, previewKeySetting)
	if err != nil {
		return nil, err
	}
	var key []byte
	if ok && stored != "" {
		if key, err = base64.RawStdEncoding.DecodeString(stored); err != nil || len(key) != 32 {
			return nil, errors.New("invalid preview credential key")
		}
	} else {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := d.SetSetting(ctx, previewKeySetting, base64.RawStdEncoding.EncodeToString(key)); err != nil {
			return nil, err
		}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (d *DB) previewSeal(aead cipher.AEAD, plaintext, aad string) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(plaintext), []byte(aad)), nil
}

func (d *DB) previewOpen(aead cipher.AEAD, sealed []byte, aad string) (string, error) {
	if len(sealed) < aead.NonceSize() {
		return "", errors.New("sealed preview secret too short")
	}
	pt, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(aad))
	if err != nil {
		// Never wrap: the error must not carry ciphertext details.
		return "", errors.New("preview secret cannot be decrypted")
	}
	return string(pt), nil
}

func credentialAAD(c PreviewCredential, field string) string {
	return fmt.Sprintf("cred\x00%s\x00%s\x00%s\x00%s\x00%s", c.ViewerID, c.OwnerID, c.Provider, c.WorkspaceID, field)
}

// CreatePreviewViewer registers a viewer (hashed cookie secret) for owner.
func (d *DB) CreatePreviewViewer(ctx context.Context, viewerID, ownerID string) error {
	_, err := d.db.ExecContext(ctx, `INSERT OR IGNORE INTO preview_viewer (viewer_id, owner_id, created_at) VALUES (?, ?, ?)`,
		viewerID, ownerID, time.Now().UnixMilli())
	return err
}

// PreviewViewerExists reports whether viewerID is registered for ownerID.
func (d *DB) PreviewViewerExists(ctx context.Context, viewerID, ownerID string) (bool, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM preview_viewer WHERE viewer_id=? AND owner_id=?`, viewerID, ownerID).Scan(&n)
	return n > 0, err
}

// DeletePreviewViewer forgets a viewer with all its pending states and
// credentials (browser sign-out). It returns the deleted credentials so the
// caller can revoke them at the provider.
func (d *DB) DeletePreviewViewer(ctx context.Context, viewerID, ownerID string) ([]PreviewCredential, error) {
	creds, err := d.previewCredentials(ctx, viewerID, ownerID, "", true)
	if err != nil {
		return nil, err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM preview_credential WHERE viewer_id=? AND owner_id=?`,
		`DELETE FROM preview_oauth_state WHERE viewer_id=? AND owner_id=?`,
		`DELETE FROM preview_viewer WHERE viewer_id=? AND owner_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, viewerID, ownerID); err != nil {
			return nil, err
		}
	}
	return creds, tx.Commit()
}

// PutPreviewOAuthState stores a pending authorization keyed by the hash of
// its state parameter. Expired states are pruned on the way.
func (d *DB) PutPreviewOAuthState(ctx context.Context, stateHash string, s PreviewOAuthState) error {
	aead, err := d.previewCipher(ctx)
	if err != nil {
		return err
	}
	enc, err := d.previewSeal(aead, s.Verifier, "state\x00"+stateHash)
	if err != nil {
		return err
	}
	if _, err := d.db.ExecContext(ctx, `DELETE FROM preview_oauth_state WHERE expires_at < ?`, time.Now().UnixMilli()); err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, `INSERT INTO preview_oauth_state (state_hash, viewer_id, owner_id, provider, verifier_enc, return_to, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, stateHash, s.ViewerID, s.OwnerID, s.Provider, enc, s.ReturnTo, s.ExpiresAt.UnixMilli())
	return err
}

// TakePreviewOAuthState atomically consumes a pending authorization. A state
// can be taken once; expired states report ErrPreviewNotFound.
func (d *DB) TakePreviewOAuthState(ctx context.Context, stateHash string) (PreviewOAuthState, error) {
	var s PreviewOAuthState
	var enc []byte
	var exp int64
	err := d.db.QueryRowContext(ctx, `DELETE FROM preview_oauth_state WHERE state_hash=?
		RETURNING viewer_id, owner_id, provider, verifier_enc, return_to, expires_at`, stateHash).
		Scan(&s.ViewerID, &s.OwnerID, &s.Provider, &enc, &s.ReturnTo, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrPreviewNotFound
	}
	if err != nil {
		return s, err
	}
	s.ExpiresAt = time.UnixMilli(exp)
	if !time.Now().Before(s.ExpiresAt) {
		return PreviewOAuthState{}, ErrPreviewNotFound
	}
	aead, err := d.previewCipher(ctx)
	if err != nil {
		return PreviewOAuthState{}, err
	}
	if s.Verifier, err = d.previewOpen(aead, enc, "state\x00"+stateHash); err != nil {
		return PreviewOAuthState{}, err
	}
	return s, nil
}

// PutPreviewCredential inserts or replaces one viewer's workspace grant.
func (d *DB) PutPreviewCredential(ctx context.Context, c PreviewCredential) error {
	aead, err := d.previewCipher(ctx)
	if err != nil {
		return err
	}
	access, err := d.previewSeal(aead, c.AccessToken, credentialAAD(c, "access"))
	if err != nil {
		return err
	}
	var refresh []byte
	if c.RefreshToken != "" {
		if refresh, err = d.previewSeal(aead, c.RefreshToken, credentialAAD(c, "refresh")); err != nil {
			return err
		}
	}
	sites, err := json.Marshal(append([]PreviewSite{}, c.Sites...))
	if err != nil {
		return err
	}
	var exp int64
	if !c.ExpiresAt.IsZero() {
		exp = c.ExpiresAt.UnixMilli()
	}
	_, err = d.db.ExecContext(ctx, `INSERT INTO preview_credential
		(viewer_id, owner_id, provider, workspace_id, workspace_name, account_name, sites_json, access_enc, refresh_enc, expires_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(viewer_id, owner_id, provider, workspace_id) DO UPDATE SET
			workspace_name=excluded.workspace_name, account_name=excluded.account_name, sites_json=excluded.sites_json,
			access_enc=excluded.access_enc, refresh_enc=excluded.refresh_enc, expires_at=excluded.expires_at, updated_at=excluded.updated_at`,
		c.ViewerID, c.OwnerID, c.Provider, c.WorkspaceID, c.WorkspaceName, c.AccountName, string(sites), access, refresh, exp, time.Now().UnixMilli())
	return err
}

// PreviewCredentials lists a viewer's grants without secrets. An empty
// provider lists every provider.
func (d *DB) PreviewCredentials(ctx context.Context, viewerID, ownerID, provider string) ([]PreviewCredential, error) {
	return d.previewCredentials(ctx, viewerID, ownerID, provider, false)
}

// PreviewCredential reads one grant including its decrypted tokens, for
// server-side provider calls only.
func (d *DB) PreviewCredential(ctx context.Context, viewerID, ownerID, provider, workspaceID string) (PreviewCredential, error) {
	creds, err := d.previewCredentials(ctx, viewerID, ownerID, provider, true)
	if err != nil {
		return PreviewCredential{}, err
	}
	for _, c := range creds {
		if c.WorkspaceID == workspaceID {
			return c, nil
		}
	}
	return PreviewCredential{}, ErrPreviewNotFound
}

func (d *DB) previewCredentials(ctx context.Context, viewerID, ownerID, provider string, secrets bool) ([]PreviewCredential, error) {
	// Resolve the key before the query: state.db has one connection, so
	// reading the key setting while rows are open would deadlock.
	var aead cipher.AEAD
	if secrets {
		var err error
		if aead, err = d.previewCipher(ctx); err != nil {
			return nil, err
		}
	}
	rows, err := d.db.QueryContext(ctx, `SELECT provider, workspace_id, workspace_name, account_name, sites_json, access_enc, refresh_enc, expires_at
		FROM preview_credential WHERE viewer_id=? AND owner_id=? AND (?='' OR provider=?) ORDER BY provider, workspace_name, workspace_id`,
		viewerID, ownerID, provider, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PreviewCredential
	for rows.Next() {
		c := PreviewCredential{ViewerID: viewerID, OwnerID: ownerID}
		var sites string
		var access, refresh []byte
		var exp int64
		if err := rows.Scan(&c.Provider, &c.WorkspaceID, &c.WorkspaceName, &c.AccountName, &sites, &access, &refresh, &exp); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(sites), &c.Sites); err != nil {
			return nil, err
		}
		if exp > 0 {
			c.ExpiresAt = time.UnixMilli(exp)
		}
		c.Refreshable = len(refresh) > 0
		if secrets {
			if c.AccessToken, err = d.previewOpen(aead, access, credentialAAD(c, "access")); err != nil {
				return nil, err
			}
			if len(refresh) > 0 {
				if c.RefreshToken, err = d.previewOpen(aead, refresh, credentialAAD(c, "refresh")); err != nil {
					return nil, err
				}
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeletePreviewCredentials removes a viewer's grants for provider; an empty
// workspaceID removes every workspace. Returns the deleted grants with
// secrets so the caller can revoke them at the provider.
func (d *DB) DeletePreviewCredentials(ctx context.Context, viewerID, ownerID, provider, workspaceID string) ([]PreviewCredential, error) {
	creds, err := d.previewCredentials(ctx, viewerID, ownerID, provider, true)
	if err != nil {
		return nil, err
	}
	var gone []PreviewCredential
	for _, c := range creds {
		if workspaceID == "" || c.WorkspaceID == workspaceID {
			gone = append(gone, c)
		}
	}
	_, err = d.db.ExecContext(ctx, `DELETE FROM preview_credential WHERE viewer_id=? AND owner_id=? AND provider=? AND (?='' OR workspace_id=?)`,
		viewerID, ownerID, provider, workspaceID, workspaceID)
	return gone, err
}
