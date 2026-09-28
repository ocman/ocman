package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Artifacts are immutable bundles of files and links attached to a project
// (and optionally a session). File payloads live in the content-addressed
// blob store (artifact_blobs.go); rows here only reference them by SHA-256.

var (
	ErrArtifactNotFound = errors.New("artifact not found")
	ErrArtifactInvalid  = errors.New("invalid artifact")
)

const (
	ArtifactItemFile = "file"
	ArtifactItemLink = "link"

	defaultArtifactPage = 50
	maxArtifactPage     = 200
)

type ArtifactItem struct {
	Kind   string `json:"kind"`
	Name   string `json:"name,omitempty"`
	MIME   string `json:"mime,omitempty"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	URL    string `json:"url,omitempty"`
	Label  string `json:"label,omitempty"`
}

type Artifact struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Directory   string         `json:"directory"`
	Platform    string         `json:"platform,omitempty"`
	SessionID   string         `json:"sessionId,omitempty"`
	RemoteID    string         `json:"remoteId"`
	CreatedAt   time.Time      `json:"createdAt"`
	Items       []ArtifactItem `json:"items"`
}

// ArtifactFilter selects a page of artifacts, newest first.
type ArtifactFilter struct {
	Directory  string
	Platform   string
	SessionIDs []string // with Platform; include descendant sessions
	Q          string
	Limit      int
	Cursor     string
}

func migrateToV101(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS artifact (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			directory TEXT NOT NULL,
			platform TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			remote_id TEXT NOT NULL DEFAULT 'local',
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS artifact_created_idx ON artifact (created_at DESC, id);
		CREATE INDEX IF NOT EXISTS artifact_directory_idx ON artifact (directory, created_at DESC, id);
		CREATE INDEX IF NOT EXISTS artifact_session_idx ON artifact (platform, session_id);
		CREATE TABLE IF NOT EXISTS artifact_item (
			artifact_id TEXT NOT NULL REFERENCES artifact(id) ON DELETE CASCADE,
			ordinal INTEGER NOT NULL,
			kind TEXT NOT NULL CHECK (kind IN ('file','link')),
			name TEXT NOT NULL DEFAULT '',
			mime TEXT NOT NULL DEFAULT '',
			size INTEGER NOT NULL DEFAULT 0,
			sha256 TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			label TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (artifact_id, ordinal)
		);
		CREATE INDEX IF NOT EXISTS artifact_item_sha_idx ON artifact_item (sha256);
		CREATE TABLE IF NOT EXISTS artifact_share (
			id TEXT PRIMARY KEY,
			artifact_id TEXT NOT NULL REFERENCES artifact(id) ON DELETE CASCADE,
			relay_id TEXT NOT NULL,
			relay_key TEXT NOT NULL,
			relay_delete_token TEXT NOT NULL,
			relay_url TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			revoked_at INTEGER
		);
		CREATE INDEX IF NOT EXISTS artifact_share_artifact_idx ON artifact_share (artifact_id)`)
	return err
}

func (d *DB) validArtifactItem(it *ArtifactItem) bool {
	switch it.Kind {
	case ArtifactItemFile:
		if strings.TrimSpace(it.Name) == "" || !artifactSHA.MatchString(it.SHA256) || it.URL != "" {
			return false
		}
		size, err := d.artifactBlobSize(it.SHA256)
		if err != nil || size != it.Size || size > MaxArtifactFileBytes {
			return false
		}
		if it.MIME == "" {
			it.MIME = "application/octet-stream"
		}
		return true
	case ArtifactItemLink:
		u, err := url.Parse(it.URL)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && it.SHA256 == ""
	}
	return false
}

// CreateArtifact validates a and inserts it with its items in one transaction.
// File items must reference a blob already stored by PutArtifactBlob.
func (d *DB) CreateArtifact(ctx context.Context, a Artifact) (Artifact, error) {
	d.artifactMu.Lock()
	defer d.artifactMu.Unlock()
	if strings.TrimSpace(a.Title) == "" || a.Directory == "" || len(a.Items) == 0 || (a.Platform == "") != (a.SessionID == "") {
		return Artifact{}, ErrArtifactInvalid
	}
	a.Items = append([]ArtifactItem(nil), a.Items...)
	for i := range a.Items {
		if !d.validArtifactItem(&a.Items[i]) {
			return Artifact{}, ErrArtifactInvalid
		}
	}
	a.ID = strings.ToLower(rand.Text())
	if a.RemoteID == "" {
		a.RemoteID = "local"
	}
	a.CreatedAt = time.UnixMilli(time.Now().UnixMilli())
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return Artifact{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO artifact (id,title,description,directory,platform,session_id,remote_id,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		a.ID, a.Title, a.Description, a.Directory, a.Platform, a.SessionID, a.RemoteID, a.CreatedAt.UnixMilli()); err != nil {
		return Artifact{}, err
	}
	for i, it := range a.Items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifact_item (artifact_id,ordinal,kind,name,mime,size,sha256,url,label) VALUES (?,?,?,?,?,?,?,?,?)`,
			a.ID, i, it.Kind, it.Name, it.MIME, it.Size, it.SHA256, it.URL, it.Label); err != nil {
			return Artifact{}, err
		}
	}
	return a, tx.Commit()
}

const artifactColumns = `id,title,description,directory,platform,session_id,remote_id,created_at`

func scanArtifact(row interface{ Scan(...any) error }) (Artifact, error) {
	var a Artifact
	var ms int64
	err := row.Scan(&a.ID, &a.Title, &a.Description, &a.Directory, &a.Platform, &a.SessionID, &a.RemoteID, &ms)
	a.CreatedAt = time.UnixMilli(ms)
	return a, err
}

// GetArtifact returns one artifact with its items.
func (d *DB) GetArtifact(ctx context.Context, id string) (Artifact, error) {
	a, err := scanArtifact(d.db.QueryRowContext(ctx, `SELECT `+artifactColumns+` FROM artifact WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, ErrArtifactNotFound
	}
	if err != nil {
		return Artifact{}, err
	}
	list := []Artifact{a}
	if err := d.loadArtifactItems(ctx, list); err != nil {
		return Artifact{}, err
	}
	return list[0], nil
}

func (d *DB) loadArtifactItems(ctx context.Context, list []Artifact) error {
	if len(list) == 0 {
		return nil
	}
	index := map[string]int{}
	args := make([]any, len(list))
	for i, a := range list {
		index[a.ID] = i
		args[i] = a.ID
		list[i].Items = []ArtifactItem{}
	}
	rows, err := d.db.QueryContext(ctx, `SELECT artifact_id,kind,name,mime,size,sha256,url,label FROM artifact_item
		WHERE artifact_id IN (?`+strings.Repeat(",?", len(list)-1)+`) ORDER BY artifact_id, ordinal`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var it ArtifactItem
		if err := rows.Scan(&id, &it.Kind, &it.Name, &it.MIME, &it.Size, &it.SHA256, &it.URL, &it.Label); err != nil {
			return err
		}
		list[index[id]].Items = append(list[index[id]].Items, it)
	}
	return rows.Err()
}

func encodeArtifactCursor(a Artifact) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(a.CreatedAt.UnixMilli(), 10) + ":" + a.ID))
}

func decodeArtifactCursor(c string) (int64, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	ms, id, ok := strings.Cut(string(raw), ":")
	n, perr := strconv.ParseInt(ms, 10, 64)
	if err != nil || !ok || perr != nil || id == "" {
		return 0, "", ErrArtifactInvalid
	}
	return n, id, nil
}

// ListArtifacts returns one page newest first and the cursor for the next
// page ("" when exhausted). Keyset paging on (created_at DESC, id) keeps pages
// stable while new artifacts are inserted.
func (d *DB) ListArtifacts(ctx context.Context, f ArtifactFilter) ([]Artifact, string, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultArtifactPage
	}
	limit = min(limit, maxArtifactPage)
	where, args := []string{"1=1"}, []any{}
	if f.Directory != "" {
		where, args = append(where, "directory=?"), append(args, f.Directory)
	}
	if f.Platform != "" || len(f.SessionIDs) > 0 {
		if f.Platform == "" || len(f.SessionIDs) == 0 {
			return nil, "", ErrArtifactInvalid
		}
		where = append(where, "platform=? AND session_id IN (?"+strings.Repeat(",?", len(f.SessionIDs)-1)+")")
		args = append(args, f.Platform)
		for _, id := range f.SessionIDs {
			args = append(args, id)
		}
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
		where = append(where, `(title LIKE ? ESCAPE '\' OR description LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM artifact_item i WHERE i.artifact_id=artifact.id AND i.name LIKE ? ESCAPE '\'))`)
		args = append(args, like, like, like)
	}
	if f.Cursor != "" {
		ms, id, err := decodeArtifactCursor(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		where, args = append(where, "(created_at < ? OR (created_at = ? AND id > ?))"), append(args, ms, ms, id)
	}
	rows, err := d.db.QueryContext(ctx, `SELECT `+artifactColumns+` FROM artifact WHERE `+strings.Join(where, " AND ")+
		` ORDER BY created_at DESC, id LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return nil, "", err
	}
	list := []Artifact{}
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			rows.Close()
			return nil, "", err
		}
		list = append(list, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		next = encodeArtifactCursor(list[limit-1])
	}
	return list, next, d.loadArtifactItems(ctx, list)
}

// TotalArtifactBytes is the stored payload size, counting shared blobs once.
func (d *DB) TotalArtifactBytes(ctx context.Context) (int64, error) {
	var n int64
	err := d.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM (SELECT MAX(size) AS size FROM artifact_item WHERE kind='file' GROUP BY sha256)`).Scan(&n)
	return n, err
}

// CountArtifacts returns the number of stored artifacts.
func (d *DB) CountArtifacts(ctx context.Context) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifact`).Scan(&n)
	return n, err
}

// DeleteArtifact removes an artifact, its items and shares, then any blob no
// other artifact still references.
func (d *DB) DeleteArtifact(ctx context.Context, id string) error {
	d.artifactMu.Lock()
	defer d.artifactMu.Unlock()
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT sha256 FROM artifact_item WHERE artifact_id=? AND kind='file'`, id)
	if err != nil {
		return err
	}
	var sums []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		sums = append(sums, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// Explicit child deletes keep this correct even without foreign_keys=ON.
	for _, q := range []string{`DELETE FROM artifact_item WHERE artifact_id=?`, `DELETE FROM artifact_share WHERE artifact_id=?`} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM artifact WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrArtifactNotFound
	}
	var orphans []string
	for _, s := range sums {
		var used bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_item WHERE sha256=?)`, s).Scan(&used); err != nil {
			return err
		}
		if !used {
			orphans = append(orphans, s)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return d.removeArtifactBlobs(orphans)
}
